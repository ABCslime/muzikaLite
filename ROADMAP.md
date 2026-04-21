# ROADMAP.md — Muzika product and technical direction

This document is the single source of truth for where Muzika is going and
why. It is committed to the repo so that Claude Code, the project author,
and any future reviewers share one vision.

If this document and reality diverge, update this document first, then
the code. Do not let this document become stale. If a version ships with
different scope than listed here, edit this file in the same commit.

---

## Product thesis

Muzika is a self-hosted music platform for deep exploration of obscure
music, optimized for a small number of users (self, family, friends) who
want transparent, controllable discovery without algorithmic babying.

We are not competing with Spotify on the axes Spotify wins: session
coherence, mood-matched transitions, collaborative recommendations
computed over hundreds of millions of listeners.

We are beating Spotify on a different axis: transparent discovery of
long-tail catalogs, grounded in the songs and their context rather than
in aggregated listener behavior.

### The three principles

1.  **Content over crowd.** Similarity and discovery are grounded in
    facts about songs (artist, label, era, genre, audio features when
    available) — not in aggregated "users who liked X also liked Y"
    statistics.

2.  **Per-user, not multi-user.** Muzika supports multiple accounts per
    instance (family, friends), but no signal ever crosses account
    boundaries. One user's listen history never influences another
    user's recommendations. Ever. This is architectural, not a feature.

3.  **Quality over availability.** We prefer a smaller queue of
    high-quality tracks to a larger queue of low-bitrate filler. The
    popularity gate (§v0.4) enforces this. Relaxations are explicit,
    never silent.

---

## Version trajectory

Each version below has a defined scope, an explicit "not in this
version" section, and a list of architectural invariants it establishes.
Versions ship in order. Skipping ahead compromises the invariants.

### v0.1 — Shipped

Backend skeleton: auth, playlist, queue, bandcamp seeding, slskd
download backend. systemd deploy to Pi. JWT auth. SQLite, single binary.

### v0.2 — Shipped

Vue frontend ported. armv7 build for 32-bit Pi userland. Stream fixes.

### v0.3 — Shipped

Retired slskd sidecar. gosk is now the in-process Soulseek backend,
consumed as a Go module (github.com/ABCslime/gosk).

### v0.4 — Shipped (search, Discogs, quality gate)

Three PRs (1 → 2 → 3) landed on `main`; PR 2.1 and PR 2.2 followed as
stylistic + observability fixes. Every in-scope item below is live.

**In scope:**

1.  **DiscoveryIntent event abstraction.** Replace `RequestRandomSong`
    with `DiscoveryIntent { Strategy, Genre, Query, SeedSongID,
    SeedPlaylistID, PreferredSources }`. Strategies for this version:
    `random`, `genre`, `search`. `similar_song` and `similar_playlist`
    are declared in the enum but not implemented until v0.5.

2.  **Discogs as a second seeder.** Discogs API integration via
    Personal Access Token. Per-client rate limiter (60 req/min, token
    bucket). 30-day SQLite cache of Discogs responses. Weighted
    refiller (configurable, default 70% Bandcamp / 30% Discogs to
    de-risk the Discogs path).

3.  **Quality gate.** Floors: 192 kbps min, 2 MB min size, 200 MB max,
    peer queue ≤ 50. Codec preference flac > mp3 > other. Every
    candidate that passes or fails the gate is logged with reason.

4.  **Ladder search strategy.** Three rungs: `[catno, artist+title,
    title]`. Log per-rung hit rate. Rung 0 (catno) is on probation —
    if it doesn't earn its keep in three months of operation, drop it.

5.  **User-initiated search.** `POST /api/queue/search` takes a query,
    emits `DiscoveryIntent { Strategy: search, Query: q }`. Typo
    tolerance: lowercase + strip punctuation + collapse whitespace.
    If empty, retry with words > 4 chars only. Relies on Discogs'
    native fuzziness — no custom fuzzy matcher.

