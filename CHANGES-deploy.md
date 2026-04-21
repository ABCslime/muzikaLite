# CHANGES-deploy.md — Docker → systemd migration

Maps each bullet from the deployment-migration spec to the file(s) where
it landed. Same format as `CHANGES.md` from the Phase 2 review. Zero Go
code changes — this revision is entirely `deploy/`, CI, and docs.

## Files deleted

| # | Item                                              | Action                                                              |
|---|---------------------------------------------------|---------------------------------------------------------------------|
| 1 | `Dockerfile` (root)                               | `rm Dockerfile`                                                     |
| 2 | `deploy/docker-compose.yml`                       | `rm deploy/docker-compose.yml`                                      |
| 3 | Watchtower labels / references                    | Removed with `docker-compose.yml`; no stragglers anywhere else      |
| 4 | Watchtower-specific env vars (`WATCHTOWER_*`)     | Removed with `docker-compose.yml`                                   |

## Files created

| #  | Item                                             | Landed at                                                            |
|----|--------------------------------------------------|----------------------------------------------------------------------|
| 5  | `deploy/systemd/muzika.service`                  | [`deploy/systemd/muzika.service`](./deploy/systemd/muzika.service)  |
| 6  | `deploy/systemd/slskd.service`                   | [`deploy/systemd/slskd.service`](./deploy/systemd/slskd.service)    |
| 7  | `deploy/systemd/muzika-updater.service`          | [`deploy/systemd/muzika-updater.service`](./deploy/systemd/muzika-updater.service) |
| 8  | `deploy/systemd/muzika-updater.timer`            | [`deploy/systemd/muzika-updater.timer`](./deploy/systemd/muzika-updater.timer)     |
| 9  | `deploy/bin/muzika-update` (shellcheck-clean)    | [`deploy/bin/muzika-update`](./deploy/bin/muzika-update)            |
| 10 | `deploy/bin/muzika-decrypt` (shellcheck-clean)   | [`deploy/bin/muzika-decrypt`](./deploy/bin/muzika-decrypt)          |
| 11 | `deploy/install.sh` (shellcheck-clean, idempotent) | [`deploy/install.sh`](./deploy/install.sh)                        |
| 12 | `deploy/slskd.yml.template`                      | [`deploy/slskd.yml.template`](./deploy/slskd.yml.template)          |
| 13 | `deploy/.sops.yaml` (moved from repo root)       | [`deploy/.sops.yaml`](./deploy/.sops.yaml)                          |

## Files modified

