// Command muzika is the single-binary entry point. It owns process lifecycle:
// load config, open DB, run migrations, wire services, start worker pools,
// mount HTTP routes, serve, and shut down gracefully on signal.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/macabc/muzika/internal/auth"
	"github.com/macabc/muzika/internal/bandcamp"
	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/config"
	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/discovery"
	"github.com/macabc/muzika/internal/download"
	"github.com/macabc/muzika/internal/httpx"
	"github.com/macabc/muzika/internal/playlist"
	"github.com/macabc/muzika/internal/queue"
	"github.com/macabc/muzika/internal/soulseek"
	"github.com/macabc/muzika/internal/web"

	"github.com/ABCslime/gosk"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg.LogLevel)

	// GC tuning: pairs with GOMEMLIMIT set in systemd unit.
	// See ARCHITECTURE.md §1 memory budget.
	runtime.SetBlockProfileRate(0)

	// ---- DB + migrations ----
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer database.Close()

	if err := db.MigrateEmbedded(database); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	log.Info("migrations applied")

	// ---- Bus + outbox ----
	b := bus.New(cfg.BusBufferSize, log)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	dispatcher := bus.StartOutboxDispatcher(ctx, database, b, log)

	// ---- Soulseek backend (native gosk — single binary, no sidecar) ----
	sk, err := soulseek.NewNativeClient(nativeGoskConfig(cfg))
	if err != nil {
		return fmt.Errorf("init gosk: %w", err)
	}

	// ---- Discovery observability (v0.4 PR 2) ----
	logw := discovery.NewWriter(database)

	// ---- Services ----
	authSvc := auth.NewService(database, cfg.JWTSecret, cfg.JWTExpiration, b, dispatcher)
	plSvc := playlist.NewService(database, b)
	defaultGenre := ""
	if len(cfg.BandcampDefaultTags) > 0 {
		defaultGenre = cfg.BandcampDefaultTags[0]
	}
	qSvc := queue.NewServiceWithDiscogs(
		ctx, database, cfg.MusicStoragePath, cfg.MinQueueSize, defaultGenre,
		b, dispatcher, cfg.DiscogsEnabled, cfg.DiscogsWeight,
	)
	bcSvc := bandcamp.NewService(bandcamp.NewClient("https://bandcamp.com", cfg.BandcampDefaultTags), database, b, dispatcher)

	dlCfg := download.Config{
		Gate: download.GateConfig{
			MinBitrateKbps: cfg.DownloadMinBitrateKbps,
			MinFileBytes:   cfg.DownloadMinFileBytes,
			MaxFileBytes:   cfg.DownloadMaxFileBytes,
			PeerMaxQueue:   cfg.DownloadPeerMaxQueue,
		},
		LadderEnabled:  cfg.DownloadLadderEnabled,
		LadderEnough:   cfg.DownloadLadderEnoughResults,
		LadderRungWait: cfg.DownloadLadderRungWindow,
	}
	dlSvc := download.NewServiceWithConfig(database, sk, cfg.MusicStoragePath, b, dispatcher, logw, dlCfg)

	// ---- Start worker pools ----
	plSvc.StartWorkers(ctx)
	qSvc.StartWorkers(ctx)
	bcSvc.StartWorkers(ctx, cfg.BandcampWorkers)
	dlSvc.StartWorkers(ctx, cfg.DownloadWorkers)

	// ---- Optional: Discogs seeder (v0.4 PR 2) ----
	if cfg.DiscogsEnabled {
		dgClient := discogs.NewClient(
			discogs.DefaultBaseURL,
			cfg.DiscogsToken,
			cfg.DiscogsDefaultGenres,
			discogs.WithCache(database),
		)
		dgClient.SweepCache(ctx) // best-effort prune of >30-day rows
		dgSvc := discogs.NewService(dgClient, database, b, dispatcher, logw)
		dgSvc.StartWorkers(ctx, cfg.DiscogsWorkers)
		log.Info("discogs seeder enabled", "workers", cfg.DiscogsWorkers, "weight", cfg.DiscogsWeight)
	}

	// ---- HTTP ----
	srv := buildServer(cfg, log, authSvc, plSvc, qSvc)
	go func() {
		log.Info("http listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server stopped", "err", err)
			cancel()
		}
	}()

	// ---- Wait for signal ----
	<-ctx.Done()
	log.Info("shutdown signal received")

	// ---- Graceful shutdown ----
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}
	dispatcher.Stop()
	b.Close()
	b.Wait()
	log.Info("exited cleanly")
	return nil
}

