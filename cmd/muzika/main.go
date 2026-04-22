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
	"github.com/macabc/muzika/internal/preferences"
	"github.com/macabc/muzika/internal/queue"
	"github.com/macabc/muzika/internal/search"
	"github.com/macabc/muzika/internal/similarity"
	discogsbuckets "github.com/macabc/muzika/internal/similarity/buckets/discogs"
	similarityplugin "github.com/macabc/muzika/internal/similarity/plugin"
	"github.com/macabc/muzika/internal/soulseek"
	"github.com/macabc/muzika/internal/web"

	"github.com/google/uuid"

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
	prefSvc := preferences.NewService(preferences.NewRepo(database))
	defaultBandcamp := ""
	if len(cfg.BandcampDefaultTags) > 0 {
		defaultBandcamp = cfg.BandcampDefaultTags[0]
	}
	defaultDiscogs := ""
	if len(cfg.DiscogsDefaultGenres) > 0 {
		defaultDiscogs = cfg.DiscogsDefaultGenres[0]
	}
	// Adapter: turn *preferences.Service into a queue.PreferredGenres function.
	// Errors collapse to empty slices — the refiller falls back to defaults,
	// which is the same behavior as a user with no prefs.
	prefLookup := func(ctx context.Context, userID uuid.UUID) ([]string, []string) {
		p, err := prefSvc.Get(ctx, userID)
		if err != nil {
			log.Warn("refiller: preferences lookup failed; falling back to defaults",
				"user_id", userID, "err", err)
			return nil, nil
		}
		return p.BandcampTags, p.DiscogsGenres
	}
	qSvc := queue.NewServiceFull(
		ctx, database, cfg.MusicStoragePath, cfg.MinQueueSize,
		defaultBandcamp, defaultDiscogs,
		b, dispatcher, cfg.DiscogsEnabled, cfg.DiscogsWeight, prefLookup,
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

	// ---- Optional: Discogs client (seeder + preview endpoint share it) ----
	//
	// The client instance is shared between the passive-refill seeder
	// (StartWorkers) and the user-typeahead Previewer (v0.4.1 PR C). Both
	// use the same 30-day SQLite cache, so the dropdown hits are free
	// once any path has fetched a query.
	//
	// When Discogs is disabled, dgClient stays nil and the Previewer
	// returns ErrDiscogsDisabled → /api/queue/search/preview answers 503.
	var dgClient *discogs.Client
	if cfg.DiscogsEnabled {
		dgClient = discogs.NewClient(
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
	previewer := search.NewPreviewer(dgClient).WithSoulseek(sk)

	// ---- Similarity service (v0.5 PR B) ----
	//
	// Continuous similar-mode refill source. simQueueAdapter
	// bridges to queue.Service + discogs.Client without an
	// import cycle (mirrors playlist.AlbumExpander pattern).
	// PR B registers two buckets (same_artist, same_label_era);
	// PR C adds three more.
	//
	// Buckets are skipped when Discogs isn't configured — they'd
	// have nothing to query — and similar mode just returns no
	// candidates, falling back to the genre path.
	simRepo := similarity.NewRepo(database)
	simAdapter := &simQueueAdapter{q: qSvc, dg: dgClient}
	simSvc := similarity.NewService(similarity.Config{
		SeedReader:   simAdapter,
		SongAcquirer: simAdapter,
		Deduper:      simAdapter,
		// v0.5 PR D: Repo satisfies WeightStore — per-user tuned
		// weights flow in via the bucket_weights JSON column.
		// Missing values fall through to bucket defaults.
		Weights:   simRepo,
		Bus:       b,
		Discovery: logw,
	})
	if dgClient != nil {
		simSvc.Register(discogsbuckets.NewSameArtist(dgClient))
		simSvc.Register(discogsbuckets.NewSameLabelEra(dgClient))
		simSvc.Register(discogsbuckets.NewSameStyleEra(dgClient))
		simSvc.Register(discogsbuckets.NewCollaborators(dgClient))
		simSvc.Register(discogsbuckets.NewSameGenreEra(dgClient))
		log.Info("similarity: registered Discogs buckets",
			"buckets", len(simSvc.Buckets()))
	} else {
		log.Info("similarity: no Discogs client; running with empty bucket registry")
	}
	// v0.6 PR A: filesystem-discovered plugin buckets. MUZIKA_
	// BUCKET_PLUGIN_DIR points at a directory whose subdirs each
	// contain an executable named "bucket". One child process
	// per plugin, registered on simSvc alongside the built-ins.
	// Lifecycle is owned by the Manager; Close on shutdown.
	pluginMgr := similarityplugin.NewManager("v0.6-dev", log)
	if err := pluginMgr.Load(ctx, cfg.BucketPluginDir); err != nil {
		log.Warn("similarity: plugin scan failed", "err", err)
	}
	for _, pb := range pluginMgr.Buckets() {
		simSvc.Register(pb)
	}
	simSvc.StartWorkers(ctx)

	// Wire the refiller's similar-mode hook. v0.6: returns the
	// full seed set; refiller random-picks one per stub. Errors
	// collapse to an empty slice → refiller falls through to
	// genre-random for that cycle.
	qSvc.Refiller().WithSimilarMode(func(ctx context.Context, userID uuid.UUID) []uuid.UUID {
		seeds, err := simRepo.SeedsFor(ctx, userID)
		if err != nil {
			log.Warn("similarity: SeedsFor failed; falling back to genre",
				"err", err, "user_id", userID)
			return nil
		}
		return seeds
	})

	// ---- HTTP ----
	srv := buildServer(cfg, log, authSvc, plSvc, qSvc, prefSvc, previewer, simRepo, simSvc)
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
	// v0.6 PR A: terminate spawned plugin children before we
	// unwind the bus, so a plugin mid-response doesn't see a
	// vanished subscriber. Idempotent; safe to call even if no
	// plugins were loaded.
	if err := pluginMgr.Close(); err != nil {
		log.Warn("plugin manager shutdown", "err", err)
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
	pref *preferences.Service,
	previewer *search.Previewer,
	simRepo *similarity.Repo,
	simSvc *similarity.Service,
) *http.Server {
	mux := http.NewServeMux()

	authH := auth.NewHandler(a)
	// v0.4.4: wire the cross-module album expander used by
	// POST /api/playlist/{id}/album. The adapter is a thin closure
	// over the existing previewer (Discogs tracklist fetch) and
	// queue service (per-track acquire), so playlist doesn't import
	// either module directly.
	plH := playlist.NewHandler(p).WithAlbumExpander(&albumExpander{prev: previewer, q: q})
	qH := queue.NewHandler(q)
	prefH := preferences.NewHandler(pref)
	searchH := search.NewHandler(previewer)

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
	mux.Handle("POST /api/playlist/{id}/album", withAuth(http.HandlerFunc(plH.AddAlbum)))
	// v0.4.4: AlbumView on-mount re-probe hook. Not scoped to a
	// particular playlist — we re-probe every not_found entry the
	// user has for the release, regardless of which playlist holds it.
	// Mounted outside /api/playlist/ because /api/playlist/album/{…}
	// would conflict with /api/playlist/{id}/song/{…} in Go 1.22's
	// pattern matcher.
	mux.Handle("POST /api/album/{releaseId}/reprobe", withAuth(http.HandlerFunc(plH.ReprobeAlbum)))

	mux.Handle("GET /api/queue/queue", withAuth(http.HandlerFunc(qH.GetQueue)))
	mux.Handle("POST /api/queue/queue", withAuth(http.HandlerFunc(qH.AddSong)))
	mux.Handle("POST /api/queue/queue/check", withAuth(http.HandlerFunc(qH.Check)))
	mux.Handle("POST /api/queue/queue/skipped", withAuth(http.HandlerFunc(qH.Skipped)))
	mux.Handle("POST /api/queue/queue/finished", withAuth(http.HandlerFunc(qH.Finished)))
	mux.Handle("POST /api/queue/search", withAuth(http.HandlerFunc(qH.Search)))
	// v0.4.1 PR C — typeahead preview. Stateless; wraps Discogs /database/search.
	mux.Handle("GET /api/queue/search/preview", withAuth(http.HandlerFunc(searchH.Preview)))
	// v0.4.2 PR C — artist / label / release detail browse routes.
	// Read-only Discogs wrappers — no queue state mutation.
	mux.Handle("GET /api/discogs/artist/{id}", withAuth(http.HandlerFunc(searchH.Artist)))
	mux.Handle("GET /api/discogs/label/{id}", withAuth(http.HandlerFunc(searchH.Label)))
	mux.Handle("GET /api/discogs/release/{id}", withAuth(http.HandlerFunc(searchH.Release)))
	// v0.4.2 PR D — bulk Soulseek availability probe for artist/label/album pages.
	mux.Handle("POST /api/queue/search/availability", withAuth(http.HandlerFunc(searchH.Availability)))
	// v0.4.2 PR E — artist-broad availability: ONE search per artist,
	// client-side filename filter. Replaces per-release probes on
	// ArtistView/AlbumView for better efficiency + reliability.
	mux.Handle("POST /api/queue/search/availability/by-artist", withAuth(http.HandlerFunc(searchH.AvailabilityByArtist)))
	mux.Handle("DELETE /api/queue/queue/{id}", withAuth(http.HandlerFunc(qH.RemoveSong)))
	mux.Handle("GET /api/queue/songs/{id}", withAuth(http.HandlerFunc(qH.StreamSong)))
	mux.Handle("GET /api/queue/songs/{id}/liked", withAuth(http.HandlerFunc(qH.IsLiked)))
	mux.Handle("POST /api/queue/songs/{id}/liked", withAuth(http.HandlerFunc(qH.Like)))
	mux.Handle("POST /api/queue/songs/{id}/unliked", withAuth(http.HandlerFunc(qH.Unlike)))

	// --- Similar mode toggle (v0.5 PR B) ---
	// GET returns the user's active seed (or null); POST sets it
	// (or clears with seedSongId=null). Refiller reads the same
	// state on each Trigger via the SimilarMode hook wired above.
	simH := similarity.NewHandler(simRepo, simSvc)
	mux.Handle("GET /api/queue/similar-mode", withAuth(http.HandlerFunc(simH.Get)))
	mux.Handle("POST /api/queue/similar-mode", withAuth(http.HandlerFunc(simH.Set)))
	// v0.6 PR D: multi-seed add/remove. The POST on similar-mode
	// replaces the entire set; these operate on one element at a
	// time so the frontend's "+ add this song" / "× remove" UX is
	// a single round-trip per action.
	mux.Handle("POST /api/queue/similar-mode/seeds/{songId}", withAuth(http.HandlerFunc(simH.AddSeed)))
	mux.Handle("DELETE /api/queue/similar-mode/seeds/{songId}", withAuth(http.HandlerFunc(simH.RemoveSeed)))
	// v0.5 PR D: bucket registry + per-user weight tuning.
	// Registry is read-only (code-owned); weights are per-user
	// JSON merged with defaults at pick time.
	mux.Handle("GET /api/similarity/buckets", withAuth(http.HandlerFunc(simH.ListBuckets)))
	mux.Handle("GET /api/similarity/weights", withAuth(http.HandlerFunc(simH.GetWeights)))
	mux.Handle("PUT /api/similarity/weights", withAuth(http.HandlerFunc(simH.PutWeights)))

	// --- User preferences (v0.4.1 PR A) ---
	mux.Handle("GET /api/user/preferences", withAuth(http.HandlerFunc(prefH.Get)))
	mux.Handle("PUT /api/user/preferences", withAuth(http.HandlerFunc(prefH.Put)))

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

// albumExpander adapts search.Previewer + queue.Service to the
// playlist.AlbumExpander interface. v0.4.4. Lives here (not in
// playlist or any other module) so neither side has to import the
// other's concerns.
type albumExpander struct {
	prev *search.Previewer
	q    *queue.Service
}

// Album fetches the Discogs release detail and packs it into the
// playlist.Album shape: artist string, cover URL (prefer the
// larger cover, fall back to the small thumb), and title-only
// tracklist.
func (a *albumExpander) Album(ctx context.Context, releaseID int) (playlist.Album, error) {
	rd, err := a.prev.Release(ctx, releaseID)
	if err != nil {
		// Map "no such release" / "Discogs disabled" to playlist's
		// own ErrNotFound so the handler returns 404 cleanly. Other
		// upstream errors (rate limit, network) bubble as-is to a 502.
		if errors.Is(err, search.ErrDiscogsDisabled) {
			return playlist.Album{}, playlist.ErrNotFound
		}
		return playlist.Album{}, err
	}
	titles := make([]string, 0, len(rd.Tracks))
	for _, t := range rd.Tracks {
		if t.Title != "" {
			titles = append(titles, t.Title)
		}
	}
	imageURL := rd.Cover
	if imageURL == "" {
		imageURL = rd.Thumb
	}
	return playlist.Album{
		Artist:   rd.Artist,
		ImageURL: imageURL,
		Tracks:   titles,
	}, nil
}

// AcquireForUser triggers the existing search-acquire path for one
// (title, artist, imageURL) triple. Returns the queue_songs UUID
// the playlist handler appends to the chosen playlist. Equivalent
// to a frontend click on a release tile in the preview dropdown.
func (a *albumExpander) AcquireForUser(ctx context.Context, userID uuid.UUID, title, artist, imageURL string) (uuid.UUID, error) {
	resp, err := a.q.Search(ctx, userID, queue.SearchRequest{
		Title:    title,
		Artist:   artist,
		ImageURL: imageURL,
		Query:    artist + " — " + title,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return resp.SongID, nil
}

// ReprobeNotFoundTrack forwards to queue.Service.ReprobeNotFoundTrack.
// v0.4.4 — the AlbumView on-mount hook fans this out for each track
// of the release; returns true when a re-probe was scheduled.
func (a *albumExpander) ReprobeNotFoundTrack(ctx context.Context, userID uuid.UUID, title, artist string) (bool, error) {
	return a.q.ReprobeNotFoundTrack(ctx, userID, title, artist)
}

// simQueueAdapter implements similarity.SeedReader,
// similarity.SongAcquirer, and similarity.QueueDeduper against
// the existing queue.Service + queue.Repo + discogs.Client.
// Mirrors the playlist.AlbumExpander pattern — keeps
// internal/similarity free of internal/queue + internal/discogs
// imports.
type simQueueAdapter struct {
	q  *queue.Service
	dg *discogs.Client // nil → no hydration; buckets receive a partial Seed
}

// ReadSeed pulls (title, artist) from queue_songs and, when
// Discogs is configured, resolves the matching Discogs release
// to populate the similarity features the buckets need
// (artist_id, label_id, year, styles, genres, collaborators).
//
// Hydration is best-effort: a Discogs miss returns a partial
// Seed with just title+artist set. Buckets bail out gracefully
// on zero IDs and the engine collapses an empty fan-out into
// ErrNoCandidates / ErrSeedUnknown — refiller falls back to
// genre-random for that cycle.
//
// Cache fully amortized: search hit + release detail hit are
// both cached for 30 days, so subsequent refills with the same
// seed cost zero Discogs API calls.
func (a *simQueueAdapter) ReadSeed(ctx context.Context, userID, songID uuid.UUID) (similarity.Seed, error) {
	sg, err := a.q.Repo().GetSong(ctx, songID)
	if err != nil {
		return similarity.Seed{SongID: songID, UserID: userID}, err
	}
	seed := similarity.Seed{
		SongID: songID,
		UserID: userID,
		Title:  sg.Title,
		Artist: sg.Artist,
	}
	if a.dg == nil || sg.Title == "" || sg.Artist == "" {
		return seed, nil
	}
	// Step 1: resolve title+artist → Discogs release id.
	// Cached search call; subsequent refills are free.
	hit, err := a.dg.SearchQuery(ctx, sg.Artist+" "+sg.Title)
	if err != nil || hit.ID == 0 {
		return seed, nil
	}
	seed.DiscogsReleaseID = hit.ID
	// Step 2: full release detail → features. Also cached.
	rd, err := a.dg.Release(ctx, hit.ID)
	if err != nil {
		return seed, nil
	}
	seed.DiscogsArtistID = rd.ArtistID
	seed.DiscogsLabelID = rd.LabelID
	seed.Year = rd.Year
	seed.Styles = rd.Styles
	seed.Genres = rd.Genres
	seed.Collaborators = rd.Collaborators
	return seed, nil
}

// AcquireForUser routes a similarity pick through the existing
// search-acquire path so it lands in queue_songs with the same
// probe + ladder + image_url plumbing as any other queued release.
// Identical body to albumExpander.AcquireForUser; kept distinct
// so future similarity-specific tweaks (different correlation
// query string, e.g.) don't bleed into the playlist module.
func (a *simQueueAdapter) AcquireForUser(ctx context.Context, userID uuid.UUID, title, artist, imageURL string) (uuid.UUID, error) {
	resp, err := a.q.Search(ctx, userID, queue.SearchRequest{
		Title:    title,
		Artist:   artist,
		ImageURL: imageURL,
		Query:    artist + " — " + title,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return resp.SongID, nil
}

// HasEntry reports whether the user already has a queue_entries
// row for this (title, artist). Engine consults this after the
// merge step to drop dupes — keeps similar mode from queuing the
// same track twice. Errors collapse to false: better to risk a
// duplicate than skip a real proposal because of a transient SQL
// blip.
func (a *simQueueAdapter) HasEntry(ctx context.Context, userID uuid.UUID, title, artist string) bool {
	id, _, err := a.q.Repo().FindEntry(ctx, userID, title, artist)
	if err != nil {
		return false
	}
	return id != uuid.Nil
}