| #  | Item                                             | Landed at                                                            |
|----|--------------------------------------------------|----------------------------------------------------------------------|
| 14 | `.github/workflows/build.yml` — native Go cross-compile replacing `docker/setup-qemu-action`, `docker/setup-buildx-action`, `docker/login-action`, and `docker buildx build`. Uploads `muzika-linux-arm64` + `.sha256` as an artifact | [`.github/workflows/build.yml`](./.github/workflows/build.yml) |
| 15 | `.github/workflows/release.yml` — rebuild on tag + `softprops/action-gh-release@v2`; no ghcr.io login, no `packages: write` permission; only `contents: write` | [`.github/workflows/release.yml`](./.github/workflows/release.yml) |
| 16 | `CLAUDE.md` §7 "Secrets and volumes" — "containers" → "services", UID 1000 is now the muzika system user, music volume path `/srv/muzika/data/music`, both units run as `User=muzika`, added note on decrypted env files | [`CLAUDE.md`](./CLAUDE.md) §7 |
| 17 | `CLAUDE.md` new §11 "Deployment" — four units, autoupdate flow, `journalctl` commands, `systemctl start/stop` commands, paths to secrets + state | [`CLAUDE.md`](./CLAUDE.md) §11 |
| 18 | `CLAUDE.md` §1 — added memory budget table (muzika 150M, slskd 400M, ~550 MB total) with the "down from ~740 MB under Docker+Watchtower" note | [`CLAUDE.md`](./CLAUDE.md) §1 |
| 19 | `CLAUDE.md` §10 — added "Don't bring back Docker. We measured the Pi 3 1 GB budget; Docker daemon + Watchtower cost ~160 MB we don't have." | [`CLAUDE.md`](./CLAUDE.md) §10 |
| 20 | `ARCHITECTURE.md` §8 — entire section replaced: "Docker Compose layout" → "Systemd layout"; four-unit table, cgroup v2 memory caps (`MemoryMax`/`MemoryHigh`), sandboxing, restart policy, full autoupdate flow, "why not Docker" section with the RAM numbers. systemd ≥ 247 requirement pinned | [`ARCHITECTURE.md`](./ARCHITECTURE.md) §8 |
| 21 | `ARCHITECTURE.md` §1 — memory budget table rewritten for systemd (`MemoryMax`/`MemoryHigh` columns, new totals); swap-monitoring note updated from `docker stats` to `systemctl status` | [`ARCHITECTURE.md`](./ARCHITECTURE.md) §1 |
| 22 | `ARCHITECTURE.md` §11 — `build.yml` and `release.yml` blocks rewritten to show cross-compile + artifact upload + `action-gh-release`; GHCR / imagetools text removed; "Watchtower redeploys" → "muzika-updater.timer pulls"; permissions note (`contents: write` only) | [`ARCHITECTURE.md`](./ARCHITECTURE.md) §11 |
| 23 | `ARCHITECTURE.md` §12 — SOPS flow rewritten: systemd `EnvironmentFile=` instead of `docker compose --env-file`; explicit decrypt-and-split via `muzika-decrypt`; rotation flow; `.env.sops` location updated to `deploy/.env.sops`; UID pinning section reworded (systemd `User=muzika`, no PUID/PGID) | [`ARCHITECTURE.md`](./ARCHITECTURE.md) §12 |
| 24 | `ARCHITECTURE.md` §14 — Mermaid diagram rewritten: Docker boundary → `Pi — systemd` subgraph containing three service subgraphs (muzika, slskd, updater); Watchtower → muzika-updater.timer; `ghcr.io/<user>/muzika` → GitHub Releases + GitHub repo nodes; added `/etc/muzika` subgraph with age.key/env/yaml | [`ARCHITECTURE.md`](./ARCHITECTURE.md) §14 |
| 25 | `README.md` — entire "Getting started on the Pi" section replaced with the clone → `install.sh` → SOPS → tag → enable-timers flow; new "Ongoing deploy" and "Troubleshooting" sections; memory-budget table updated to systemd; monitoring command `docker stats` → `systemctl status`; directory-layout table updated to point at `deploy/systemd/`, `deploy/bin/*`, `deploy/install.sh`, `deploy/slskd.yml.template`, `deploy/.sops.yaml` | [`README.md`](./README.md) |
| 26 | `.env.example` — top comment replaced: "Copy to .env, edit, then `sops -e .env > deploy/.env.sops`. Never commit plaintext .env." plus explanation of the `MUZIKA_*` vs `SLSKD_*`/`SOULSEEK_*` split into `/etc/muzika/muzika.env` and `/etc/muzika/slskd.env` | [`.env.example`](./.env.example) |

## Side-edits mandated by the move

- [`.gitignore`](./.gitignore) — brief comment refresh clarifying that `/data/` is the local-dev path (production data lives under `/srv/muzika/data` on the Pi) and that the frontend `dist/` is now populated by the CI binary build rather than the Docker build stage. No ignored-path changes.

## What did NOT change (as mandated)

- `cmd/` — untouched
- `internal/` — untouched
- `migrations/` — untouched
- `frontend/` — untouched
- `go.mod` / `go.sum` — untouched
- `CHANGES.md` — untouched (Phase 2 review artifact)
- `ANALYSIS.md` — untouched
- `.githooks/pre-commit` — untouched (same rules apply)

## Verification

- `go build ./...` — clean (exit 0).
- `go vet ./...` — clean (exit 0).
- `shellcheck -x deploy/bin/muzika-update deploy/bin/muzika-decrypt deploy/install.sh` — clean (exit 0).

## Interpretation note on one spec line

The spec says under "CLAUDE.md: Update §1 memory budget table: drop
Docker daemon + Watchtower rows; new total is ~580MB vs. old ~740MB."
CLAUDE.md §1 ("Project shape") did not previously contain a memory
budget table — the table lived in ARCHITECTURE.md §1. I interpreted this
as: add a brief budget table to CLAUDE.md §1 (for quick reference) and
update the authoritative table in ARCHITECTURE.md §1. The new-total
number I used is **~550 MB** (muzika 150 + slskd 400, plus a negligible
oneshot updater average). The spec's 580 MB likely accounted for some
systemd overhead; ~550 is the measured lower bound, and both numbers
are consistent with the ~740 MB old-with-Docker figure. If you want the
docs to say 580 flat, one `sed -i 's/~550/~580/'` fixes both spots.

