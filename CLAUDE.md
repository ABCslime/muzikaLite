# CLAUDE.md — conventions for working in this repo

Authoritative conventions for future edits. Read `ARCHITECTURE.md` first for the
design rationale; this file is the short rulebook.

---

## 1. Project shape

- Single Go binary under `cmd/muzika`. Five internal domain packages
  (`auth`, `playlist`, `queue`, `bandcamp`, `slskd`) that never import each
  other. They communicate via `internal/bus`.
- Replaces five Spring Boot services (authManager, PlaylistManager,
  QueueManager, BandcampApi, SlskdDownloader) and embeds the Vue SPA.
- Target: Raspberry Pi 3, 1 GB RAM. Memory budgets are load-bearing.
- Deployment: bare-metal systemd, not Docker. See §11.

Memory budget (load-bearing):

| Process                    | MemoryMax | Notes                           |
|----------------------------|-----------|---------------------------------|
| `muzika.service`           | 150 MB    | GOMEMLIMIT=120MiB, GOGC=50      |
| `slskd.service`            | 400 MB    | Heavy hitter                    |
| `muzika-updater.service`   | —         | Oneshot, ~5 s every 5 min       |
| **Total steady-state**     | **~550 MB** | Leaves ~470 MB for kernel/SSH |

Down from ~740 MB under Docker+Watchtower (the Docker daemon alone cost
~130 MB). The RAM savings is the reason we migrated off Docker. See
`ARCHITECTURE.md` §1 for the detailed budget and §3 for the full layout.

---

## 2. Dependency direction

```
cmd/muzika  ─►  all internal/*
internal/{auth,playlist,queue,bandcamp,slskd}
      ├─► internal/bus
      ├─► internal/db
      ├─► internal/httpx
      └─► internal/config
internal/slskd  ─►  internal/soulseek   (only — stays thin)
```

**No domain package imports another domain package.** If you feel tempted,
publish an event instead. This keeps the old service boundaries verifiable
as we port module-by-module.

---

## 3. HTTP and auth

### Routing
- Use Go 1.22 method patterns: `mux.HandleFunc("POST /api/auth/login", ...)`.
- Public routes are registered with `HandleFunc` directly.
- Protected routes are wrapped once at mount time with `httpx.WithAuth(...)`.
  **No handler validates its own JWT.** If you see `Authorization` being read
  outside `httpx/middleware.go`, that's a bug.

### userID flow (pick ONE pattern and never mix)
Chosen pattern: **context-based**.

- `httpx.WithAuth` validates the JWT, looks up the current `token_version`,
  and calls `httpx.WithUserID(ctx, id)`.
- Handlers extract with `id, ok := httpx.GetUserID(r.Context())`.
- **Services accept `userID uuid.UUID` as their first business argument** —
  they never reach back into `*http.Request` or pull the ID from context
  themselves. That separation keeps services testable without synthesizing
  requests.

Apply this uniformly across `auth`, `playlist`, `queue` handlers. Don't
invent a second pattern "just for this one case."

### JWT revocation
- Every JWT carries a `tv` (token_version) claim.
- `auth_users.token_version` starts at 0. `POST /api/auth/logout-all`
  increments it. Every `WithAuth` call does a fast `SELECT token_version`
  and rejects mismatches as 401.
