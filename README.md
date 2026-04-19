# Muzika

Personal music platform — single Go binary, SQLite, Vue 3 SPA, Soulseek +
Bandcamp discovery. Replaces five Java/Spring services that previously ran
on Kubernetes. Runs on a Raspberry Pi 3 under systemd — **no Docker**.

See [`ARCHITECTURE.md`](./ARCHITECTURE.md) for the full design and
[`CLAUDE.md`](./CLAUDE.md) for working conventions.

**Status:** Phase 3 scaffold. Modules are stubs; business logic ports
land module-by-module in Phases 4–9.

**Frontend deferred.** The Vue SPA is not yet in the repo — it's being
brought in after the `auth`, `playlist`, and `queue` modules are
ported. Until then, the backend binary serves a minimal placeholder
page at `/`, and every endpoint under `/api/*` returns
`ErrNotImplemented` until its module lands. CI workflows detect the
missing `frontend/` and skip the Node build steps automatically.

---

## Quick start (local dev)

```sh
git clone git@github.com:macabc/muzika.git
cd muzika
git config core.hooksPath .githooks        # enable .env pre-commit guard

# Backend
cp .env.example .env                        # fill in JWT_SECRET, SLSKD_*
go mod tidy
go run ./cmd/muzika

# Frontend (separate terminal)
cd frontend
npm install
npm run dev                                 # :3000, proxies /api/* to :8080
```

Hit `http://localhost:3000`. The Go binary serves nothing at `/` in dev —
Vite's dev server does, and proxies API traffic to Go on :8080.

---

## Getting started on the Pi

Deployment is bare-metal systemd. Four units, one auto-updater that polls
GitHub Releases for new `muzika-linux-arm64` binaries every 5 min.

### 1. Clone to /srv/muzika/repo

```sh
sudo mkdir -p /srv/muzika
sudo chown "$USER:" /srv/muzika
git clone https://github.com/macabc/muzika.git /srv/muzika/repo
```

### 2. Run the installer

```sh
cd /srv/muzika/repo
sudo deploy/install.sh
```

`install.sh` is idempotent. It creates the `muzika` system user (UID
1000), all required directories (`/srv/muzika/data`, `/var/lib/muzika`,
`/var/lib/slskd`, `/etc/muzika`, `/opt/slskd`), installs the systemd
units, downloads the latest slskd ARM64 release into `/opt/slskd`,
installs `age` and `sops`, and generates an age keypair at
`/etc/muzika/age.key`. It prints the **age public key** at the end.

It does **not** start any service. The rest of the steps are yours.

### 3. Wire up SOPS

On your dev machine, paste the public key from step 2 into
[`deploy/.sops.yaml`](./deploy/.sops.yaml):

```yaml
creation_rules:
  - path_regex: \.env\.sops$
    age: age1abc...xyz      # the public key from the Pi
```

Commit and push.

### 4. Encrypt your env

```sh
cp .env.example .env
$EDITOR .env                        # fill in JWT_SECRET, SLSKD_*, SOULSEEK_*
sops -e .env > deploy/.env.sops
git add deploy/.sops.yaml deploy/.env.sops
git commit -m "deploy: seed encrypted env"
git push
rm .env                             # do NOT commit plaintext
```

### 5. Tag the first release

```sh
git tag v0.1.0
git push --tags
```

