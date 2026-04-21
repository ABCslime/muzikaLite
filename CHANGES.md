# CHANGES.md — where each v2 revision item landed

Maps each of the 28 revision items from the Phase 2 review to the section of `ARCHITECTURE.md` v2 where it was applied.

Ordering mirrors the review message's clusters.

## Hardware target (6)

| # | Item                                                                 | Landed in                                                        |
|---|----------------------------------------------------------------------|------------------------------------------------------------------|
| 1 | SQLite at `/data/muzika.db` via `modernc.org/sqlite`; WAL, `foreign_keys=ON`, `busy_timeout=5000`; schema-as-table-prefix (`auth_users`, `playlist_playlists`, `playlist_songs`, `queue_songs`, `queue_entries`, `queue_user_songs`, `outbox`) | §1 (budget row on SQLite cache_size); §3 (library table); §5 open sequence; §5 full DDL block |
| 2 | Cross-compile strategy: Ubuntu runner does `GOOS=linux GOARCH=arm64`, assembles with `docker buildx`, pushes to ghcr.io (public repo = free) | §8 Dockerfile (`--platform=$BUILDPLATFORM` stages, `CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH`); §11 `build.yml` |
| 3 | Watchtower on the Pi polls ghcr.io, auto-updates `muzika` on new `:latest`; ~20 MB RAM | §1 memory-budget table (30 MB limit); §8 `watchtower` service block; §14 diagram |
| 4 | Approval gate = tagged releases. Every push to `main` → `:sha-<shortsha>`. A `v*` tag triggers `release.yml` that re-tags existing `:sha-*` as `:latest`. Watchtower watches `:latest` only | §11 both workflows; §11 final paragraph on no-rebuild promotion via `buildx imagetools create` |
| 5 | `mem_limit` / `mem_reservation` on every service (muzika 150/75, slskd 400/200, watchtower 30/15) | §1 budget table; §8 each service block |
| 6 | Swap setup documented in README (USB stick, not SD); step-by-step, `vm.swappiness`, monitoring | §1 "Swap mitigation" subsection (spec for README); README itself is scaffolded in Phase 3 |

## Soulseek backend architecture (6)

| # | Item                                                                 | Landed in                                                        |
|---|----------------------------------------------------------------------|------------------------------------------------------------------|
| 7 | `SoulseekClient` interface in new `internal/soulseek` package: `Search`, `Download`, `DownloadStatus`; backend selected by `MUZIKA_SOULSEEK_BACKEND={slskd|native}`, default `slskd` | §3 repo layout (internal/soulseek/ tree); §7 interface block; §7 selector block; §10 `Config.SoulseekBackend`; §13 main.go switch |
| 8 | slskd impl is first-class, ships day one; whole system must work end-to-end on slskd before gosk starts | §7 "slskd implementation — first-class, ships day one" subsection |
| 9 | Native Go impl lives in a separate module at `github.com/<user>/gosk`, depends on `github.com/bh90210/soul`; not under `internal/` | §3 "Out-of-repo (separate module)" block; §7 "gosk — separate Go module, lives outside this repo" subsection |
| 10 | gosk v1 scope is explicit and bounded: login+session, search with N-sec window, peer selection, single-file download with resume, state persistence. Explicit non-goals listed | §7 "gosk v1 scope — explicit" + "gosk v1 explicit non-goals" |
| 11 | gosk RAM target 40–80 MB under load; if over, gosk stays experimental and muzika sticks with slskd | §7 "RAM target for gosk: 40–80 MB under load" subsection |
| 12 | Strict development order: scaffold → all ports against slskd → muzika fully working → then gosk v1 → then swap behind interface. Muzika never blocked on gosk | §7 "Development order — strict" numbered list |

## Bus design — Cluster 1 (2)

| #  | Item                                                                 | Landed in                                                        |
|----|----------------------------------------------------------------------|------------------------------------------------------------------|
| 13 | Fan-out fix: per-subscriber channels. `Subscribe[T]` returns a fresh channel; `Publish[T]` fans out to every subscriber. Fixes the `RequestSlskdSong` double-consumer bug | §4 "Subscribe / Publish model" subsection + code sketch; table note "fan-out case ... now works correctly" |
| 14 | Outbox pattern for all state-change events (UserCreated, UserDeleted, LikedSong, UnlikedSong, LoadedSong). Request events published directly | §4 "Transactional outbox" subsection; §4 event-classification lists; §5 `outbox` table DDL; §13 main.go `bus.StartOutboxDispatcher` |