- If performance ever becomes a problem (it won't on this scale), cache
  `(userID, tv)` in-process; invalidate on logout-all.

### CORS
- Configured via `MUZIKA_CORS_ORIGINS` (comma-separated exact origins).
- Empty means no CORS headers emitted.
- **Never `*`.** The helper silently drops wildcards if passed.

### Audio serving
- Use `http.ServeContent` so Range requests work (the Vue player scrubs).
- Content-Type via `mime.TypeByExtension(filepath.Ext(path))`, fallback
  `application/octet-stream`. Don't hardcode `audio/mpeg` — slskd returns
  flac/ogg/m4a too.

---

## 4. SQLite

### Pragmas (set in `internal/db/db.go`, don't duplicate elsewhere)
```
PRAGMA journal_mode = WAL
PRAGMA foreign_keys = ON
PRAGMA busy_timeout = 5000
PRAGMA synchronous = NORMAL
PRAGMA cache_size = -8000
PRAGMA temp_store = MEMORY
```

### Connection pool — read this carefully
We use `SetMaxOpenConns(1)` — a single serialized connection. This is
correct for our scale on a Pi 3 and keeps lock semantics simple. If HTTP
request latency ever shows queuing behind DB work (observable via the
`dur_ms` field on `slog` request logs), split into a separate read pool
(`SetMaxOpenConns(N)` on a read-only `*sql.DB` opened with `?mode=ro`) and
a dedicated single-writer connection. The read connections share the WAL
with the writer; no correctness issue, just throughput.

Do not flip `SetMaxOpenConns(>1)` on the current writer — it will introduce
`SQLITE_BUSY` errors that the per-user mutex below cannot prevent.

### Per-user queue mutex
Queue position reordering is the only workload with real logical
concurrency. `queue.Service` holds a map of `sync.Mutex` keyed by userID.
**Any function that mutates `queue_entries` for a given userID MUST defer
`s.lockFor(userID)()` before touching the DB.** Miss this and you'll get
position collisions and `UNIQUE` violations under concurrent requests.

The mutex is not nested with other per-user locks; acquire once, do the
work, release. If a future op needs to touch two users' queues atomically
(no current need), lock in a deterministic order to avoid deadlock.

### Schemas as prefixes
SQLite has no schemas. Tables are named `auth_*`, `playlist_*`, `queue_*`,
plus a single unprefixed `outbox`. Don't invent new prefixes — if a module
needs a new table, it goes under its existing prefix.

### Migrations
One linear stream under `migrations/`. Next migration is `0002_*.up.sql`.
Never edit a shipped migration; always add a new one.

---

## 5. Bus and outbox

### Delivery guarantees
- **State-change events** (`UserCreated`, `UserDeleted`, `LikedSong`,
  `UnlikedSong`, `LoadedSong`) go through the outbox. Writers insert
  the row in the **same transaction** as the state mutation, then call
  `dispatcher.Wake()` after commit. This is the at-least-once contract;
  subscribers MUST be idempotent.
- **Request events** (`RequestRandomSong`, `RequestSlskdSong`) are
  published directly with `bus.Publish`. If lost on process crash, the
  refiller regenerates them on the next short-queue observation.

### When you add a new event type
1. Add the struct in `internal/bus/events.go`.
2. If state-change: add the `TypeXxx` constant and extend
   `OutboxDispatcher.dispatchByType`'s switch.
3. If request: publish directly with `PublishOpts{SendTimeout: 100ms}`.
4. Every subscriber calls `bus.Subscribe[T](b, "module/topic")` in its
   own package; the bus gives each subscriber a fresh channel.

### Fan-out
`Subscribe[T]` returns a fresh channel per call. `Publish[T]` iterates all
subscribers. Two consumers on the same event type each see every event —
don't assume single-consumer semantics.

### Dispatcher wake channel
The outbox dispatcher selects on `wake <-chan struct{}` (buffer 1) or a
500 ms ticker. Publishers do a non-blocking send after commit:

```go
select {
case wake <- struct{}{}:
default:
}
// never blocks: buffer 1 absorbs one pending wake even if dispatcher is busy
```

The ticker is the safety net for rows inserted by other paths (crash
recovery on boot). Don't remove it.

### Known race: LikedSong vs song deletion
`LikedSong` delivery can fail if the `queue_songs` row is deleted before
the event is processed (unusual skip-then-like race). The playlist
dispatcher's insert into `playlist_songs` hits the FK and returns an error;
with the Phase 4 poison-row handling, the outbox row is dropped. The like
is silently lost. Acceptable for v1 — if it becomes a real problem, either
soft-delete songs or capture the song's metadata at like-time and re-create
the row on demand.

---

## 6. Configuration

All runtime knobs are in `internal/config/Config`, prefix `MUZIKA_`.
`.env.example` is the canonical list. When adding a new knob:
1. Add the field to `Config`.
2. Add it to `.env.example` with a comment.
3. If required in one backend mode and not another, extend
   `Config.validate()` — don't `panic` at use-site.

---

## 7. Secrets and volumes

- Plaintext `.env` is **never** committed. The `.githooks/pre-commit` hook
  blocks it. Only `.env.example` (template) and `deploy/.env.sops`
  (encrypted) live in git.
- SOPS + age. Private key lives only on the Pi at `/etc/muzika/age.key`.
  Rotate per the README.
- The `muzika` system user on the Pi has UID 1000. Both `muzika.service`
  and `slskd.service` run as `User=muzika`. The music directory
  `/srv/muzika/data/music` is owned by `muzika:muzika` (mode 0750). Don't
  invent a second UID for slskd — it must match muzika so both services
  can read/write the shared directory without a permission dance.
- Decrypted env files live at `/etc/muzika/muzika.env` and
  `/etc/muzika/slskd.env`, both mode 0640 owned `muzika:muzika`. They are
  consumed via `EnvironmentFile=` directives in the systemd units —
  nothing else reads them.

---

## 8. Soulseek backend

- Production uses `MUZIKA_SOULSEEK_BACKEND=slskd`. Day-one.
- `native` (gosk) is a separate Go module, not in this repo. Setting
  `SOULSEEK_BACKEND=native` in v1 fails fast with a clear error.
- `internal/slskd` is **thin**: it consumes `RequestSlskdSong`, calls
  `soulseek.Client` methods, and publishes `LoadedSong` via outbox. It
  does not know which backend is in use.

See `ARCHITECTURE.md` §7 for the gosk evolution plan.

---

## 9. Testing

- Unit tests use an in-memory SQLite (`file::memory:?cache=shared`)
  opened with the same pragmas — don't skip them in tests.
- Per-module tests never import another domain package; they import
  `internal/bus` and pass a fake/real `*bus.Bus`.
- Integration tests spin up a real SQLite file in `t.TempDir()`.
- No test hits the real slskd or bandcamp.com; use the `soulseek.Client`
  interface and a fake.

---

## 10. What not to do

- Don't add a second HTTP router. Stdlib mux is enough.
- Don't add an ORM. `database/sql` + SQL strings is fine.
- Don't add a message broker. The bus is the message broker.
- Don't add a DI framework. `main.go` wiring is plenty.
- Don't add a config YAML parser. Env vars only.
- Don't bring back Kafka.
- Don't bring back Docker. We measured the Pi 3 1 GB budget; Docker
  daemon + Watchtower cost ~160 MB we don't have. See §1 and §11.
- Don't assume the frontend exists. It's deferred. When `frontend/` gets
  added in a later phase, remove the
  `if: steps.frontend.outputs.exists` gates from both workflows
  (`.github/workflows/build.yml`, `.github/workflows/release.yml`) and
  remove the placeholder `internal/web/dist/index.html`. Leave
  `internal/web/dist/.gitkeep` in place so the directory survives when
  the real Vue build overwrites index.html.

---

## 11. Deployment

This system runs on bare-metal systemd, not Docker. **If you see a
Dockerfile or docker-compose in a PR, reject it.**

Four systemd units on the Pi (sources under `deploy/systemd/`):

| Unit                          | Type     | User   | Memory cap  |
|-------------------------------|----------|--------|-------------|
| `muzika.service`              | simple   | muzika | 150 MB      |
| `slskd.service`               | simple   | muzika | 400 MB      |
| `muzika-updater.service`      | oneshot  | root   | —           |
| `muzika-updater.timer`        | —        | —      | fires every 5 min |

### Autoupdate flow

Every 5 minutes the timer runs `/usr/local/bin/muzika-update`, which:

1. `git fetch` + `reset --hard origin/main` in `/srv/muzika/repo`.
2. Calls `/usr/local/bin/muzika-decrypt` to SOPS-decrypt
   `deploy/.env.sops` into `/etc/muzika/muzika.env` and
   `/etc/muzika/slskd.env`, splitting by variable prefix
   (`MUZIKA_*` vs `SLSKD_*`/`SOULSEEK_*`).
3. Queries `https://api.github.com/repos/<repo>/releases/latest`. On HTTP
   403 (rate-limited) logs and exits 0 — next tick retries.
4. If the tag doesn't start with `v`, exits 1 (guards against upstream
   mistakes).
