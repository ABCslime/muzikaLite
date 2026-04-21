# Muzika Platform — Phase 2 Architecture (v2)

Revision of v1 after Phase 2 review. Hardware target moved from "a Pi" (vague) to **Raspberry Pi 3, 1 GB RAM** (load-bearing). That single fact reshapes the DB choice, the build pipeline, the memory budget, and the Soulseek backend strategy. All other Phase 2 revisions apply concurrently.

See `CHANGES.md` for the mapping from each revision item to the section where it landed.

> **v0.3.0 delta** — the slskd HTTP sidecar was retired. Soulseek now runs
> in-process via [gosk](https://github.com/ABCslime/gosk), so the Pi
> deployment is a single binary plus its env file. The gosk evolution
> plan in §7 is done — it's the only backend now. The former
> `internal/slskd` package is now `internal/download`; the
> `RequestSlskdSong` event is now `RequestDownload`; the backend
> selector and its env var (`MUZIKA_SOULSEEK_BACKEND`) are gone. Sections
> below are annotated where they describe the pre-v0.3.0 design; the
> narrative is preserved for historical context.

---

## 1. Hardware constraints

**Target:** Raspberry Pi 3 Model B, 1 GB RAM, ARMv8 quad-core 1.2 GHz, SD card storage (optional USB stick for swap / audio).

Every downstream design decision in this document answers to one of these constraints:

| Constraint                    | Consequence                                                                 |
|-------------------------------|-----------------------------------------------------------------------------|
| 1 GB total RAM                | No JVM. No Postgres (≈80–150 MB base). No Kafka (≈400 MB JVM). No Strimzi. |
| SD-card-only by default       | Minimize writes; WAL-journaled SQLite; audio on USB if possible.            |
| ARMv8, low CPU budget         | Cross-compile in CI, not on-device. No emulation. No `buildx` QEMU.         |
| Home network, single user     | No HA, no replication, no horizontal scaling, no distributed consensus.     |

### Memory budget (target, steady-state)

| systemd unit                     | `MemoryMax` | `MemoryHigh` | Notes                                               |
|----------------------------------|-------------|---------------|-----------------------------------------------------|
| `muzika.service`                 | 150 MB      | 130 MB        | Static Go binary, GOMEMLIMIT=120MiB, GOGC=50. Embeds gosk in-process since v0.3.0. |
| `muzika-updater.service`         | —           | —             | `Type=oneshot`, runs ~5 s every 5 min. Negligible average. |
| **Total steady-state**           | **~150 MB** | —             | Leaves ~870 MB for kernel, journald, SSH.           |

Down from ~550 MB pre-v0.3.0 (muzika 150 + slskd 400). Down from ~740 MB under Docker+Watchtower (~130 MB of which was the Docker daemon itself). Two RAM cliffs: migrating off Docker (Phase 3.5) bought ~190 MB; retiring the slskd sidecar (v0.3.0, §7) bought another ~400 MB. See §8 for the systemd layout, §10 for the "don't bring Docker back" rationale.

No Postgres process; SQLite is in-process inside `muzika`, so its page cache counts toward muzika's 150 MB limit. Configured `PRAGMA cache_size = -8000` (≈8 MB).

### Swap mitigation (documented in README, not configured in-repo)

If the Pi OOMs, the README will instruct the operator to add swap on a USB stick — **not** on the SD card (swap churn destroys SD cards in weeks). Step-by-step:

```bash
# Plug in a USB stick (≥4 GB), identify it (e.g. /dev/sda1), format ext4
sudo mkfs.ext4 /dev/sda1
sudo mkdir /mnt/usbswap && sudo mount /dev/sda1 /mnt/usbswap
sudo fallocate -l 2G /mnt/usbswap/swapfile
sudo chmod 600 /mnt/usbswap/swapfile
sudo mkswap /mnt/usbswap/swapfile
sudo swapon /mnt/usbswap/swapfile
# Persist across reboots via /etc/fstab: /mnt/usbswap/swapfile none swap sw 0 0
```

README will also cover: turning off `zram` (duplicates work), `vm.swappiness=10`, and monitoring via `free -h` / `systemctl status muzika.service`.

---

## 2. Existing CI/CD snapshot (condensed)

The old `digeper-old/.github/` is an ops-manifest repo (K8s/Strimzi/kustomize), not a workflows repo. Each of the 6 service repos has one identical 4-job pipeline (build → Jib push to ACR → `kubectl apply -k` on AKS → notify). All Azure-specific: `ACR_*`, `AKS_*`, `AZURE_CREDENTIALS`, `KEYVAULT_NAME`, managed identities, `SecretProviderClass`.

**Carry forward, conceptually:**
- The 4-stage shape (test → build → deploy → verify).
- Tag convention `${version}-${short_sha}`.
- `workflow_dispatch` with environment + skip-tests inputs (useful for manual reruns).
- The nginx ingress routing contract: `/api/auth`, `/api/playlist`, `/api/queue`, `/` → the Go router must mount these exact prefixes so the Vue SPA works unchanged.

**Discard:** Azure entirely (ACR, AKS, Key Vault, managed identities, kustomize), Jib, Strimzi, Kafka topic CRDs, per-service workflows, HPA.

The new deployment pipeline is described in §11.

---

## 3. Repository layout

```
muzika/
├── cmd/
│   └── muzika/
│       └── main.go                     # wires everything; HTTP + workers
├── internal/
│   ├── auth/                           # authManager port
│   │   ├── handler.go
│   │   ├── service.go
│   │   ├── repo.go
│   │   ├── jwt.go                      # includes tv (token_version) claim
│   │   └── models.go
│   ├── playlist/                       # PlaylistManager port
│   ├── queue/                          # QueueManager port
│   │   ├── service.go                  # per-user sync.Mutex map here (§5)
│   │   ├── refiller.go                 # on-demand only, no ticker (§9)
│   │   └── …
│   ├── bandcamp/                       # BandcampApi port
│   ├── download/                       # THIN: wraps internal/soulseek.Client
│   │                                   # (was internal/slskd/ before v0.3.0)
│   ├── soulseek/                       # NEW (§7): SoulseekClient interface + gosk impl
│   │   ├── client.go                   # interface definition
│   │   └── native.go                   # gosk-backed implementation (sole backend since v0.3.0)
│   ├── bus/                            # in-process event bus (per-subscriber channels)
│   │   ├── bus.go
│   │   ├── events.go
│   │   ├── outbox.go                   # outbox dispatcher (§4)
│   │   └── pool.go
│   ├── db/
│   │   ├── db.go                       # SQLite open + pragmas
│   │   └── tx.go
│   ├── config/
│   │   └── config.go                   # envconfig
│   ├── httpx/
│   │   ├── middleware.go
│   │   ├── userctx.go                  # context-based userID (§6)
│   │   ├── cors.go                     # configurable, no wildcards
│   │   └── errors.go
│   └── web/
│       ├── static.go                   # //go:embed dist/*
│       └── dist/                       # populated by `npm run build` during Docker build
├── migrations/
│   ├── 0001_init.up.sql                # ONE linear stream, all tables
│   └── 0001_init.down.sql
├── frontend/                           # Vue 3 SPA, copied unchanged
├── .github/
│   └── workflows/
│       ├── build.yml                   # every push to main: test + build + push :sha-XXX
│       └── release.yml                 # every v* tag: promote :sha-XXX to :latest
├── .githooks/
│   └── pre-commit                      # blocks .env* except .env.example + .env.sops
├── deploy/
│   ├── docker-compose.yml
│   └── .env.sops                       # SOPS-encrypted, in git
├── Dockerfile
├── .env.example                        # plaintext template (tracked)
├── .gitignore                          # ignores .env, .env.local, etc.
├── CLAUDE.md
├── README.md
├── ANALYSIS.md
├── ARCHITECTURE.md
├── CHANGES.md
├── go.mod
└── go.sum
```

**Out-of-repo (separate module):**
```
gosk/                                   # github.com/ABCslime/gosk, separate go.mod
├── go.mod
├── client.go                           # implements soulseek.Client
├── session.go                          # login + persistent Soulseek session (uses bh90210/soul)
├── search.go
├── download.go
├── state.go                            # SQLite for in-flight downloads
└── README.md                           # scope doc
```

`gosk` is not in `muzika/` at all. Muzika imports it by module path. See §7 for the integration — since v0.3.0 gosk is the only backend.

### Dependency direction
```
cmd/muzika ───► all internal/*
internal/{auth,playlist,queue,bandcamp,download}
        │
        ├──► internal/bus
        ├──► internal/db
        ├──► internal/httpx
        └──► internal/config
internal/download ──► internal/soulseek   (for soulseek.Client)
internal/soulseek ──► github.com/ABCslime/gosk
```
No cross-domain imports.

### Library choices
| Concern           | Library                                    |
|-------------------|--------------------------------------------|
| HTTP router       | `net/http` (Go 1.22+ method patterns)      |
| JWT               | `github.com/golang-jwt/jwt/v5`             |
| Password hash     | `golang.org/x/crypto/bcrypt`               |
| DB driver         | `modernc.org/sqlite` (pure-Go)             |
| Migrations        | `github.com/golang-migrate/migrate/v4` + its `sqlite` source |
| Env config        | `github.com/kelseyhightower/envconfig`     |
| UUID              | `github.com/google/uuid`                   |
| HTML scraping     | `github.com/PuerkitoBio/goquery`           |
| Logging           | `log/slog`                                 |
| Soulseek wire protocol (in gosk only) | `github.com/bh90210/soul`  |

`modernc.org/sqlite` is pure-Go (no CGO), so the `linux/arm64` cross-compile in CI is a clean `GOOS=linux GOARCH=arm64 go build`.

---

## 4. Event bus: per-subscriber channels + transactional outbox

### Subscribe / Publish model

**v1 had a bug**: one channel per event type, multiple consumers reading from it — but channel receives are consumed by exactly one receiver, so the second subscriber never sees the message. The `request-slskd-song` topic (consumed by both `slskd` and `queue`) would break. (Renamed to `RequestDownload` in v0.3.0 when `internal/slskd` became `internal/download`.)

**v2 fixes it with per-subscriber channels.** `Subscribe[T]` returns a fresh channel; `Publish[T]` fans out to every subscriber's channel.

```go
// bus.Bus (shape)
type Bus struct {
    mu           sync.RWMutex
    subscribers  map[reflect.Type][]chan any   // sketch; actual impl uses generic wrappers
    log          *slog.Logger
    wg           sync.WaitGroup
    bufferSize   int
}

func Subscribe[T any](b *Bus, name string) <-chan T { /* fresh chan, registered */ }
func Publish[T any](b *Bus, ctx context.Context, event T) error {
    // For each registered subscriber of type T, non-blocking-with-timeout send.
    // Send policy per event type — see table below.
}
```

`RunPool[T]` is unchanged from v1 in shape: takes the result of `Subscribe`, spawns N goroutines, each ranging over the channel.

### Channel ↔ topic mapping (v2)

| Old Kafka topic        | Event type             | Publisher     | Subscribers (worker pool size, default)             |
|------------------------|------------------------|---------------|-----------------------------------------------------|
| `user-created`         | `UserCreatedEvent`     | `auth`        | `playlist` (1), `queue` (1)                         |
| `user-deleted` (new)   | `UserDeletedEvent`     | `auth`        | `playlist` (1), `queue` (1) — for cache invalidation only (cascade already handled by FK, §5) |
| `liked`                | `LikedSongEvent`       | `queue`       | `playlist` (1)                                      |
| `unliked`              | `UnlikedSongEvent`     | `queue`       | `playlist` (1)                                      |
| `loaded-song`          | `LoadedSongEvent`      | `download`    | `queue` (1)                                         |
| `request-random-song`  | `RequestRandomSongEvent` | `queue` (refiller) | `bandcamp` (cfg `BANDCAMP_WORKERS`, default 2) |
| `request-download`     | `RequestDownloadEvent`   | `bandcamp` | `download` (cfg `DOWNLOAD_WORKERS`, default 2), `queue` (1, for metadata update) |

The two-subscriber case on `RequestDownloadEvent` (was `RequestSlskdSongEvent` before v0.3.0) works correctly: each of `download` and `queue` gets its own channel with its own backlog.

### Transactional outbox

State-change events must **never** be silently dropped. The outbox pattern guarantees at-least-once delivery across process crashes:

1. A handler mutates state in SQLite **and writes an outbox row** in the same transaction.
2. A dispatcher goroutine polls the `outbox` table (every ~500 ms, or notified via a wake channel), publishes each row to the bus via `Publish[T]`, and on a successful return deletes the row.
3. If the process crashes mid-publish, the row is still there; it's republished on restart. Subscribers must be idempotent (they already are: "add song to Liked playlist" is INSERT OR IGNORE; "mark listen count" is an UPDATE).

**Events that go through outbox** (all state-change):
- `UserCreatedEvent`
- `UserDeletedEvent`
- `LikedSongEvent`
- `UnlikedSongEvent`
- `LoadedSongEvent`

**Events published directly** (regenerable, no outbox):
- `RequestRandomSongEvent` — if lost, the next refiller call re-emits.
- `RequestDownloadEvent` — if lost, the Bandcamp worker gets the same `RequestRandomSong` again next time the queue is short. (Was `RequestSlskdSongEvent` before v0.3.0.)

### Outbox table

```sql
CREATE TABLE outbox (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type  TEXT    NOT NULL,          -- "UserCreated", "LikedSong", ...
    payload     BLOB    NOT NULL,          -- JSON-encoded event
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_outbox_created ON outbox(created_at);
```

Dispatcher pseudocode:
```go
for {
    rows := SELECT id, event_type, payload FROM outbox ORDER BY id LIMIT 64;
    for each row:
        err := b.dispatchByType(row.event_type, row.payload)
        if err == nil {
            DELETE FROM outbox WHERE id = ?
        } else {
            log; leave row for retry
        }
    wait 500ms or wake signal
}
```

Buffer / drop policy per event type (send from dispatcher to subscriber channel):
- State-change (outbox-originated): block indefinitely. The dispatcher is single-threaded; head-of-line blocking is acceptable because state-change rate is low.
- Request events: non-blocking with 100 ms timeout; on full-channel, log-and-drop. The refiller re-emits.

---

## 5. Database: SQLite, one file, prefix-named tables

One DB file at `/data/muzika.db`. No schemas (SQLite doesn't have them). Schema-per-module becomes **table-name prefixes**: `auth_*`, `playlist_*`, `queue_*`. Outbox is a single unprefixed table (§4).

### Open sequence (internal/db/db.go)

```go
db, _ := sql.Open("sqlite", "file:/data/muzika.db?_pragma=busy_timeout(5000)")
for _, p := range []string{
    "PRAGMA journal_mode = WAL",
    "PRAGMA foreign_keys = ON",
    "PRAGMA busy_timeout = 5000",       // belt + suspenders with the URL-level pragma
    "PRAGMA synchronous = NORMAL",      // safe with WAL, faster than FULL
    "PRAGMA cache_size = -8000",        // ~8 MB page cache
    "PRAGMA temp_store = MEMORY",
} { _, err := db.Exec(p); /* fail fast */ }
db.SetMaxOpenConns(1)                   // SQLite writes are serialized anyway;
                                        // single connection avoids lock contention,
                                        // and we use a per-user mutex (below) for
                                        // logical concurrency.
```

Actually — SetMaxOpenConns(1) serializes **all** DB work, which is fine for our scale on a Pi 3 but worth calling out. Readers go through the same connection; WAL makes concurrent readers safe at the SQLite level, but Go's `database/sql` serializes on the connection anyway. For this workload (one user, ≤10 req/s peak), a single connection is not the bottleneck.

### Per-user queue mutex

Queue position reordering is the only workload with real logical concurrency — two HTTP handlers on the same user racing to insert at position N. Solution: a map of `sync.Mutex` keyed by `userID`, held for the duration of any queue-mutating operation.

```go
// internal/queue/service.go
type Service struct {
    db        *sql.DB
    muUsers   sync.Mutex                 // protects userLocks map itself
    userLocks map[uuid.UUID]*sync.Mutex
    // ...
}

func (s *Service) lockFor(userID uuid.UUID) func() {
    s.muUsers.Lock()
    m, ok := s.userLocks[userID]
    if !ok {
        m = &sync.Mutex{}
        s.userLocks[userID] = m
    }
    s.muUsers.Unlock()
    m.Lock()
    return m.Unlock
}

// Usage:
func (s *Service) AddToQueue(ctx context.Context, userID uuid.UUID, songID uuid.UUID, pos int) error {
    defer s.lockFor(userID)()
    // …DB tx…
}
```

The pattern — "acquire per-user mutex for any queue-mutating op" — is documented in `CLAUDE.md` so future edits don't skip it. On `UserDeletedEvent`, the entry in `userLocks` is removed (trivial leak protection).

### Schema (migrations/0001_init.up.sql) — one linear stream

Table creation order matters because of FK references; the single migration ordering is: `auth_users` → `queue_songs` → everything that FKs to either.

```sql
-- Outbox
CREATE TABLE outbox (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT    NOT NULL,
    payload    BLOB    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_outbox_created ON outbox(created_at);

-- Auth
CREATE TABLE auth_users (
    id             TEXT    PRIMARY KEY,                    -- UUID as text
    username       TEXT    NOT NULL UNIQUE,
    password       TEXT    NOT NULL,                        -- bcrypt
    email          TEXT,
    token_version  INTEGER NOT NULL DEFAULT 0,              -- JWT revocation (§6)
    created_at     INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at     INTEGER
);
CREATE INDEX idx_auth_users_username ON auth_users(username);

-- Songs (FK target for both queue_* and playlist_songs)
CREATE TABLE queue_songs (
    id        TEXT    PRIMARY KEY,
    title     TEXT,
    artist    TEXT,
    album     TEXT,
    genre     TEXT,
    duration  INTEGER,                                       -- seconds
    url       TEXT                                           -- path relative to MUSIC_STORAGE_PATH
);

-- Queue
CREATE TABLE queue_entries (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    song_id    TEXT    NOT NULL REFERENCES queue_songs(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (user_id, song_id)
);
CREATE INDEX idx_queue_entries_user_position ON queue_entries(user_id, position);

CREATE TABLE queue_user_songs (
    user_id           TEXT    NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    song_id           TEXT    NOT NULL REFERENCES queue_songs(id) ON DELETE CASCADE,
    listen_count      INTEGER NOT NULL DEFAULT 0,
    first_listened_at INTEGER,
    last_listened_at  INTEGER,
    liked             INTEGER NOT NULL DEFAULT 0,            -- SQLite boolean
    skipped           INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, song_id)
);

-- Playlists
CREATE TABLE playlist_playlists (
    id               TEXT    PRIMARY KEY,
    user_id          TEXT    NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    name             TEXT    NOT NULL,
    description      TEXT,
    is_system_liked  INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at       INTEGER
);
CREATE INDEX idx_playlists_user ON playlist_playlists(user_id);

-- Partial unique index: at most one system-liked playlist per user.
-- (v1 had an incorrect composite UNIQUE; replaced with this partial index.)
CREATE UNIQUE INDEX uniq_system_liked_per_user
    ON playlist_playlists(user_id) WHERE is_system_liked = 1;

CREATE TABLE playlist_songs (
    playlist_id TEXT    NOT NULL REFERENCES playlist_playlists(id) ON DELETE CASCADE,
    song_id     TEXT    NOT NULL REFERENCES queue_songs(id)        ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    added_at    INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (playlist_id, song_id)
);
CREATE INDEX idx_playlist_songs_position ON playlist_songs(playlist_id, position);
```

Everything is one file, one linear migration chain (`0001`, then future `0002_*.up.sql`, etc.). No per-module migration directories.

### User deletion, cascade, and UserDeleted event

v1 had per-module "local user copies" (e.g. `users_playlist`). v2 removes them: **no module stores a local copy of a user**. Every `user_id` column is an FK to `auth_users.id` with `ON DELETE CASCADE`. One `DELETE FROM auth_users WHERE id = ?` cascades to everything atomically.

```go
// auth.service.DeleteUser (shape)
tx, _ := s.db.BeginTx(ctx, nil)
_, _ = tx.ExecContext(ctx, `INSERT INTO outbox (event_type, payload) VALUES ('UserDeleted', ?)`, marshal(evt))
_, _ = tx.ExecContext(ctx, `DELETE FROM auth_users WHERE id = ?`, userID)
_ = tx.Commit()
```

The `UserDeletedEvent` is still published (via outbox) for **cache invalidation only** — e.g. the per-user mutex map in §5. Subscribers don't delete rows; the FK cascade already did.

### Boolean as INTEGER

SQLite has no BOOL type; `0`/`1` INTEGERs are the idiom. Go structs use `bool`; `database/sql` drivers (including `modernc.org/sqlite`) handle the conversion.

---

## 6. HTTP layer

### Routing: Go 1.22 method patterns, explicit wrapping

```go
// cmd/muzika/main.go (sketch)
mux := http.NewServeMux()

// Public — NO auth middleware
mux.HandleFunc("POST /api/auth/user",   authH.Register)   // register
mux.HandleFunc("POST /api/auth/login",  authH.Login)
mux.HandleFunc("GET  /health",          healthHandler)

// Protected — each explicitly wrapped with WithAuth
withAuth := httpx.WithAuth(authVerifier)
mux.Handle("DELETE /api/auth/user/{id}",         withAuth(authH.Delete))
mux.Handle("POST /api/auth/logout-all",          withAuth(authH.LogoutAll))
mux.Handle("GET  /api/playlist/",                withAuth(plH.List))
mux.Handle("POST /api/playlist/",                withAuth(plH.Create))
mux.Handle("GET  /api/playlist/{id}",            withAuth(plH.Get))
mux.Handle("DELETE /api/playlist/{id}",          withAuth(plH.Delete))
mux.Handle("POST /api/playlist/{id}/song/{songId}",   withAuth(plH.AddSong))
mux.Handle("DELETE /api/playlist/{id}/song/{songId}", withAuth(plH.RemoveSong))
// …etc for /api/queue/*

// Static SPA
mux.Handle("GET /", web.SPAHandler())
```

Benefits:
- Method is part of the route pattern — `POST /api/auth/login` and `DELETE /api/auth/user/{id}` are distinct entries. No per-handler method-switching code.
- Public vs. protected is visible at registration site. **No handler wraps its own auth.**
- Path params (`{id}`) extracted via `r.PathValue("id")`.

### JWT: `tv` claim and revocation

JWT payload:
```json
{
  "sub": "<uuid>",
  "tv":  3,
  "iat": 1713571200,
  "exp": 1713657600
}
```

On every protected request, `WithAuth`:
1. Parses + verifies signature.
2. Reads `tv` claim + `sub`.
3. `SELECT token_version FROM auth_users WHERE id = ?` and compares. Mismatch → 401.
4. On success, puts `userID uuid.UUID` in `r.Context()` via `httpx.WithUserID(ctx, id)`.

`POST /api/auth/logout-all` runs `UPDATE auth_users SET token_version = token_version + 1 WHERE id = ?`. All outstanding tokens for that user become invalid on the next request.

**Cost:** one extra SELECT per authenticated request. On SQLite with an indexed PK lookup on one-user workload, this is sub-millisecond. Worth it for revocation.

**Future:** if cost ever matters, cache `token_version` in memory with a TTL and invalidate on logout-all. Not doing it in v1.

### Handler testability convention (CLAUDE.md entry)

**Pattern chosen: context-based userID.** The full convention, to be documented verbatim in CLAUDE.md:

> Handlers under `/api/*` that require authentication extract the user ID from `r.Context()` via `httpx.GetUserID(ctx)`. Middleware `httpx.WithAuth` is responsible for populating it — no handler validates JWTs directly. Services receive `userID uuid.UUID` as the first business argument, never a `*http.Request` or `context.Context`-only (context is passed separately for cancellation). This separation makes services testable without synthesizing requests, and makes the auth dependency explicit at the HTTP layer.

Applied uniformly across `auth`, `playlist`, `queue` handlers.

### CORS (configurable, no wildcards)

```go
// httpx/cors.go
type CORSConfig struct {
    Origins []string        // parsed from MUZIKA_CORS_ORIGINS (comma-separated)
}

// Middleware emits headers ONLY when len(Origins) > 0 and the request's Origin matches one.
// Never emits Access-Control-Allow-Origin: *.
// If MUZIKA_CORS_ORIGINS is empty, no CORS headers are emitted (same-origin assumed).
```

In production (Go binary serves SPA from same origin), CORS is unnecessary and left empty. In dev (Vite on 3000, Go on 8080), `MUZIKA_CORS_ORIGINS=http://localhost:3000`. In no case is `*` used — an attack surface the old Spring services all had.

### MIME + range requests for audio

```go
// queue/handler.go GET /api/queue/songs/{id}
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
    path := resolvePath(...)
    f, _ := os.Open(path)
    defer f.Close()

    ct := mime.TypeByExtension(filepath.Ext(path))
    if ct == "" { ct = "application/octet-stream" }
    w.Header().Set("Content-Type", ct)

    st, _ := f.Stat()
    http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
    // ServeContent honors Range requests for free — scrubbing in the player works.
}
```

Replaces the old hardcoded `audio/mpeg`. Works for mp3, flac, ogg, m4a uniformly.

---

## 7. Soulseek backend (gosk, in-process)

Since v0.3.0 there is one backend: gosk, a pure-Go Soulseek client at
[github.com/ABCslime/gosk](https://github.com/ABCslime/gosk), imported
as a library and run in-process. No sidecar, no HTTP hop, no second
systemd unit. The `MUZIKA_SOULSEEK_BACKEND` switch is gone.

### Interface

```go
// internal/soulseek/client.go
package soulseek

type SearchResult struct {
    Peer        string
    Filename    string
    Size        int64
    Bitrate     int
    QueueLen    int
    FilesShared int
}

type DownloadHandle struct {
    ID string       // gosk-opaque
}

type DownloadState struct {
    State    string       // "queued" | "transferring" | "completed" | "failed"
    Bytes    int64
    Size     int64
    FilePath string       // populated when State == "completed"
}

type Client interface {
    Search(ctx context.Context, query string, window time.Duration) ([]SearchResult, error)
    Download(ctx context.Context, peer, filename string, size int64) (DownloadHandle, error)
    DownloadStatus(ctx context.Context, h DownloadHandle) (DownloadState, error)
}
```

Construction (in `cmd/muzika/main.go`):

```go
sk, err := soulseek.NewNativeClient(nativeGoskConfig(cfg))
if err != nil {
    return fmt.Errorf("init gosk: %w", err)
}
```

`NewNativeClient` wraps `gosk.New(...)`, supplying credentials and the
state path from `Config` (`MUZIKA_SOULSEEK_USERNAME`, `_PASSWORD`,
`MUZIKA_SOULSEEK_SERVER_ADDRESS`, `_PORT`, `MUZIKA_SOULSEEK_LISTEN_PORT`,
`MUZIKA_GOSK_STATE_PATH`).

### gosk scope

- Login + persistent session (reconnect on drop, exponential backoff).
- Search with N-second response window.
- Peer selection filter by file count and queue length.
- Single-file download with resume on reconnect.
- In-flight download state persistence in SQLite at
  `MUZIKA_GOSK_STATE_PATH` (default `/data/gosk-state.db`).

Explicit non-goals (same as always): uploads, chat, room search,
distributed peer routing, wishlists, web UI, swarm download. If muzika
needs any of these, add them to gosk — not to muzika.

### History (pre-v0.3.0)

Originally v2 shipped with two implementations behind `soulseek.Client`:
- **slskd** — HTTP client against the external slskd daemon, running as
  its own `slskd.service`. This was the default through v0.2.x.
- **native** — early gosk, flagged as experimental.

The plan in the v2 design doc was to port every module against slskd
first, then swap to gosk once it met a 40–80 MB RAM target. That's what
happened: Phases 4–8 ported modules against slskd; Phase 8 stabilized
gosk; v0.3.0 deleted the slskd implementation, the backend switch, and
the `slskd.service` unit. The `internal/slskd` package was renamed to
`internal/download` to match its new role — driving an in-process Go
client, not an external daemon.

The rename is intentional: "slskd" referred to a specific external
binary that no longer exists in the deployment. Keeping the name would
misdescribe the code.

---

## 8. Systemd layout

Bare-metal systemd, not Docker. Three units installed to `/etc/systemd/system/` by `deploy/install.sh` (pre-v0.3.0 there were four — `slskd.service` was the fourth):

| Unit                          | Type      | User    | Memory cap | Started by            |
|-------------------------------|-----------|---------|------------|-----------------------|
| `muzika.service`              | simple    | muzika  | 150 MB     | the updater, after first binary install |
| `muzika-updater.service`      | oneshot   | root    | —          | the timer              |
| `muzika-updater.timer`        | —         | —       | —          | operator (`systemctl enable --now muzika-updater.timer` once) |

Sources live under [`deploy/systemd/`](./deploy/systemd/). `install.sh` copies them to `/etc/systemd/system/` and runs `systemctl daemon-reload`.

### Memory caps (cgroup v2)

- `muzika.service`: `MemoryMax=150M`, `MemoryHigh=130M`, plus `Environment=GOMEMLIMIT=120MiB GOGC=50` so the Go runtime starts collecting before hitting the cgroup ceiling.

`MemoryMax=` is a hard cap — the process is OOM-killed past it. `MemoryHigh=` is a soft throttle — the process is heavily reclaimed but not killed. Requires systemd ≥ 247; Raspberry Pi OS Bookworm ships 252, every current distro is fine.

### Sandboxing (muzika.service)

- `User=muzika`, `Group=muzika` (UID 1001, created by `install.sh`).
- `ProtectSystem=strict` — entire rootfs read-only except explicitly listed paths.
- `ProtectHome=true`, `PrivateTmp=true`, `NoNewPrivileges=true`.
- `ReadWritePaths=/srv/muzika/data` — audio files, the SQLite DB, and the
  gosk state DB all live under this one directory.
- `StandardOutput=journal`, `StandardError=journal` → `journalctl -u muzika`.

### Restart policy

- muzika: `Restart=on-failure`, `RestartSec=5`, `StartLimitBurst=5`, `StartLimitIntervalSec=60`. On a persistently-broken binary we stop retrying after 5 attempts in 60 s — no tight-loop restart storm eating CPU on a 1 GB Pi.
- updater: no restart. Timer retries every 5 min on its own.

### Autoupdate flow

1. `muzika-updater.timer` fires every 5 min (`OnBootSec=2min`, `OnUnitActiveSec=5min`, `Persistent=true`). `Persistent=true` replays missed ticks across reboots.
2. Timer starts `muzika-updater.service` (oneshot, `User=root`).
3. [`/usr/local/bin/muzika-update`](./deploy/bin/muzika-update) does, in order:
   1. `git fetch && git reset --hard origin/main` in `/srv/muzika/repo`.
   2. Run [`/usr/local/bin/muzika-decrypt`](./deploy/bin/muzika-decrypt): SOPS-decrypt `deploy/.env.sops` into `/etc/muzika/muzika.env`. Compare sha256 before/after to set `env_changed`. (Pre-v0.3.0 this split by prefix into two files — `muzika.env` and `slskd.env` — because the slskd sidecar needed its own env. With slskd gone there's a single consumer, so a single file.)
   3. `curl` GitHub Releases API for `<repo>/releases/latest`. On HTTP 403 (rate-limited) or 5xx, log and exit 0 — the timer retries next tick.
   4. If the returned `tag_name` doesn't start with `v`, exit 1 (guard against upstream mistakes).
   5. Read `/var/lib/muzika/version`. If binary tag matches and `env_changed=0`, exit 0.
   6. Otherwise `curl` the `muzika-linux-arm64` binary and its `.sha256` companion. Verify checksum; mismatch → exit 1.
   7. `chmod 0755` the tempfile, `mv` to `/usr/local/bin/muzika` (rename is atomic within the same filesystem). Write the tag to `/var/lib/muzika/version`.
   8. `systemctl restart muzika`. The Go shutdown path has 30 s (`TimeoutStopSec=30`) to drain HTTP + bus + outbox.

### Why not Docker

- Docker daemon: ~130 MB resident.
- Watchtower: ~30 MB.
- Both subtract from a fixed 1024 MB budget. systemd is already PID 1 and costs nothing extra.
- See §1 for the numbers, §10 for the "don't bring it back" rule.

### gosk config

There is no separate gosk config file. gosk is a Go library linked into
muzika; every knob comes from `MUZIKA_*` env vars parsed into
`internal/config.Config` (see §10) and passed to `soulseek.NewNativeClient`
at startup. The pre-v0.3.0 `/etc/muzika/slskd.yml` file and its
`deploy/slskd.yml.template` source are gone.

---

## 9. Refiller

On-demand only. No background ticker. Matches old QueueManager behavior:
- Every HTTP handler that removes from the queue (`/queue/skipped`, `/queue/finished`) calls `refiller.Trigger(userID)` fire-and-forget.
- `GET /api/queue` also calls `refiller.Trigger(userID)` on its way out.
- `refiller.Trigger` computes how short the queue is vs. `MIN_QUEUE_SIZE` and publishes that many `RequestRandomSongEvent`s.

No ticker goroutine — if the user isn't interacting, the queue sits. Same as before. Future: if we ever want background refill, it's a single ticker goroutine added to `queue.service`. Out of scope for v2.

---

## 10. Configuration

```go
type Config struct {
    HTTPPort             int           `envconfig:"HTTP_PORT" default:"8080"`
    DBPath               string        `envconfig:"DB_PATH" default:"/data/muzika.db"`
    JWTSecret            string        `envconfig:"JWT_SECRET" required:"true"`
    JWTExpiration        time.Duration `envconfig:"JWT_EXPIRATION" default:"24h"`
    MusicStoragePath     string        `envconfig:"MUSIC_STORAGE_PATH" default:"/data/music"`
    CORSOrigins          []string      `envconfig:"CORS_ORIGINS"`                          // comma-separated, empty = off

    // Soulseek network account (gosk login)
    SoulseekUsername       string      `envconfig:"SOULSEEK_USERNAME" required:"true"`
    SoulseekPassword       string      `envconfig:"SOULSEEK_PASSWORD" required:"true"`
    SoulseekServerAddress  string      `envconfig:"SOULSEEK_SERVER_ADDRESS" default:"server.slsknet.org"`
    SoulseekServerPort     int         `envconfig:"SOULSEEK_SERVER_PORT" default:"2242"`
    SoulseekListenPort     int         `envconfig:"SOULSEEK_LISTEN_PORT" default:"2234"`
    GoskStatePath          string      `envconfig:"GOSK_STATE_PATH" default:"/data/gosk-state.db"`

    MinQueueSize         int           `envconfig:"MIN_QUEUE_SIZE" default:"10"`
    BandcampWorkers      int           `envconfig:"BANDCAMP_WORKERS" default:"2"`
    DownloadWorkers      int           `envconfig:"DOWNLOAD_WORKERS" default:"2"`
    BandcampDefaultTags  []string      `envconfig:"BANDCAMP_DEFAULT_TAGS" default:"electronic,house"`
    BusBufferSize        int           `envconfig:"BUS_BUFFER_SIZE" default:"64"`
    LogLevel             string        `envconfig:"LOG_LEVEL" default:"info"`
}
```

Prefix `MUZIKA_`. `SoulseekUsername` / `SoulseekPassword` are `required:"true"` — muzika won't start without a Soulseek account. The v0.2.x struct had a `SoulseekBackend` switch and four `Slskd*` fields; all five were dropped in v0.3.0 along with the sidecar. `SlskdWorkers` became `DownloadWorkers`.

---

## 11. GitHub Actions: two workflows

### `.github/workflows/build.yml` — every push to `main`

```yaml
name: build
on:
  push: { branches: [main] }
  pull_request: { branches: [main] }

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - run: go vet ./...
      - run: go test ./...
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: npm
          cache-dependency-path: frontend/package-lock.json
      - run: cd frontend && npm ci && npm run build -- --mode=production

  binary:
    needs: test
    if: github.event_name == 'push'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22', cache: true }
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: npm
          cache-dependency-path: frontend/package-lock.json
      - name: Build muzika binary
        env:
          GOOS: linux
          GOARCH: arm64
          CGO_ENABLED: 0
        run: |
          cd frontend && npm ci && npm run build && cd ..
          mkdir -p internal/web/dist
          cp -r frontend/dist/* internal/web/dist/
          go build -trimpath -ldflags="-s -w" -o muzika-linux-arm64 ./cmd/muzika
          sha256sum muzika-linux-arm64 > muzika-linux-arm64.sha256
      - uses: actions/upload-artifact@v4
        with:
          name: muzika-binary
          path: |
            muzika-linux-arm64
            muzika-linux-arm64.sha256
```

Every push to `main` → a cross-compiled `linux/arm64` binary uploaded as a workflow artifact. **No container registry. No GHCR login. No ghcr permissions.** Nothing deploys.

### `.github/workflows/release.yml` — every `v*` git tag

Triggered by pushing a `v*` tag. Rebuilds the binary (same cross-compile as `build.yml`) and creates a GitHub Release via [`softprops/action-gh-release@v2`](https://github.com/softprops/action-gh-release).

```yaml
name: release
on:
  push:
    tags: ['v*']

jobs:
  build-and-release:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22', cache: true }
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: npm
          cache-dependency-path: frontend/package-lock.json
      - name: Build muzika binary
        env:
          GOOS: linux
          GOARCH: arm64
          CGO_ENABLED: 0
        run: |
          cd frontend && npm ci && npm run build && cd ..
          mkdir -p internal/web/dist
          cp -r frontend/dist/* internal/web/dist/
          go build -trimpath -ldflags="-s -w" -o muzika-linux-arm64 ./cmd/muzika
          sha256sum muzika-linux-arm64 > muzika-linux-arm64.sha256
      - uses: softprops/action-gh-release@v2
        with:
          tag_name: ${{ github.ref_name }}
          files: |
            muzika-linux-arm64
            muzika-linux-arm64.sha256
          generate_release_notes: true
```

Rebuilding on tag (instead of cross-workflow `actions/download-artifact`) keeps this workflow self-contained and adds ~2 min of CI time — fine.

The Pi's `muzika-updater.timer` watches `/releases/latest` and pulls the new binary on the next tick (≤5 min lag).

**Release cadence = tag cadence.** Approval gate is "did you push the tag." No `:latest` churn on every commit.

**No ghcr.io, no packages: write.** Only `contents: write` on the release workflow (needed to create the Release). No self-hosted runner. GitHub-hosted ubuntu-latest does everything.

---

## 12. Secrets, volumes, pre-commit

### SOPS-encrypted `.env` in git

`deploy/.env.sops` is committed to the repo, encrypted with [SOPS](https://github.com/getsops/sops) using an [age](https://github.com/FiloSottile/age) recipient. The age **private key** lives only on the Pi at `/etc/muzika/age.key` (mode 0400, root-owned). The age public key is committed in `deploy/.sops.yaml`.

```yaml
# deploy/.sops.yaml
creation_rules:
  - path_regex: \.env\.sops$
    age: age1abc...xyz      # public key, safe to commit
```

### Decrypt flow on the Pi

On every timer tick, `/usr/local/bin/muzika-update` calls `/usr/local/bin/muzika-decrypt`, which:

1. Reads `/srv/muzika/repo/deploy/.env.sops`.
2. Runs `sops -d` with `SOPS_AGE_KEY_FILE=/etc/muzika/age.key`.
3. Writes the decrypted output to `/etc/muzika/muzika.env` (mode 0640, owner `muzika:muzika`).

The file is consumed by systemd via `EnvironmentFile=` in `muzika.service`. There is no `docker compose --env-file` pipeline, no env-var templating — systemd loads the file as plain `KEY=value` pairs.

Pre-v0.3.0 this step split the decrypted output into two files by variable prefix — `MUZIKA_*` into `muzika.env`, `SLSKD_*`/`SOULSEEK_*` into `slskd.env` — because the slskd sidecar needed its own env. With slskd retired, everything that remains is a `MUZIKA_*` var consumed by the one binary.

### Rotation

1. Generate a new age keypair on the Pi: `sudo age-keygen -o /tmp/new.key`.
2. Update `deploy/.sops.yaml` with the new recipient.
3. `sops updatekeys deploy/.env.sops` (re-encrypts with new recipient).
4. Commit + push.
5. On the Pi, replace `/etc/muzika/age.key` with the new private key (0400 root).
6. Next timer tick picks up the re-encrypted file.

### Pre-commit hook

[`.githooks/pre-commit`](./.githooks/pre-commit) blocks staging of any `.env*` file except `.env.example` and `deploy/.env.sops`. Enable per clone with `git config core.hooksPath .githooks`.

### UID pinning on the Pi

`install.sh` creates a `muzika` system user at UID 1001. `/srv/muzika/data` (audio files + SQLite DB + gosk state) is owned `muzika:muzika` (0750) and `muzika.service` runs as `User=muzika`. Since v0.3.0 there's no second service sharing the music directory — gosk runs inside muzika — so the shared-UID dance that mattered for slskd is moot. The Docker-era PUID/PGID env vars were retired in Phase 3.5.

---

## 13. Startup / shutdown (main.go)

```go
func main() {
    cfg := config.Load()
    log := slog.New(...)
    runtime.SetGCPercent(50)                // match GOGC env default

    db := db.Open(cfg.DBPath)
    migrate.Run(db, "migrations")           // one linear stream

    b := bus.New(cfg.BusBufferSize, log)

    authSvc := auth.NewService(db, cfg.JWTSecret, cfg.JWTExpiration, b)
    plSvc   := playlist.NewService(db, b)
    qSvc    := queue.NewService(db, cfg.MusicStoragePath, cfg.MinQueueSize, b)
    bcSvc   := bandcamp.NewService(cfg.BandcampDefaultTags, b)

    sk, err := soulseek.NewNativeClient(nativeGoskConfig(cfg))  // gosk, in-process
    if err != nil { log.Error("init gosk: " + err.Error()); os.Exit(1) }
    dlSvc := download.NewService(sk, cfg.MusicStoragePath, b)

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    // Subscribers + worker pools
    plSvc.StartWorkers(ctx)
    qSvc.StartWorkers(ctx)
    bcSvc.StartWorkers(ctx, cfg.BandcampWorkers)
    dlSvc.StartWorkers(ctx, cfg.DownloadWorkers)

    // Outbox dispatcher
    outbox := bus.StartOutboxDispatcher(ctx, db, b, log)

    mux := http.NewServeMux()
    mountRoutes(mux, authSvc, plSvc, qSvc)
    srv := &http.Server{Addr: ":" + strconv.Itoa(cfg.HTTPPort), Handler: mux}
    go srv.ListenAndServe()

    <-ctx.Done()

    // Graceful shutdown: stop accepting, drain HTTP, stop outbox, wait workers.
    shutdownCtx, c := context.WithTimeout(context.Background(), 30*time.Second)
    defer c()
    srv.Shutdown(shutdownCtx)
    outbox.Stop()
    b.Close()
    b.Wait()
    db.Close()
}
```

---

## 14. System diagram

```mermaid
flowchart LR
    subgraph Browser["Web Browser"]
        SPA[Vue SPA<br/>localStorage JWT<br/>tv claim validated]
    end

    subgraph Pi["Raspberry Pi — systemd"]
        subgraph MuzikaSvc["muzika.service (User=muzika, MemoryMax=150M)"]
            HTTP["net/http mux<br/>Go 1.22 method patterns"]
            Auth["internal/auth<br/>token_version"]
            Playlist["internal/playlist"]
            Queue["internal/queue<br/>per-user mutex<br/>refiller (on-demand)"]
            Bandcamp["internal/bandcamp<br/>workers: N"]
            Download["internal/download<br/>workers: N"]
            SoulseekIface(["internal/soulseek.Client"])
            GoskImpl["gosk<br/>(github.com/ABCslime/gosk)<br/>in-process"]

            Bus[("internal/bus<br/>per-subscriber channels")]
            Outbox[("outbox table<br/>+ dispatcher")]
            SQLite[(SQLite<br/>/srv/muzika/data/muzika.db)]
            GoskDB[(gosk state<br/>/srv/muzika/data/gosk-state.db)]

            HTTP --> Auth & Playlist & Queue
            Auth -- "INSERT outbox + DELETE" --> SQLite
            Queue -- "INSERT outbox + state" --> SQLite
            Download -- "INSERT outbox (LoadedSong)" --> SQLite

            Outbox -- polls --> SQLite
            Outbox -- "Publish state-change" --> Bus

            Queue -- "Publish RequestRandomSong" --> Bus
            Bandcamp -- "Publish RequestDownload" --> Bus

            Bus -- per-subscriber chan --> Playlist
            Bus -- per-subscriber chan --> Queue
            Bus -- per-subscriber chan --> Bandcamp
            Bus -- per-subscriber chan --> Download

            Download --> SoulseekIface
            SoulseekIface --> GoskImpl
            GoskImpl --- GoskDB
        end

        subgraph UpdaterSvc["muzika-updater.timer + .service<br/>(oneshot, User=root, fires every 5 min)"]
            Updater["/usr/local/bin/muzika-update<br/>+ muzika-decrypt"]
        end

        subgraph Disk["Pi filesystem (/srv/muzika — owned muzika:muzika)"]
            DBFile["data/muzika.db"]
            GoskFile["data/gosk-state.db"]
            Audio["data/music/"]
            Repo["repo/ (git checkout)"]
        end

        subgraph Etc["/etc/muzika (0750 root:muzika)"]
            AgeKey["age.key (0400 root)"]
            MuzEnv["muzika.env (0640)"]
        end

        SQLite --- DBFile
        GoskDB --- GoskFile
        GoskImpl -.writes.-> Audio
        Queue -.reads (ServeContent).-> Audio
        MuzikaSvc -.EnvironmentFile.-> MuzEnv
    end

    SPA -->|HTTPS /api/*, Bearer w/ tv| HTTP

    subgraph External["Internet"]
        BC[bandcamp.com]
        SN[Soulseek P2P]
        GHREL[GitHub Releases<br/>muzika-linux-arm64<br/>muzika-linux-armv7]
        GHGIT[GitHub repo]
    end

    Bandcamp -.goquery scrape.-> BC
    GoskImpl -.P2P wire protocol.-> SN
    Updater -.curl + verify sha.-> GHREL
    Updater -.git pull.-> GHGIT
    Updater -.decrypts .env.sops with age.key.-> Etc
    Updater -.atomic mv + systemctl restart.-> MuzikaSvc
```

---

## 15. Testing strategy

1. **Unit** — pure logic, fake bus + in-memory SQLite (`:memory:` via modernc driver, same PRAGMAs).
2. **Repo integration** — real SQLite file in `t.TempDir()`.
3. **End-to-end smoke** — `go test -tags=e2e` spins the full binary against a `:memory:` DB and a fake `soulseek.Client`; asserts login → add song → fetch queue.
4. **Outbox correctness** — a dedicated test kills the dispatcher mid-run and restarts to confirm events are still delivered.

No test hits the real Soulseek network. Tests use a fake `soulseek.Client` and run the full download worker against it. Manual smoke on the Pi post-deploy is enough for a personal deploy.

---

## 16. Open questions (carried forward)

All Phase 2-v1 items resolved. Remaining small uncertainties for Phase 3:

1. **Frontend dev workflow** — locked: when `internal/web/dist` is empty (`embed.FS` yields zero files), muzika serves nothing at `/` and returns 404. In dev, run `vite dev` on :3000; the Vite proxy forwards `/api/*` to `localhost:8080` as it does today. In prod, the CI binary build (`build.yml`) runs `npm run build` and copies `frontend/dist/` into `internal/web/dist/` before `go build`, so `//go:embed` bakes the SPA into the released binary. README will document both.
2. **Audio extensions** — if a filename has no extension (some peers serve one), `mime.TypeByExtension` yields `""` and we fall back to `application/octet-stream`. The browser's `<audio>` element may reject unknown types. If it becomes a real problem, we sniff the first 512 bytes (`http.DetectContentType`). Not doing it in v2 to keep things lean.
3. **gosk module path** — resolved: [`github.com/ABCslime/gosk`](https://github.com/ABCslime/gosk). Independent repo, independent lifecycle, imported as a normal Go dep. v0.3.0 consumes gosk v0.1.0.

---

**End of v2.** Stop here; no scaffolding until you approve.
