// Package config loads runtime configuration from environment variables.
// Single source of truth: see ARCHITECTURE.md §10 and .env.example.
package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

const envPrefix = "MUZIKA"

// Config is the full set of runtime knobs. Populate with Load().
type Config struct {
	HTTPPort int `envconfig:"HTTP_PORT" default:"8080"`

	DBPath           string `envconfig:"DB_PATH"            default:"/data/muzika.db"`
	MusicStoragePath string `envconfig:"MUSIC_STORAGE_PATH" default:"/data/music"`

	JWTSecret     string        `envconfig:"JWT_SECRET"     required:"true"`
	JWTExpiration time.Duration `envconfig:"JWT_EXPIRATION" default:"24h"`

	// Empty = no CORS headers emitted. No wildcards; list explicit origins.
	CORSOrigins []string `envconfig:"CORS_ORIGINS"`

	// Soulseek network credentials — register free at https://www.slsknet.org/.
	// The gosk backend uses these to authenticate to the public Soulseek server.
	SoulseekUsername      string `envconfig:"SOULSEEK_USERNAME"       required:"true"`
	SoulseekPassword      string `envconfig:"SOULSEEK_PASSWORD"       required:"true"`
	SoulseekServerAddress string `envconfig:"SOULSEEK_SERVER_ADDRESS" default:"server.slsknet.org"`
	SoulseekServerPort    int    `envconfig:"SOULSEEK_SERVER_PORT"    default:"2242"`
	SoulseekListenPort    int    `envconfig:"SOULSEEK_LISTEN_PORT"    default:"2234"`
	GoskStatePath         string `envconfig:"GOSK_STATE_PATH"         default:"/data/gosk-state.db"`

	MinQueueSize        int      `envconfig:"MIN_QUEUE_SIZE"        default:"10"`
	BandcampWorkers     int      `envconfig:"BANDCAMP_WORKERS"      default:"2"`
	DownloadWorkers     int      `envconfig:"DOWNLOAD_WORKERS"      default:"2"`
	BandcampDefaultTags []string `envconfig:"BANDCAMP_DEFAULT_TAGS" default:"electronic,house"`
	BusBufferSize       int      `envconfig:"BUS_BUFFER_SIZE"       default:"64"`

	// --- Discogs seeder (v0.4 PR 2) ---
	// Disabled by default so existing deployments keep working on upgrade;
	// operators flip it on after generating a Personal Access Token at
	// https://www.discogs.com/settings/developers.
	DiscogsEnabled       bool     `envconfig:"DISCOGS_ENABLED"        default:"false"`
	DiscogsToken         string   `envconfig:"DISCOGS_TOKEN"` // required only when enabled
	DiscogsWorkers       int      `envconfig:"DISCOGS_WORKERS"        default:"1"`
	DiscogsDefaultGenres []string `envconfig:"DISCOGS_DEFAULT_GENRES" default:"Electronic,Rock"`
	// DiscogsWeight is the probability the refiller routes a DiscoveryIntent
	// to Discogs (vs Bandcamp). 0.3 default keeps Bandcamp as the reliability
	// floor while giving Discogs a third of the queue; set to 0 to disable
	// without unsetting DISCOGS_ENABLED.
	DiscogsWeight float64 `envconfig:"DISCOGS_WEIGHT" default:"0.3"`

	// --- Quality gate (v0.4 PR 2) ---
	// Floors applied to every Soulseek SearchResult in download/gate.go.
	// The gate is strict by default; if strict rejects all peers at a given
	// ladder rung, the worker relaxes (halves thresholds) once before
	// declaring the rung a miss. See ROADMAP §v0.4 item 3.
	DownloadMinBitrateKbps int   `envconfig:"DOWNLOAD_MIN_BITRATE_KBPS" default:"192"`
	DownloadMinFileBytes   int64 `envconfig:"DOWNLOAD_MIN_FILE_BYTES"   default:"2000000"`   // 2 MB
	DownloadMaxFileBytes   int64 `envconfig:"DOWNLOAD_MAX_FILE_BYTES"   default:"200000000"` // 200 MB
	DownloadPeerMaxQueue   int   `envconfig:"DOWNLOAD_PEER_MAX_QUEUE"   default:"50"`

	// --- Ladder search strategy (v0.4 PR 2) ---
	// Three rungs in order: [catno, artist+title, title]. The ladder exits
	// at the first rung whose post-gate result count ≥ LadderEnoughResults.
	// RungWindow bounds the per-rung Soulseek search; the first rung uses
	// the existing searchWindow, subsequent rungs use this shorter value
	// to cap worst-case miss latency.
	DownloadLadderEnabled       bool          `envconfig:"DOWNLOAD_LADDER_ENABLED"        default:"true"`
	DownloadLadderEnoughResults int           `envconfig:"DOWNLOAD_LADDER_ENOUGH_RESULTS" default:"3"`
	DownloadLadderRungWindow    time.Duration `envconfig:"DOWNLOAD_LADDER_RUNG_WINDOW"    default:"5s"`

	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`
}

// Load reads environment variables (prefix MUZIKA_) into a Config and validates it.
func Load() (Config, error) {
	var c Config
	if err := envconfig.Process(envPrefix, &c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validate() error {
	if c.MinQueueSize < 1 {
		return fmt.Errorf("config: MIN_QUEUE_SIZE must be >= 1")
	}
	if c.BandcampWorkers < 1 || c.DownloadWorkers < 1 {
		return fmt.Errorf("config: worker counts must be >= 1")
	}
	if c.DiscogsEnabled {
		if c.DiscogsToken == "" {
			return fmt.Errorf("config: DISCOGS_ENABLED=true requires DISCOGS_TOKEN")
		}
		if c.DiscogsWorkers < 1 {
			return fmt.Errorf("config: DISCOGS_WORKERS must be >= 1 when enabled")
		}
	}
	if c.DiscogsWeight < 0 || c.DiscogsWeight > 1 {
		return fmt.Errorf("config: DISCOGS_WEIGHT must be in [0.0, 1.0]")
	}
	if c.DownloadMinBitrateKbps < 0 {
		return fmt.Errorf("config: DOWNLOAD_MIN_BITRATE_KBPS must be >= 0")
	}
	if c.DownloadMinFileBytes < 0 || c.DownloadMaxFileBytes < c.DownloadMinFileBytes {
		return fmt.Errorf("config: DOWNLOAD_MAX_FILE_BYTES must be >= DOWNLOAD_MIN_FILE_BYTES")
	}
	if c.DownloadPeerMaxQueue < 0 {
		return fmt.Errorf("config: DOWNLOAD_PEER_MAX_QUEUE must be >= 0")
	}
	if c.DownloadLadderEnoughResults < 1 {
		return fmt.Errorf("config: DOWNLOAD_LADDER_ENOUGH_RESULTS must be >= 1")
	}
	return nil
}