## Database — Cluster 2 (4)

| #  | Item                                                                 | Landed in                                                        |
|----|----------------------------------------------------------------------|------------------------------------------------------------------|
| 15 | One linear migration stream: single `migrations/` dir, `0001_init.up.sql` creates all tables in dependency order; golang-migrate with SQLite driver | §3 repo layout (migrations/ shows single `0001_*`); §5 "Schema ... one linear stream" subsection with full DDL |
| 16 | Per-user `sync.Mutex` map in `queue.service` for queue-position concurrency; pattern documented in CLAUDE.md | §3 repo layout (queue/service.go comment); §5 "Per-user queue mutex" subsection with full Go pattern + CLAUDE.md mandate |
| 17 | Direct FK coupling to `auth_users`, no local user copies; `ON DELETE CASCADE` on every `user_id` FK; `UserDeleted` event still published for future cache invalidation | §5 every FK in the DDL uses `REFERENCES auth_users(id) ON DELETE CASCADE`; §5 "User deletion, cascade, and UserDeleted event" subsection |
| 18 | Partial unique index only for system-liked: `CREATE UNIQUE INDEX ... WHERE is_system_liked = 1`. Removed the incorrect composite UNIQUE from v1 | §5 DDL for `playlist_playlists` plus `uniq_system_liked_per_user` partial index; explicit note "(v1 had an incorrect composite UNIQUE; replaced with this partial index.)" |

## HTTP layer — Cluster 3 (4)

| #  | Item                                                                 | Landed in                                                        |
|----|----------------------------------------------------------------------|------------------------------------------------------------------|
| 19 | CORS configurable via `MUZIKA_CORS_ORIGINS` (comma-separated). Empty = no headers emitted. No wildcards anywhere | §6 "CORS (configurable, no wildcards)" subsection + code sketch; §10 `Config.CORSOrigins` |
| 20 | JWT revocation via `token_version`: column on `auth_users`, `tv` claim, middleware rejects stale tv, `POST /api/auth/logout-all` increments | §5 DDL `token_version INTEGER NOT NULL DEFAULT 0`; §6 "JWT: `tv` claim and revocation" subsection with pseudocode; §6 routing table lists `POST /api/auth/logout-all` |
| 21 | Per-route method-specific mux mounting using Go 1.22 patterns; public routes mounted without middleware, protected wrapped explicitly with `httpx.WithAuth`; no handler wraps its own auth | §6 "Routing: Go 1.22 method patterns, explicit wrapping" subsection with full code block |
| 22 | Handler testability convention chosen and documented in CLAUDE.md: context-based userID, services take `userID` as first business arg | §6 "Handler testability convention (CLAUDE.md entry)" subsection — verbatim text for CLAUDE.md |

## Secrets + volumes — Cluster 4 (2)

| #  | Item                                                                 | Landed in                                                        |
|----|----------------------------------------------------------------------|------------------------------------------------------------------|
| 23 | SOPS-encrypted `.env` in git. Age keypair; private key only on Pi at `/etc/muzika/age.key`. CI does not need the key. Setup + rotation in README | §3 repo layout (`.env.sops` in `deploy/`, `.sops.yaml` at root); §12 "SOPS-encrypted `.env` in git" subsection with setup + rotation steps |
| 24 | Pin explicit UIDs for shared audio volume: both muzika and slskd run as 1000:1000. Dockerfile `USER 1000:1000`, slskd `PUID=1000 PGID=1000`. Comment explaining why | §8 both compose service blocks (`user: "1000:1000"` on muzika; `PUID`/`PGID` on slskd); §8 Dockerfile `USER 1000:1000`; §12 "Volume UID pinning" subsection |

## Misc (4)