6.  **Relax-mode, split by intent origin.** Passive refill relaxes
    silently. User-initiated search surfaces the relaxation ("no
    high-quality matches; showing best available"). Never ship 128 kbps
    tracks without the user knowing.

7.  **Discovery observability.** New SQLite table `discovery_log`
    captures every attempt: source, strategy, rung, gate outcome,
    result. Never deleted. Historical data becomes valuable in v0.5.

**Explicitly not in v0.4:**

- Genre-scoped discovery (user picks preferred genres, refills bias
  toward them). Deferred to v0.4.1 because it needs a user preferences
  table and UI.
- Removing Bandcamp. Bandcamp stays. It's the reliability floor
  underneath Discogs' higher-variance catalog.
- Audio features of any kind.
- Frontend changes beyond the search endpoint wiring.

**Architectural invariants this establishes:**

- `DiscoveryIntent` is the canonical discovery event. All future
  discovery strategies extend this event rather than adding new event
  types.
- Seeders filter on `Strategy` and `Source`, not on their name in the
  bus. Adding a new seeder later is one Subscribe call and one worker.
- Discogs responses are cached before they're used. Never hit the API
  for data the cache has.

**Shape:** three PRs in order.

- **PR 1** — DiscoveryIntent rename + refiller update + outbox
  backward-compat. Pure refactor. No new behavior.
- **PR 2** — Discogs integration + gate + ladder + observability
  table + logging.
- **PR 3** — User search endpoint + typo handling + relax-mode split.

Each PR is reviewed and merged before the next is prompted to
Claude Code. No skipping.

### v0.4.1 — Genre preferences

Small release layered on v0.4.

- User preferences table with genre affinities.
- Settings UI for picking genres from a source-scoped vocabulary
  (Bandcamp tags vs Discogs genres, kept separate — no mapping yet).
- Refiller respects user's genre affinities when selecting
  `DiscoveryIntent.Genre`.

### v0.5 — Similar-to-song

The first content-based similarity feature.

**In scope:**

1.  **"Similar to this song" button.** In the player UI, next to
    shuffle/like. Clicking it queues up songs similar to the currently
    playing song. Not tied to the like state — independent action.

2.  **Metadata similarity engine** in `internal/similarity/`. Given
    a seed song, queries Discogs for:
    - Same artist, other releases (highest weight)
    - Same label, same era (±3 years) (medium weight)
    - Frequent collaborators' releases (medium weight)
    - Same Discogs "style" (sub-genre) (medium weight)
    - Same era in broader genre (lower weight)

    Candidate ranker produces a sorted list, top N go through the
    existing `RequestDownload` path for acquisition.

3.  **`DiscoveryIntent.Strategy = "similar_song"` implementation.**
    The similarity engine subscribes to this strategy on the bus.
    Emits `RequestDownload` events for chosen candidates.

**Explicitly not in v0.5:**

- Audio features (tempo, key, mood). Deferred to v0.10, and only if
  public APIs (Spotify, Discogs annotations, AcousticBrainz dump) don't
  cover the use case.
- Collaborative signals. No cross-user aggregation, ever. Even for a
  single-user Pi, this invariant holds to keep the design simple.
- Learning over time. Similarity is recomputed per request from
  metadata; nothing is trained.

**Invariants this establishes:**

- Similarity is a worker consuming `DiscoveryIntent` events with
  strategy-specific fields. Not a new service, not a new event type.
- Similarity uses cached Discogs data. Single "similar to this song"
  click should cost at most one or two Discogs API calls — the rest
  comes from the 30-day cache established in v0.4.

### v0.5.2 — Similar-to-playlist

Small extension of the v0.5 engine.

- "Find similar playlist" button next to each playlist.
- Engine aggregates the playlist's songs into a profile (top artists,
  top labels, style distribution), runs the same similarity logic on
  that profile.
- Produces a new temporary playlist the user can keep or discard.