The `release.yml` workflow builds `muzika-linux-arm64` and publishes a
GitHub Release. Wait for it to finish
(<https://github.com/macabc/muzika/actions>).

### 6. Enable services on the Pi

```sh
sudo systemctl enable --now muzika-updater.timer   # triggers the first pull
sudo systemctl enable --now slskd.service          # starts slskd
```

The updater fires 2 min after boot and every 5 min thereafter. On its
first tick it pulls the v0.1.0 binary from the GitHub Release, decrypts
`deploy/.env.sops` into `/etc/muzika/muzika.env` and
`/etc/muzika/slskd.env`, and starts muzika. Visit `http://<pi>:8080`.

---

## Ongoing deploy

Push to `main` → CI builds and uploads `muzika-linux-arm64` as a workflow
artifact. **Nothing deploys.**

Push a `v*` tag → `release.yml` builds and creates a GitHub Release. The
Pi's `muzika-updater.timer` picks it up within 5 min, verifies the
sha256, atomic-replaces `/usr/local/bin/muzika`, and `systemctl restart
muzika`.

The "ship it" signal is the tag push — no `:latest` churn on every
commit.

Secrets updates propagate the same way: edit `.env`, `sops -e > deploy/.env.sops`,
commit, push. The next updater tick re-pulls the repo, re-decrypts, sees
the env files changed (sha256 diff), and restarts muzika. No new binary
required.

---

## Memory and swap (Pi 3 1 GB)

Steady-state budget (see `deploy/systemd/*.service`):

| Unit                      | `MemoryMax` |
|---------------------------|-------------|
| `muzika.service`          | 150 MB      |
| `slskd.service`           | 400 MB      |
| `muzika-updater.service`  | — (oneshot) |
| **Total**                 | **~550 MB** |

Down from ~740 MB when this ran under Docker + Watchtower — the Docker
daemon alone cost ~130 MB.

If you hit OOMs, add swap — **on a USB stick, not the SD card**. SD swap
will destroy the card in weeks.

```sh
# Plug in a USB stick (>= 4 GB), identify it (e.g. /dev/sda1), format ext4
sudo mkfs.ext4 /dev/sda1
sudo mkdir /mnt/usbswap && sudo mount /dev/sda1 /mnt/usbswap
sudo fallocate -l 2G /mnt/usbswap/swapfile
sudo chmod 600 /mnt/usbswap/swapfile
sudo mkswap /mnt/usbswap/swapfile
sudo swapon /mnt/usbswap/swapfile

# Persist across reboots (fstab):
echo '/mnt/usbswap/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab

# Discourage aggressive swapping:
echo 'vm.swappiness=10' | sudo tee /etc/sysctl.d/99-swap.conf
sudo sysctl -p /etc/sysctl.d/99-swap.conf

# Turn off zram if present (duplicates work):
sudo systemctl disable --now zramswap.service 2>/dev/null || true

# Monitor:
free -h
systemctl status muzika.service slskd.service
```

If you still OOM after this, the real fix is bigger hardware, not more
swap.

---

## Troubleshooting

**muzika won't start.** Read the last 50 lines of the journal:
```sh
journalctl -u muzika -n 50
```
muzika's slog output is JSON on stdout — all of it ends up here. Common
causes: missing env vars (first line of startup logs names the missing
key), DB path not writable, port 8080 already bound.

**The updater isn't pulling new releases.**
```sh
systemctl status muzika-updater.timer
systemctl list-timers muzika-updater.timer
journalctl -u muzika-updater -n 100
```
If the timer isn't active, `sudo systemctl enable --now muzika-updater.timer`.
If the GitHub API is returning 403 (rate-limited), the log will say so and
the next tick retries — this is benign.

**Force an immediate update check:**
```sh
sudo systemctl start muzika-updater.service
journalctl -u muzika-updater -f
```

**Halt autoupdates temporarily:**
```sh
sudo systemctl stop muzika-updater.timer
```

**slskd can't write to /srv/muzika/data/music.** Check ownership:
```sh
ls -la /srv/muzika/data
# should show `muzika muzika` on everything
sudo chown -R muzika:muzika /srv/muzika/data
```
Both services run as the `muzika` user — UID mismatch is the usual
cause of "file exists but empty" issues.

**"sops: no recipient matches" on the Pi.** The `/etc/muzika/age.key`
on the Pi doesn't match the public key in `deploy/.sops.yaml`. Regenerate
the keypair (`age-keygen`), update `deploy/.sops.yaml`, run `sops
updatekeys deploy/.env.sops`, commit.

---

## Soulseek backend

Two implementations behind `internal/soulseek.Client`, selected by
`MUZIKA_SOULSEEK_BACKEND`:

- `slskd` (default) — talks HTTP to the [slskd daemon](https://github.com/slskd/slskd)
  running as a systemd sidecar. **This is what ships and what you should
  use.**
- `native` — [gosk](https://github.com/macabc/gosk) (separate Go module,
  not in this repo). Returns `ErrNotImplemented` in v1; muzika refuses
  to start. See `ARCHITECTURE.md` §7 for the gosk roadmap.

To use slskd you need a Soulseek network account (register free at
<https://www.slsknet.org/>). Set `SOULSEEK_USERNAME` / `SOULSEEK_PASSWORD`
in your plaintext `.env` before encrypting.

---

## Directory layout

| Path                               | What                                                    |
|------------------------------------|---------------------------------------------------------|
| `cmd/muzika/main.go`               | Process entry point; wires everything                   |
| `internal/auth/`                   | Identity, JWT, registration                             |
| `internal/playlist/`               | Playlists + "Liked" system playlist                     |
| `internal/queue/`                  | Per-user queue, song catalog, audio byte serving        |
| `internal/bandcamp/`               | Bandcamp discovery scrape                               |
| `internal/slskd/`                  | RequestSlskdSong worker; wraps `internal/soulseek`      |
| `internal/soulseek/`               | Backend interface + slskd impl + native (stub) impl     |
| `internal/bus/`                    | In-process event bus + transactional outbox            |
| `internal/db/`                     | SQLite open + pragmas + migrations runner               |
| `internal/httpx/`                  | Middleware (auth, CORS, logging), errors, userctx       |
| `internal/config/`                 | envconfig-driven Config                                 |
| `internal/web/`                    | Embeds `frontend/dist/` via `//go:embed`                |
| `migrations/`                      | One linear stream of SQL migrations                     |
| `frontend/`                        | Vue 3 SPA (copied unchanged from old repo)              |
| `deploy/systemd/`                  | The four systemd units                                  |
| `deploy/bin/muzika-update`         | Updater script (timer target)                           |
| `deploy/bin/muzika-decrypt`        | SOPS decrypt + split into service env files             |
| `deploy/install.sh`                | One-shot Pi provisioning script                         |
| `deploy/slskd.yml.template`        | slskd config template (seeded to `/etc/muzika/slskd.yml`) |
| `deploy/.sops.yaml`                | SOPS recipient config                                   |
| `deploy/.env.sops`                 | Encrypted env (committed)                               |
| `.github/workflows/`               | `build.yml` (every push) + `release.yml` (v* tags)      |

---

## Migration phases

- [x] **Phase 1** — Analysis (see `ANALYSIS.md`)
- [x] **Phase 2** — Architecture (see `ARCHITECTURE.md`, `CHANGES.md`)
- [x] **Phase 3** — Scaffold
- [x] **Phase 3.5** — Docker → systemd deployment migration (see `CHANGES-deploy.md`)
- [ ] **Phase 4** — Port auth module
- [ ] **Phase 5** — Port playlist module
- [ ] **Phase 6** — Port queue module
- [ ] **Phase 7** — Port bandcamp module
- [ ] **Phase 8** — Port slskd module (against slskd backend)
- [ ] **Phase 9** — Frontend integration + smoke test on the Pi
- [ ] **Later** — gosk v1 in a separate repo

---

## License

Personal project. Not licensed for redistribution.