| #  | Item                                                                 | Landed in                                                        |
|----|----------------------------------------------------------------------|------------------------------------------------------------------|
| 25 | MIME via `mime.TypeByExtension(filepath.Ext(path))`, fallback `application/octet-stream`; serve via `http.ServeContent` so Range requests work | §6 "MIME + range requests for audio" subsection with full handler code |
| 26 | Refiller on-demand only, no background ticker. Matches old behavior | §9 "Refiller" section (single-paragraph definitive statement) |
| 27 | Frontend dev workflow: when `internal/web/dist` empty, serve nothing at `/`; dev uses Vite on :3000. Document both dev and prod workflows | §3 repo layout (note that `dist/` populated during Docker build); §16 open-questions item 1 ("locked"); README to be written in Phase 3 will include both flows |
| 28 | Pre-commit hook blocking `.env*` except `.env.example` and `.env.sops`, installed via `.githooks/pre-commit` | §3 repo layout (`.githooks/pre-commit`); §12 "Pre-commit hook" subsection with full script and `core.hooksPath` instruction |

## Structural additions mandated by the review

In addition to the 28 line items, the review required:

- **"Hardware constraints" section covering Pi 3 1 GB implications** → §1 "Hardware constraints" (new).
- **"Soulseek backend evolution" section covering slskd-now-gosk-later plan + interface contract** → §7 "Soulseek backend evolution" (new).

## What was removed from v1 (for completeness)

- Multi-schema PostgreSQL design → replaced by single SQLite file with prefixed table names (items 1, 15).
- Per-module `migrations/auth`, `migrations/playlist`, `migrations/queue` directories → collapsed to `migrations/` (item 15).
- v1 composite UNIQUE on `(user_id, is_system_liked) DEFERRABLE INITIALLY DEFERRED` → replaced by partial unique index (item 18).
- v1 "one-channel-per-event-type, multiple consumers" bus model → replaced by per-subscriber channels (item 13).
- v1 self-hosted runner on the Pi for builds → removed in favor of ubuntu-latest cross-compile + Watchtower pull (items 2, 3, 4).
- v1 CORS wildcard plan ("mirrors the old nginx ingress, allow-origin `*`") → replaced with strict configurable origins (item 19).
- v1 distroless:nonroot user → replaced with explicit `USER 1000:1000` to match slskd's PUID (item 24).
- v1 manual-approval gate as a separate workflow job → replaced by tag-driven release promotion (item 4).

---

## v0.3.0 — slskd retired; Soulseek goes in-process (2026-04-21)

The v2 plan (items 7–12 above) carried a two-backend design: ship on
slskd, then swap to gosk once it met a 40–80 MB RAM target. v0.3.0 is
the point where the swap happened — not as a runtime flip but as a
deletion. The slskd implementation, the `MUZIKA_SOULSEEK_BACKEND`
switch, and the `slskd.service` systemd unit were removed.

### Code

| Before (v0.2.x)                                    | After (v0.3.0)                                   |
|----------------------------------------------------|--------------------------------------------------|
| `internal/slskd/` package                          | `internal/download/` (renamed — drives gosk, not slskd) |
| `bus.RequestSlskdSong` event                       | `bus.RequestDownload` (renamed)                  |
| `internal/soulseek/slskd.go`, `slskd_test.go`, `id.go` | deleted                                      |
| Two `soulseek.Client` impls behind a switch        | One impl: `internal/soulseek/native.go` (gosk)   |
| `Config.SoulseekBackend`, `SlskdURL`, `SlskdUsername`, `SlskdPassword` | deleted                   |
| `Config.SlskdWorkers` (`MUZIKA_SLSKD_WORKERS`)     | `Config.DownloadWorkers` (`MUZIKA_DOWNLOAD_WORKERS`) |
| `Config.SoulseekUsername` / `Password` optional    | `required:"true"` — muzika won't start without Soulseek creds |
| `DownloadHandle{ID, Peer, Filename}`               | `DownloadHandle{ID}` (gosk handles peer/filename internally) |

### Deploy

