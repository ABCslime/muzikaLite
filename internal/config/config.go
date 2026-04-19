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

	// "slskd" (ships day one) or "native" (gosk, not available in v1).
	SoulseekBackend string `envconfig:"SOULSEEK_BACKEND" default:"slskd"`
	SlskdURL        string `envconfig:"SLSKD_URL"        default:"http://slskd:5030"`
	SlskdUsername   string `envconfig:"SLSKD_USERNAME"`
	SlskdPassword   string `envconfig:"SLSKD_PASSWORD"`

	MinQueueSize        int      `envconfig:"MIN_QUEUE_SIZE"        default:"10"`
	BandcampWorkers     int      `envconfig:"BANDCAMP_WORKERS"      default:"2"`
	SlskdWorkers        int      `envconfig:"SLSKD_WORKERS"         default:"2"`
	BandcampDefaultTags []string `envconfig:"BANDCAMP_DEFAULT_TAGS" default:"electronic,house"`
	BusBufferSize       int      `envconfig:"BUS_BUFFER_SIZE"       default:"64"`

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
	switch c.SoulseekBackend {
	case "slskd":
		if c.SlskdURL == "" || c.SlskdUsername == "" || c.SlskdPassword == "" {
			return fmt.Errorf("config: SOULSEEK_BACKEND=slskd requires SLSKD_URL, SLSKD_USERNAME, SLSKD_PASSWORD")
		}
	case "native":
		// gosk not available in v1; main.go will exit with a clearer message.
	default:
		return fmt.Errorf("config: unknown SOULSEEK_BACKEND %q (want slskd|native)", c.SoulseekBackend)
	}
	if c.MinQueueSize < 1 {
		return fmt.Errorf("config: MIN_QUEUE_SIZE must be >= 1")
	}
	if c.BandcampWorkers < 1 || c.SlskdWorkers < 1 {
		return fmt.Errorf("config: worker counts must be >= 1")
	}
	return nil
}