### v0.6 — Mapping and source-aware genre vocabulary

Addresses the v0.4 "two separate genre vocabularies" decision once
there's enough real usage to know what to map.

- `genre_mapping` table: seed it with common equivalences (Bandcamp
  "minimal-house" ↔ Discogs "Electronic > Minimal").
- Grow the mapping from successful searches over time (not ML, just
  recording which source → source translations produced hits).
- Single user-facing genre vocabulary by the end.

### v0.7 — Internet-safe auth hardening

Prerequisite for any Muzika instance exposed beyond the LAN.

Muzika today is designed for LAN-only operation. The auth module is
correct but not hardened against internet-scale attack patterns. Before
the README, deploy guide, or any docs suggest port-forwarding, VPN-less
remote access, or public-facing hostnames, this milestone must ship.

**In scope:**

1.  **Rate limiting on `/api/auth/login`.** Per-IP token bucket, strict
    (e.g. 5 attempts / 5 min / IP, 10 / 5 min / username). Rejected
    requests return 429 with `Retry-After`. Bucket state in memory is
    fine — a restart resets counters, which is an acceptable DoS
    surface for a single-node system. Log every rejection at WARN.

2.  **Constant-time user lookup.** Close the remaining enumeration gap
    flagged in the Phase 4 review: the DB lookup in `Login` is not
    constant-time between "user exists" and "user doesn't." Fix by
    always doing a row-shaped SELECT, using a sentinel row if needed,
    so timing is indistinguishable between the two paths.

3.  **Stronger password policy.** Raise minimum length to 12 characters
    for new registrations. Check against a local compromised-password
    list (HaveIBeenPwned's SHA-1 prefix bloom filter works offline — no
    external calls at registration time). Existing users keep their
    current passwords until they change them; `POST /api/auth/logout-
    all` remains the revocation story.

4.  **JWT lifetime shortened + refresh token flow.** Access tokens drop
    from 24h to 30 minutes. Add refresh tokens (longer-lived, rotated
    on use, stored in `auth_refresh_tokens` with explicit revocation
    support). The token_version revocation story still works for
    nuclear logout; refresh tokens add normal re-auth.

5.  **HTTPS enforcement documentation.** The server doesn't terminate
    TLS — that's the reverse proxy's job. But the deploy docs get a
    dedicated "Exposing Muzika to the internet" section covering:
    Caddy/nginx with Let's Encrypt, `Secure` and `HttpOnly` cookie
    flags, HSTS headers, CORS tightening (no more `*`). The docs
    call out explicitly: **do not expose Muzika over HTTP.**

6.  **Security logging.** All auth events (login success/fail, logout,
    logout-all, token refresh, rate-limit rejection) log to a new
    SQLite table `auth_audit` with IP, username, result. Append-only.
    Ships with a simple CLI to tail or query it from the Pi.

7.  **Recommended deploy modes.** Deploy docs get three tiers:
    - Tier 1 (LAN only): default, no v0.7 features required.
    - Tier 2 (remote via VPN/Tailscale): LAN-equivalent, no extra
      hardening required — the VPN is the security boundary.
    - Tier 3 (direct internet via port-forward + reverse proxy): all
      v0.7 features required, explicit checklist in deploy docs.

**Explicitly not in v0.7:**

- 2FA / TOTP. Useful but not blocking. File for v0.7.1 if desired.
- Automated intrusion detection / fail2ban integration. Rate limiting
  at the app layer is enough for v0.7. Operators who want fail2ban
  can configure it independently against the logs we already emit.
- Email-based password reset. The logout-all revocation path plus
  manual password change via authenticated session is sufficient for
  the scale Muzika targets.
- Session management UI ("see all your active sessions, revoke one").
  File for later if the refresh-token flow surfaces a clear need.

**Invariants this establishes:**