| Before                                             | After                                            |
|----------------------------------------------------|--------------------------------------------------|
| Two services: `muzika.service` + `slskd.service`   | One service: `muzika.service`                    |
| Two env files: `/etc/muzika/muzika.env` + `/etc/muzika/slskd.env` | One file: `/etc/muzika/muzika.env` |
| `/etc/muzika/slskd.yml` non-secret config          | gone — every gosk knob is a `MUZIKA_*` env var  |
| `deploy/systemd/slskd.service`, `deploy/slskd.yml.template` | deleted                                 |
| `install.sh` downloaded slskd binary, seeded yml   | `install.sh` installs only age/sops; no sidecar  |
| `muzika-decrypt` split decrypted env by prefix     | writes a single file                             |
| `muzika-update` restarted both services            | restarts only muzika                             |
| UID 1000 chosen to match slskd's PUID              | UID 1001 (`muzika` system user) — no shared-UID constraint remaining |

### Memory

| Component              | Pre-v0.3.0 | Post-v0.3.0 | Delta   |
|------------------------|------------|-------------|---------|
| `muzika.service`       | 150 MB     | 150 MB      | 0       |
| `slskd.service`        | 400 MB     | —           | −400 MB |
| **Total**              | **~550 MB** | **~150 MB** | **−400 MB** |

The Pi 3 went from ~470 MB headroom to ~870 MB. v0.3.0 is the largest
memory delta since the Docker→systemd migration in Phase 3.5.

### Why the rename

`internal/slskd` described a module that drove an external daemon.
With v0.3.0 the same code drives an in-process Go library. Keeping
the name would misdescribe the package's responsibility. Same reasoning
for the event: `RequestSlskdSong` → `RequestDownload`. The event
name now describes intent (please obtain this track) rather than the
backend that fulfills it — matching the shape of `RequestRandomSong`.

### What the v2-plan evolution items 7–12 now mean

- Item 7 (backend selector) — **removed**. One code path, no switch.
- Item 8 (slskd first-class) — **satisfied and retired**. slskd shipped
  through v0.2.x; gosk superseded it.
- Items 9–10 (gosk module, scope, non-goals) — **shipped as v0.1.0 at
  github.com/ABCslime/gosk**. Muzika consumes it as a library.
- Item 11 (RAM target) — **met**. gosk runs in-process inside muzika's
  150 MB cap; no dedicated cgroup to measure anymore, but the whole
  muzika RSS sits well under the cap with gosk integrated.
- Item 12 (strict dev order) — **followed**. Every module was ported
  against slskd (Phases 4–8); muzika ran fully on slskd through v0.2.x;
  gosk stabilized in Phase 8.5; v0.3.0 deleted slskd once gosk was
  proven.

---

## v0.4 PR 2 — Discogs + quality gate + catalog-number ladder + observability

Second of three v0.4 PRs. Ships real product change (not just plumbing)
in four coordinated pieces, all gated by ROADMAP §v0.4.

### 1. Second seeder: Discogs (`internal/discogs`)

Discogs API integration as the second discovery source. Bandcamp is the
reliability floor, Discogs is the higher-variance long-tail catalog.

- **Auth**: Personal Access Token via `Authorization: Discogs token=<PAT>`.
  Read-only; no OAuth dance. `MUZIKA_DISCOGS_TOKEN` required when enabled.
- **Rate limit**: per-client token bucket, 1 token/s refill, 5 burst.
  Well under Discogs' 60/min authenticated quota.
- **Cache**: 30-day SQLite cache of API responses (`discogs_cache`
  table, migration 0003). Expired rows hidden by readers and swept at
  startup.