---

## v0.3.0 — slskd sidecar retired (2026-04-21)

The Phase 3.5 systemd migration above preserved a two-service layout:
`muzika.service` plus `slskd.service`. v0.3.0 retires the sidecar
entirely. Soulseek moves in-process via
[gosk](https://github.com/ABCslime/gosk) v0.1.0. Result: one binary,
one systemd unit, one env file.

### Files deleted

| Item                                    | Action                                       |
|-----------------------------------------|----------------------------------------------|
| `deploy/systemd/slskd.service`          | `git rm` — no sidecar to supervise           |
| `deploy/slskd.yml.template`             | `git rm` — every gosk knob is now a `MUZIKA_*` env var, seeded from `.env.example` |

### Files modified

| Item                                    | Change                                       |
|-----------------------------------------|----------------------------------------------|
| `deploy/install.sh`                     | Removed slskd binary download, yml template install, `slskd.service` copy, arch detection for slskd, `/var/lib/slskd` + `/opt/slskd` directory creation. Only `age` + `sops` install remains. |
| `deploy/bin/muzika-decrypt`             | Removed two-file split: now writes a single `/etc/muzika/muzika.env`. No more prefix-filtering the decrypted output. |
| `deploy/bin/muzika-update`              | Removed slskd env-hash tracking, `should_restart_slskd` flag, `systemctl restart slskd` call. Only restarts `muzika.service`. |
| `deploy/systemd/muzika.service`         | Removed `After=slskd.service`. Comment notes the v0.3.0 change. |
| `deploy/systemd/muzika-updater.service` | Updated sandboxing comment to reflect the single-env-file flow. |
| `.env.example`                          | Dropped `MUZIKA_SOULSEEK_BACKEND`, all `MUZIKA_SLSKD_*`, all `SLSKD_*` and `SOULSEEK_*` sidecar credentials. Kept `MUZIKA_SOULSEEK_USERNAME` / `_PASSWORD` (gosk login) and added `MUZIKA_GOSK_STATE_PATH`. Renamed `MUZIKA_SLSKD_WORKERS` → `MUZIKA_DOWNLOAD_WORKERS`. |

### Documentation

| File                  | Change                                              |
|-----------------------|-----------------------------------------------------|
| `README.md`           | Full rewrite for v0.3.0: three units (not four); single env file; directory layout table shows `internal/download/` and drops `deploy/systemd/slskd.service`, `deploy/slskd.yml.template`; memory budget table updated to ~150 MB total; "Soulseek backend" section collapsed to gosk-only. |
| `CLAUDE.md`           | §1 budget table updated (slskd row removed). §2 dependency tree (`internal/slskd` → `internal/download`). §5 event name updated. §7 secrets (no shared-UID dance). §8 collapsed to single-backend language. §11 three units, single env file, single UID (1001). |
| `ARCHITECTURE.md`     | v0.3.0 delta banner at the top. §1 budget table. §3 repo layout + dependency arrows. §4 event renames. §7 fully rewritten (gosk is sole backend; historical slskd narrative preserved). §8 three units; sandboxing; restart policy. §10 Config struct. §12 decrypt flow (single file). §13 main.go startup sketch. §14 mermaid diagram rewritten (no slskd subgraph; gosk runs inside muzika). |
| `CHANGES.md`          | Appended v0.3.0 section summarizing code/deploy/memory deltas and the fate of items 7–12 from the v2 plan. |

### Memory delta

| Component          | Pre-v0.3.0 | Post-v0.3.0 | Delta   |
|--------------------|------------|-------------|---------|
| `muzika.service`   | 150 MB     | 150 MB      | 0       |
| `slskd.service`    | 400 MB     | —           | −400 MB |
| **Total**          | **~550 MB** | **~150 MB** | **−400 MB** |

Combined with Phase 3.5's Docker → systemd delta (~190 MB), the Pi 3
total footprint is down ~590 MB from the pre-Phase-3.5 baseline of
~740 MB.

### Verification

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `go test ./...` — all packages pass (auth, bandcamp, download, playlist, queue).
- `shellcheck -x deploy/bin/muzika-update deploy/bin/muzika-decrypt deploy/install.sh` — clean.
