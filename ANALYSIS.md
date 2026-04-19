# Muzika Platform — Phase 1 Analysis

Analysis of the 6 source repositories in `../digeper-old/`, prepared for consolidation into a single Go monorepo.

- **5 Java/Spring Boot services** (Java 17, Spring Boot 3.5.6/3.5.7, Maven, Jib-based Docker images)
- **1 Vue 3 SPA** (Vite, Pinia, Tailwind, no TypeScript)
- **Shared PostgreSQL** in K8s (MySQL locally, same DB name: `queue_manager_db` — see [§6.1](#61-shared-database-naming))
- **Kafka** (Strimzi on K8s) is the only inter-service transport
- **Azure/Key Vault / ACR** for cloud deploy; all services use `jib-maven-plugin`

---

## 1. Service inventory

| Service          | Port | Java | Spring Boot | DB? | Kafka? | External I/O         |
|------------------|------|------|-------------|-----|--------|----------------------|
| authManager      | 8091 | 17   | 3.5.7       | yes | producer | — (none)           |
| PlaylistManager  | 8092 | 17   | 3.5.7       | yes | consumer | — (none)           |
| QueueManager     | 8090 | 17   | 3.5.7       | yes | both     | filesystem (audio) |
| BandcampApi      | 8080 | 17   | 3.5.6       | no (JPA excluded) | both | bandcamp.com (jsoup scrape) |
| SlskdDownloader  | 8080 | 17   | 3.5.6       | no  | both     | slskd HTTP API     |
| frontend (Vue 3) | 3000 | —    | —           | —   | —        | 3 services via proxy |

---

## 2. authManager

### Purpose
Identity provider. User registration, login, JWT issuance. Stateless bearer-token auth for the other services (they all validate JWTs using the same shared secret).

### REST endpoints
All in `AuthorizationController` + `HealthController`:

| Method | Path(s)                       | Body                              | Response                        | Auth |
|--------|-------------------------------|-----------------------------------|---------------------------------|------|
| POST   | `/user` · `/api/auth/user`    | `{username, password, email}`     | `{id, username, email, ...}` 201| none |
| POST   | `/login` · `/api/auth/login`  | `{username, password}`            | `{token, userId, username, email}` | none |
| DELETE | `/user/{id}`                  | —                                 | 204                             | JWT  |
| GET    | `/` · `/health`               | —                                 | `{status, service}`             | none |

CORS is set to `*` in `SecurityConfig.java`.

### DB tables
Single entity `User` → table **`usersAuth`**:

| Column      | Type      | Notes                    |
|-------------|-----------|--------------------------|
| id          | UUID      | PK, `@GeneratedValue`    |
| username    | VARCHAR   | NOT NULL, UNIQUE         |
| password    | VARCHAR   | NOT NULL (bcrypt hash)   |
| email       | VARCHAR   | nullable                 |
| created_at  | TIMESTAMP | NOT NULL, immutable      |
| updated_at  | TIMESTAMP | nullable                 |

No Liquibase/Flyway. `ddl-auto=update`.

### Kafka
- **Produces `user-created`**: `{userId: UUID, username: String}` — sent from `AuthorizationService.createUser()` via `KafkaProducerService`.
- Consumes: none.

### External deps
None — no `RestTemplate`/`WebClient`/`Feign`.

### Config
- `jwt.secret`, `jwt.expiration` (24h default)
- `spring.datasource.*` (MySQL local `jdbc:mysql://localhost:3306/queue_manager_db`, PostgreSQL in K8s via `${POSTGRES_URL}`)
- `spring.kafka.bootstrap-servers`

### Notes / concerns
- JWT secret defaults to a hardcoded dev string.
- Password min length is 6 (weak).
- No rate limiting, no audit log, no soft-delete.

---

## 3. PlaylistManager

### Purpose
Owns user playlists and their songs. Reacts to `liked`/`unliked` Kafka events by maintaining a special **"Liked"** playlist per user (hardcoded name).

### REST endpoints (all JWT-protected, base `/api/playlist`)

| Method | Path                           | Body                       | Response                       |
|--------|--------------------------------|----------------------------|--------------------------------|
| GET    | `/`                            | —                          | `PlaylistResponse[]`           |
| GET    | `/{id}`                        | —                          | `PlaylistWithSongsResponse`    |
| POST   | `/`                            | `{name, description?}`     | `PlaylistResponse` 201         |
| DELETE | `/{id}`                        | —                          | 204                            |
| POST   | `/{id}/song/{songId}`          | —                          | 201                            |
| DELETE | `/{id}/song/{songId}`          | —                          | 204                            |

### DB tables
3 entities (no migrations; `ddl-auto=update`):

- **`users_playlist`**: `uuid` (PK), `user_name`, `user_id` (UUID — the auth-service userId).
- **`playlists`**: `id` (PK UUID), `user_id` (no FK), `name`, `description` TEXT, `created_at`, `updated_at`. `@OneToMany` → `PlaylistSong` with cascade delete.
- **`playlist_songs`**: composite PK `(playlist_id, song_id)`, plus `position INT`, `added_at`. Songs reference QueueManager songs by UUID — no DB-level FK.

### Kafka (consumer only, group `playlist-manager-group`)

| Topic          | Payload                                        | Effect                                           |
|----------------|------------------------------------------------|--------------------------------------------------|
| `user-created` | `{userId, username}`                           | Creates local user + a new **"Liked"** playlist. |
| `liked`        | `{userId, username, songId}`                   | Adds `songId` to that user's "Liked" playlist.   |
| `unliked`      | `{userId, username, songId}`                   | Removes `songId` from "Liked" playlist.          |

Produces: **none**.

### External deps
None.

### Notes / concerns
- `User` entity has both `uuid` (local PK) and `userId` (auth service's UUID) — service falls back between them inconsistently. **Treat `user_id` (auth UUID) as the canonical identifier in the port.**
- Error handling in consumers is inconsistent: `user-created` re-throws (retries), `liked`/`unliked` just log-and-swallow.
- Position gaps on song removal are not closed (not reordered).

---

## 4. QueueManager (the central hub)

### Purpose
Owns the per-user playback queue, tracks listen/skip/like stats, and **triggers automatic refills** when the queue dips below the threshold (10 songs). Also serves the audio file bytes over HTTP. This is the orchestrator — every other service exists to feed it.

### REST endpoints (base `/api/queue`, JWT required unless noted)

| Method | Path                       | Body / Params                   | Response                      |
|--------|----------------------------|---------------------------------|-------------------------------|
| GET    | `/queue`                   | —                               | `{songs: SongDTO[]}` — also triggers async refill |
| POST   | `/queue`                   | `{songId, position}`            | 200                           |
| POST   | `/queue/check`             | —                               | manual refill trigger         |
| POST   | `/queue/skipped`           | `{songId, queueEntryId?}`       | mark skipped, remove, refill  |
| POST   | `/queue/finished`          | `{songId, queueEntryId?}`       | increment listen count, remove, refill |
| GET    | `/songs/{id}`              | —                               | `audio/mpeg` byte stream      |
| GET    | `/songs/{id}/liked`        | —                               | `{liked: bool}`               |
| POST   | `/songs/{id}/liked`        | —                               | 200 + emits `liked` event     |
| POST   | `/songs/{id}/unliked`      | —                               | 200 + emits `unliked` event   |
| GET    | `/` · `/health`            | —                               | public health                 |

### DB tables
5 entities (`ddl-auto=update`, no migrations):

- **`queue`**: `user_uuid` (PK, `@MapsId`), `uuid`. OneToOne with User, OneToMany with QueueSong.
- **`queue_users`**: `uuid` (PK), `user_name`, `user_id`.
- **`songs`**: `id` (PK UUID), `title`, `artist`, `album`, `genre`, `duration`, `url` (filesystem path).
- **`queue_songs`**: `id` (PK UUID), `queue_user_uuid` (FK), `songs_id` (FK), `queue_uuid`, `position`. Unique (queue_user_uuid, songs_id).
- **`user_songs`**: composite PK (user_id, song_id), `listen_count`, `first_listend_at`, `last_listend_at` (note: typo in column names), `liked`, `skipped`.

### Kafka

**Consumes** (group `queue-manager-group` for user-created; default `group-id` for the rest):

| Topic                 | Payload                                   | Effect                                  |
|-----------------------|-------------------------------------------|-----------------------------------------|
| `user-created`        | `{userId, username}`                      | Creates local user, seeds queue with 10 random songs. |
| `loaded-song`         | `{uuid, filePath, status}` where status ∈ {COMPLETED, ERROR} | On COMPLETED, updates song + adds to queue; on ERROR, deletes song stub. Triggers refill. |
| `request-slskd-song`  | `{id, title, artist}`                     | Updates song metadata on an existing song record (the stub the request was built from). |

**Produces**:

| Topic                    | Payload                                     | When                                   |
|--------------------------|---------------------------------------------|----------------------------------------|
| `request-random-song`    | `{songId, genre}` (genre hardcoded `"hisa"`)| Queue below 10 songs → fan out to BandcampApi. |
| `liked`                  | `{userId, username, songId}`                | POST `/songs/{id}/liked`.              |
| `unliked`                | `{userId, username, songId}`                | POST `/songs/{id}/unliked`.            |

### External deps
- **Filesystem**: audio files at `${music.storage.base-path}` — local dev: `/Users/macabc/IdeaProjects/muzika/music-storage/downloads`; K8s: `/mnt/azfiles/downloads` (Azure File mount).

### Notes / concerns
- Default genre `"hisa"` is hardcoded (looks like a typo/slug).
- Column names `first_listend_at`/`last_listend_at` are misspelled.
- Refill is fire-and-forget via `CompletableFuture.supplyAsync` — no error propagation.
- Startup `cleanupInvalidSongs()` deletes songs without files — silent in zero-cleaned case.
- Magic number `10` (min queue size) repeated in multiple places.

---

## 5. BandcampApi

### Purpose
Stateless Kafka worker. Receives a "give me a random song of genre X" request, scrapes Bandcamp's discovery endpoint, returns metadata for slskd to try to download.

### REST endpoints
Only `/` and `/health`. No business API.

### DB
**None.** `BandcampApiApplication` excludes `DataSourceAutoConfiguration` and `HibernateJpaAutoConfiguration`. JPA + Postgres driver are on the classpath but unused.

### Kafka

| Direction | Topic                   | Payload                                      |
|-----------|-------------------------|----------------------------------------------|
| consume   | `request-random-song`   | `{songId, genre}` (group `bandcampapi-group`) |
| produce   | `request-slskd-song`    | `{id, title, artist}` (id = the `songId` from the request) |

### External deps — Bandcamp
Uses **jsoup 1.21.2** to POST against `https://bandcamp.com/api/discover/1/discover_web` with a Firefox-masquerading `User-Agent`. No official API, no auth, no cookies. Album artwork at `https://f4.bcbits.com/img/a{id}_2.jpg`. Timeouts: 100s GET / 30s POST (GET is unusually long).

### Notes / concerns
- **The `genre` parameter from the Kafka message is ignored** — `BandcampSearcher` uses a hardcoded tag list `["electronic", "house", "progressive-house", "melodic-techno"]`. Effectively QueueManager's request is genre-blind.
- `login()` on 401 is an empty stub — recursive `getDocument()` can loop if Bandcamp ever challenges.
- Uses `System.out.println` for status codes in `WebSearcher`.
- `RequestSlskdSong` has a suspicious regex `\(.*?\)}` (unmatched brace).
- Spring Cloud dependency is declared (2025.0.0 BOM) but no Cloud features are actually used.

---

## 6. SlskdDownloader

### Purpose
Stateless Kafka worker. Given an `{id, title, artist}`, drive the slskd daemon through its REST API to search Soulseek, pick a peer with results, download the file, and report the resulting file path back.

### REST endpoints
Only `/` and `/health`.

### DB
None. No JPA on the classpath. State held in-memory only.

### Kafka

| Direction | Topic                 | Payload                                               |
|-----------|-----------------------|-------------------------------------------------------|
| consume   | `request-slskd-song`  | `{id, title, artist}` (group `slskd-download-group`)  |
| produce   | `loaded-song`         | `{uuid, filePath, status: COMPLETED\|ERROR}`          |

### External deps — slskd HTTP API
Base URL: `${slskd.api.url}` (default `http://localhost:5030`, K8s hardcoded to `http://20.215.135.129:5030`). Uses jsoup HTTP (not `RestTemplate`). Flow in `SlskdSearcher`:

1. `POST /api/v0/session` — login with credentials **hardcoded as `slskd:slskd`**, gets a token.
2. `POST /api/v0/searches` — submit search with a reserved UUID from a pool.
3. `GET /api/v0/searches/{id}` — poll until `isComplete`.
4. `GET /api/v0/searches/{id}/responses` — pick a peer (filters by file count, queue length).
5. `POST /api/v0/transfers/downloads/{username}` — start download.
6. `GET /api/v0/transfers/downloads` — poll until state `"Completed, Succeeded"`.
7. `DELETE /api/v0/searches/{id}` — clean up.

On 401, auto re-login and retry in `WebSearcher`.

### Notes / concerns
- **Hardcoded slskd credentials `slskd:slskd` in source.** Must move to config on port.
- **Search ID pool is exactly 8 hardcoded UUIDs** in `SlskdSearchIdController`. The 9th concurrent request returns `null` → NPE. Worth replacing with `UUID.randomUUID()` on port.
- Package name is `kafkaMassages` (typo) — shared with BandcampApi.
- `@Async` thread pool has `setQueueCapacity(Integer.MAX_VALUE)` — unbounded.
- `RequestRandomSong` class exists in this service but is not consumed — dead code.
- On download success but failed `loaded-song` send, the download is not rolled back (fire-and-forget).

---

## 7. frontend (Vue 3 SPA)

### Purpose
Single-page music player. Login/register, queue view with playback controls, playlists CRUD.

### Stack
Vue 3 (Composition API), Vite 5, Pinia 2.1, Vue Router 4.2, Axios 1.6, Tailwind 3.4. **No TypeScript, no tests, no component library.** ~3.4k LOC.

### API integration
Axios instance (`src/api/client.js`) with interceptors that (1) inject `Authorization: Bearer <token>` from localStorage, (2) on 401 clear session and redirect to `/login`. **Timeout is set to `0` (infinite)** with a "testing" comment.

Base URLs come from `import.meta.env`:
```js
AUTH:     VITE_AUTH_API_URL     || '/api/auth'
PLAYLIST: VITE_PLAYLIST_API_URL || '/api/playlist'
QUEUE:    VITE_QUEUE_API_URL    || '/api/queue'
```

In dev, Vite proxy forwards `/api/*` → `http://localhost:8080`. **There is no explicit gateway in the old system — `localhost:8080` is assumed to be something routing to the 3 services**, but there's no single service listening there; this suggests an implicit ingress/path-based routing we'll need to reproduce (trivially, since in the new world a single Go binary serves them all).

### Endpoints called
Exactly the ones documented in §2, §3, §4. Audio files are fetched as blobs from `GET /api/queue/songs/:id` and played via `URL.createObjectURL`.

### Auth flow
Token + user JSON stored in `localStorage` keys `digeper_token` / `digeper_user`. Hydrated into Pinia on app init. Router guards check `authStore.isAuthenticated`.

### Stores
- `authStore` — user, token, login/register/logout.
- `playerStore` — complex: current song, playing state, seek, volume, shuffle, repeat, liked, blob URL cleanup.
- `queueStore` — queue fetch + add/remove; heavy debug logging.
- `playlistStore` — playlist CRUD.

### Notes / concerns
- Hardcoded 4-second post-registration loading screen.
- Heavy `console.log` with emoji prefixes throughout queue fetch — looks like active debugging.
- `playerStore.toggleLiked` exists; UI hookup appears partial.
- No error toasts/UI, errors mostly go to console.
- No `.env` file in repo — relies on defaults.

---

## 8. System-wide Kafka topology

```
authManager ──► user-created ──► PlaylistManager   (creates user + Liked playlist)
                             ──► QueueManager      (creates user + seeds 10 songs)

QueueManager ──► request-random-song ──► BandcampApi
BandcampApi  ──► request-slskd-song  ──► SlskdDownloader  AND  QueueManager
                                                               (metadata update)
SlskdDownloader ──► loaded-song ──► QueueManager
                                       (on COMPLETED: add to queue; on ERROR: delete song stub)

QueueManager ──► liked   ──► PlaylistManager (add to Liked playlist)
QueueManager ──► unliked ──► PlaylistManager (remove from Liked playlist)
```

Every topic is JSON-serialized with Jackson. Keys are UUID or String depending on topic. Auto-create is **on** in dev, **off** in K8s (Strimzi manages topics declaratively).

---

## 9. Deployment / CI today

- **Jib** plugin in every Java pom.xml pushes `eclipse-temurin:17-jre`-based images to `${ACR_NAME}.azurecr.io/muzika/<service>:<version>`. Target arch: amd64 (default). **Not ARM64.**
- **Kubernetes** manifests in each repo's `k8s/` folder: Deployment + Service, ConfigMap, SecretProviderClass (Azure Key Vault CSI driver mounted at `/mnt/secrets-store`), TCP-based probes.
- **Kafka** = Strimzi-managed (`kafka-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092`).
- **DB** = Azure Database for PostgreSQL, SSL required.
- **Audio storage** = Azure File share mounted at `/mnt/azfiles/downloads`.
- **GitHub Actions**: a top-level `.github/` folder exists at `/Users/macabc/digeper-old/.github` (not yet inspected — flagging for Phase 2).

---

## 10. Unresolved questions (flagging for Phase 2)

1. **Genre handling.** QueueManager sends `{genre: "hisa"}` (a hardcoded typo), and BandcampApi ignores genre entirely. Do you want the new Go binary to actually honor genre, or preserve the current behavior (pick from a fixed tag list)?
2. **The `request-slskd-song` double-consumer.** Both SlskdDownloader *and* QueueManager listen on this topic. SlskdDownloader uses it as its job input; QueueManager uses the same payload to update song metadata on a stub record. This works today because they're in different consumer groups. In the Go port this becomes a fan-out channel — trivial, but worth calling out so we don't accidentally route only to one consumer.
3. **Shared DB name `queue_manager_db`.** All services point at the same MySQL DB locally even though each has its own tables (`usersAuth`, `playlists`, `queue`, etc.). Was this ever really shared, or is every repo just copying the same connection string? Intent: **per-module schema inside one DB** (as you specified). I'll propose a schema-per-module layout in Phase 2.
4. **"Liked" playlist semantics.** Name is hardcoded. Is this a special system playlist that should be a DB flag, or stays as a named playlist we auto-create?
5. **slskd deployment.** Is slskd staying external (the Pi talks to it over the network), or should docker-compose spin it up as a sidecar container? The old K8s config points at `20.215.135.129:5030` (an Azure IP) — on the Pi this needs to change.
6. **Frontend auth storage.** Current localStorage-based JWT is XSS-exposed. Keep as-is for personal-use simplicity, or move to httpOnly cookie during the port?
7. **Kafka replacement granularity.** My plan for Phase 2: replace each topic with a typed Go channel, and run each consumer as a fixed-size worker pool. `loaded-song` and `request-random-song` currently have 1 partition each — so 1 worker is behavior-preserving. Slskd and Bandcamp scraping are the bottlenecks; they'd benefit from parallelism (configurable pool size).
8. **User deletion cascades.** authManager hard-deletes users. QueueManager and PlaylistManager have no `user-deleted` listener, so orphaned rows exist. Do you want the Go port to fix this (emit `user-deleted`, cascade everywhere) or preserve current behavior?
9. **JWT shared secret.** All 5 services validate JWTs using the same `jwt.secret`. In the single-binary world this becomes trivially one value — but do we want token-based auth at all between internal modules, or just session-level auth at the HTTP boundary and trust function calls internally? Recommend the latter.
10. **ARM64 images.** Nothing is currently ARM64-compatible — both base images and any native deps (jsoup is pure Java so fine). For the Go binary this is trivial (`GOARCH=arm64`); calling out so Phase 3's Dockerfile uses `--platform=linux/arm64` properly.
11. **.github folder at root.** There's a `.github/` at `/Users/macabc/digeper-old/.github/` sibling to the service repos. Haven't read its workflows yet — worth inspecting in Phase 2 to understand existing CI patterns before designing the new one.
12. **Search-ID pool of 8 UUIDs in SlskdDownloader.** Clearly a bug/limitation; safe to fix during the port (use `uuid.New()` per request).

---

## 11. What's carrying over vs. what's getting dropped

**Carrying over (business logic):**
- User registration/login + JWT (authManager)
- Playlist CRUD + Liked-playlist sync (PlaylistManager)
- Queue with auto-refill + listen stats + audio serving (QueueManager)
- Bandcamp discovery scrape (BandcampApi)
- slskd search/download orchestration (SlskdDownloader)
- The entire Vue SPA, unchanged on the client

**Getting dropped:**
- Kafka (→ Go channels)
- Inter-service HTTP (there is none anyway, except the frontend's)
- Separate JVM processes, separate `application-k8s.properties`
- Jib, Strimzi, Azure Key Vault CSI, Azure File mount, Azure Container Registry
- Spring Security filter chain (→ a small JWT middleware in Go)
- Liquibase/Flyway are **not** currently used — so "dropping" them is moot, but Phase 2 will propose adding migrations (e.g. `golang-migrate`) since `ddl-auto=update` is not acceptable for a Pi deploy.
- 8-UUID search pool, hardcoded slskd credentials, `System.out.println` logging, misspelled columns/packages — all fixed in-flight.

---

**End of Phase 1.** Ready for your review before I start Phase 2 (ARCHITECTURE.md).