- **Genre vocabulary**: separate `MUZIKA_DISCOGS_DEFAULT_GENRES`
  (Discogs' closed vocabulary) from `MUZIKA_BANDCAMP_DEFAULT_TAGS`
  (Bandcamp's free-form tags). v0.6 introduces cross-source mapping.
- **Default off**: `MUZIKA_DISCOGS_ENABLED=false` so upgrades from
  v0.3.x don't require ops action.

### 2. Weighted refiller routing

Both seeders subscribe to the `DiscoveryIntent` channel. For per-intent
exclusivity (not doubling the download rate), the refiller writes a
one-element `PreferredSources` list picked by a weighted RNG:

| `MUZIKA_DISCOGS_WEIGHT` | Bandcamp share | Discogs share |
|-------------------------|----------------|---------------|
| 0.0 (Discogs disabled)  | 100%           | 0%            |
| 0.3 (default, enabled)  | 70%            | 30%           |
| 0.5                     | 50%            | 50%           |
| 1.0                     | 0%             | 100%          |

Seeders return early on source mismatch — the intent isn't for them.

### 3. Quality gate + catalog-number ladder (`internal/download`)

Gate thresholds (defaults, all configurable):

| Floor                  | Default      | Env                              |
|------------------------|--------------|----------------------------------|
| Min bitrate            | 192 kbps     | `MUZIKA_DOWNLOAD_MIN_BITRATE_KBPS` |
| Min file size          | 2 MB         | `MUZIKA_DOWNLOAD_MIN_FILE_BYTES`   |
| Max file size          | 200 MB       | `MUZIKA_DOWNLOAD_MAX_FILE_BYTES`   |
| Peer queue ceiling     | 50           | `MUZIKA_DOWNLOAD_PEER_MAX_QUEUE`   |
| Codec preference       | flac > mp3 > other | (hardcoded)                 |

Zero-valued bitrate/FilesShared are treated as "unknown" (gosk leaves
them at 0 when the wire-level response omits them); rejecting on 0
would silently drop every result.

Ladder rungs in priority order:

0. `catno` — present only when `RequestDownload.CatalogNumber` is
   non-empty (Discogs seeder populates; Bandcamp leaves blank). On
   probation per ROADMAP — instrumented via `discovery_log` so we can
   measure hit rate over three months.
1. `artist + " " + title`.
2. `title`.

First rung with `MUZIKA_DOWNLOAD_LADDER_ENOUGH_RESULTS` post-gate passes
(default 3) wins; remaining rungs are skipped. Rung 0 uses the full
search window; later rungs use `MUZIKA_DOWNLOAD_LADDER_RUNG_WINDOW`
(default 5s) to cap worst-case miss latency.

If strict rejects everything at every rung, one relax pass (halved
floors, doubled ceilings) runs before the worker emits
`LoadedSong{Error}`. PR 2's relax is silent; PR 3 splits it by intent
origin so user-initiated search can surface the relaxation.

### 4. `discovery_log` observability (migration 0003)

Every seeder pick, every ladder rung, every gate outcome, every picked
peer writes a row. Columns: `song_id, user_id, source, strategy, stage,
rung, query, outcome, reason, result_count, filename, peer, bitrate,
size`. Never deleted.

Seed-stage rows carry the originating `DiscoveryIntent.Strategy`.
Download-stage rows leave strategy NULL — aggregations self-join on
`song_id` to recover it.

Writer lives in `internal/discovery/log.go`. Synchronous INSERTs on
the shared single-writer `*sql.DB`; a nil `*Writer` is a valid receiver
so tests can skip observability.

### Files

New packages:
- `internal/discogs/` — client, rate limiter, cache, worker, errors, tests
- `internal/discovery/` — `Writer` + `Record`

New files:
- `internal/db/migrations/0003_discovery_log.{up,down}.sql`
- `internal/download/gate.go`, `gate_test.go`
- `internal/download/discoverywriter_helper_test.go`

Modified:
- `cmd/muzika/main.go` — wires `discovery.Writer`, `discogs.Service`
  (behind `MUZIKA_DISCOGS_ENABLED`), passes ladder/gate config into
  `download.NewServiceWithConfig`
- `internal/bus/events.go` — `RequestDownload.CatalogNumber` added
- `internal/config/config.go` — 9 new knobs, new validation
- `internal/download/worker.go` — rewritten around the ladder + gate
- `internal/download/worker_test.go` — ladder + gate + relax tests
- `internal/queue/refiller.go` — `NewRefillerWithDiscogs`, weighted
  `pickSource()`
- `internal/queue/service.go` — `NewServiceWithDiscogs` constructor
- `internal/bandcamp/worker.go` — PreferredSources filter
- `.env.example` — 10 new lines documenting every knob
- `CLAUDE.md`, `ARCHITECTURE.md` — inline notes

### Verification

- `go build ./...` clean
- `go vet ./...` clean
- `go test ./...` all packages pass:
  `auth, bandcamp, bus, discogs, download, playlist, queue`

### Scope lock honored

This commit explicitly does not ship ROADMAP §v0.4 PR 3 (user search
endpoint + typo handling + relax-mode split). PR 3 follows on review.