func buildServer(
	cfg config.Config,
	log *slog.Logger,
	a *auth.Service,
	p *playlist.Service,
	q *queue.Service,
) *http.Server {
	mux := http.NewServeMux()

	authH := auth.NewHandler(a)
	plH := playlist.NewHandler(p)
	qH := queue.NewHandler(q)

	withAuth := httpx.WithAuth(a.Verifier())

	// --- Public ---
	mux.HandleFunc("POST /api/auth/user", authH.Register)
	mux.HandleFunc("POST /api/auth/login", authH.Login)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// --- Protected — each wrapped explicitly ---
	mux.Handle("DELETE /api/auth/user/{id}", withAuth(http.HandlerFunc(authH.Delete)))
	mux.Handle("POST /api/auth/logout-all", withAuth(http.HandlerFunc(authH.LogoutAll)))

	mux.Handle("GET /api/playlist/", withAuth(http.HandlerFunc(plH.List)))
	mux.Handle("GET /api/playlist/{id}", withAuth(http.HandlerFunc(plH.Get)))
	mux.Handle("POST /api/playlist/", withAuth(http.HandlerFunc(plH.Create)))
	mux.Handle("DELETE /api/playlist/{id}", withAuth(http.HandlerFunc(plH.Delete)))
	mux.Handle("POST /api/playlist/{id}/song/{songId}", withAuth(http.HandlerFunc(plH.AddSong)))
	mux.Handle("DELETE /api/playlist/{id}/song/{songId}", withAuth(http.HandlerFunc(plH.RemoveSong)))

	mux.Handle("GET /api/queue/queue", withAuth(http.HandlerFunc(qH.GetQueue)))
	mux.Handle("POST /api/queue/queue", withAuth(http.HandlerFunc(qH.AddSong)))
	mux.Handle("POST /api/queue/queue/check", withAuth(http.HandlerFunc(qH.Check)))
	mux.Handle("POST /api/queue/queue/skipped", withAuth(http.HandlerFunc(qH.Skipped)))
	mux.Handle("POST /api/queue/queue/finished", withAuth(http.HandlerFunc(qH.Finished)))
	mux.Handle("DELETE /api/queue/queue/{id}", withAuth(http.HandlerFunc(qH.RemoveSong)))
	mux.Handle("GET /api/queue/songs/{id}", withAuth(http.HandlerFunc(qH.StreamSong)))
	mux.Handle("GET /api/queue/songs/{id}/liked", withAuth(http.HandlerFunc(qH.IsLiked)))
	mux.Handle("POST /api/queue/songs/{id}/liked", withAuth(http.HandlerFunc(qH.Like)))
	mux.Handle("POST /api/queue/songs/{id}/unliked", withAuth(http.HandlerFunc(qH.Unlike)))

	// --- SPA (fallback for everything else) ---
	mux.Handle("GET /", web.SPAHandler())

	// Wrap globally with panic recovery, CORS, request log.
	handler := httpx.Recover(log)(
		httpx.CORS(httpx.CORSConfig{Origins: cfg.CORSOrigins})(
			httpx.RequestLog(log)(mux),
		),
	)

	return &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.HTTPPort),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// nativeGoskConfig translates muzika's Config into a *gosk.Config for the
// native Soulseek backend.
func nativeGoskConfig(cfg config.Config) *gosk.Config {
	g := gosk.DefaultConfig()
	g.Username = cfg.SoulseekUsername
	g.Password = cfg.SoulseekPassword
	g.SoulSeekAddress = cfg.SoulseekServerAddress
	g.SoulSeekPort = cfg.SoulseekServerPort
	g.OwnPort = cfg.SoulseekListenPort
	g.DownloadFolder = cfg.MusicStoragePath
	g.StatePath = cfg.GoskStatePath
	return g
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}