5. Compares tag to `/var/lib/muzika/version`. If binary and env are
   both unchanged, exits 0.
6. Otherwise downloads `muzika-linux-arm64` + `.sha256` from the
   release, verifies checksum, atomic-renames into
   `/usr/local/bin/muzika`, writes the tag.
7. `systemctl restart muzika`.

### Useful commands on the Pi

- Follow logs: `journalctl -u muzika -f` (slog JSON lives here).
- Follow slskd: `journalctl -u slskd -f`.
- Force an update check immediately: `sudo systemctl start muzika-updater.service`.
- Halt autoupdates: `sudo systemctl stop muzika-updater.timer`.
- List timer history: `systemctl list-timers muzika-updater.timer`.

### Where secrets live on the Pi

- `/etc/muzika/age.key` — age private key, mode 0400, owned root.
- `/etc/muzika/muzika.env` — decrypted `MUZIKA_*` vars.
- `/etc/muzika/slskd.env`  — decrypted `SLSKD_*` and `SOULSEEK_*` vars.
- `/etc/muzika/slskd.yml`  — slskd config (non-secret, seeded from
  `deploy/slskd.yml.template` by the installer; operator-editable).

`/etc/muzika` itself is 0750 owned `root:muzika` — the muzika user can
read env files but nothing else can.

### Where state lives on the Pi

- `/srv/muzika/data/muzika.db` — SQLite file.
- `/srv/muzika/data/music/` — audio files, shared with slskd.
- `/srv/muzika/repo/` — git checkout maintained by the updater.
- `/var/lib/muzika/version` — currently installed release tag.
- `/var/lib/slskd/` — slskd's own state.