- The README and deploy docs gain a clear mode distinction. No guide
  is allowed to suggest "Muzika can be exposed publicly" without also
  pointing at the Tier 3 checklist. This invariant is enforced in
  documentation review.
- Security-sensitive auth code gains an explicit "threat model"
  comment block describing what it does and does not defend against.
  Future changes to auth must update that block in the same PR.

### v0.10 — Audio features, if still wanted

Before writing a single line of DSP code, check in this order:

1.  **Spotify Web API's `/audio-features` endpoint** — free for
    non-commercial use, covers Spotify-catalog tracks, returns tempo,
    key, energy, valence, danceability, acousticness. Exactly the
    features we'd want.
2.  **Discogs release annotations** — user-contributed, inconsistent,
    but free BPM and key data for a fraction of releases.
3.  **AcousticBrainz 2022 final dump** — 24 million tracks of
    precomputed audio features, ~30 GB, free, never updated again but
    static and mirror-able.
4.  **Last.fm tags** — rough mood proxies ("chill", "uplifting",
    "dark") at the artist level.

Only if (1)–(4) combined don't cover the real catalog Muzika is
acquiring, consider local DSP computation.

If local computation becomes necessary:

- The analysis pipeline runs on your development machine, not on the
  Pi. Features are computed once per track, cached in SQLite, synced
  to the Pi.
- Expected cost: 30-120 seconds per track on a Pi 3, unacceptable; a
  few seconds per track on a modern laptop, fine.
- Accuracy is limited: ~70-90% for tempo on rhythmic music, 40-60%
  for key on tonal music, mood models are rough. Manage expectations
  by treating these as signals, not facts.

---

## What Muzika will never be

These are not "out of scope for now." These are architectural refusals.

- **Collaborative filtering.** No cross-user aggregation. Ever. Not
  even opt-in.
- **A social network.** No comments, likes-by-user-visible-to-others,
  following, sharing feeds.
- **Ad-supported.** No.
- **Cloud-primary.** Muzika is self-hosted first. The server runs on
  hardware the owner controls; data is stored on that hardware; Muzika
  does not phone home and has no dependency on any third-party or
  Muzika-operated cloud service for core functionality. Remote access
  — port forwarding, reverse proxies, VPN, Tailscale, Cloudflare
  Tunnel — is explicitly supported and is the owner's choice to
  configure. When exposing Muzika beyond the LAN, v0.7's hardening
  milestone is a prerequisite.
- **Integration with major streaming services.** No Spotify OAuth,
  no Apple Music, no YouTube Music. Muzika uses their metadata APIs
  where free (v0.10 audio features), never their playback.
- **DRM support.** Muzika plays files from disk. Files on disk are
  not protected. This is the entire point.

---

## Operational principles

- **The Pi is a deployment target, not a development environment.**
  All development happens on the dev laptop. CI cross-compiles for
  armv7 (current target OS) or arm64 (if the Pi is ever reflashed).
- **All deploys are tagged releases.** No direct pushes to the Pi.
  No scp'd binaries. Tag → CI builds → GitHub Release →
  `muzika-updater.timer` pulls. This is the deploy contract.
- **Secrets live on the Pi, encrypted in the repo.** SOPS + age.
  The age private key never leaves the Pi. The decrypted `.env` never
  enters git. This invariant holds regardless of how the rest of the
  deploy evolves.
- **Memory budget is enforced, not aspirational.** systemd
  `MemoryMax=` caps everything. If a version exceeds 580 MB total,
  that version doesn't ship to the Pi until it fits.
- **Review rhythm per PR.** No autonomous multi-phase runs without
  explicit scope limits. Each PR is reviewable in 20 minutes.

---

## When this document needs updating

- Any time a version ships with scope different from the plan above.
- Any time a new version is scoped.
- Any time an architectural invariant changes (this should be rare
  and deliberate).
- Any time a "never" becomes a "maybe later" or vice versa.

Edit this document in the same commit as the change it describes.
Review the change like code.
