#!/usr/bin/env bash
# install.sh: first-time provisioning for a Muzika Pi.
# Run with sudo. Idempotent — safe to re-run.
#
# Provisions the muzika system user, directories, systemd units, the
# muzika-update / muzika-decrypt scripts, sops/age tooling, and an age
# keypair. Does NOT enable any services — prints a checklist the operator
# runs manually.
#
# v0.3.0 dropped the slskd sidecar: muzika is now a single Go binary that
# speaks Soulseek natively via github.com/ABCslime/gosk. Nothing else to
# install alongside it.

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "Run as root (sudo)." >&2
    exit 1
fi

MUZIKA_USER="muzika"
MUZIKA_UID=1001
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SOPS_VERSION="${SOPS_VERSION:-v3.8.1}"

# Pick sops architecture matching the running kernel. Pi 3 CPU is aarch64
# but a 32-bit Ubuntu/Raspberry Pi OS userland only loads ARMv7 ELFs.
#   aarch64 / arm64  → arm64  (64-bit userland)
#   armv7l / armv6l  → arm    (32-bit userland)
arch=$(uname -m)
case "$arch" in
    aarch64|arm64)
        SOPS_ARCH="arm64"
        ;;
    armv7l|armv6l)
        SOPS_ARCH="arm"
        ;;
    *)
        echo "unsupported architecture: $arch" >&2
        exit 1
        ;;
esac

log() {
    echo "[install] $*"
}

# ----------------------------------------------------------------------------
# 1. System user.
# ----------------------------------------------------------------------------
if ! id -u "$MUZIKA_USER" >/dev/null 2>&1; then
    log "creating system user ${MUZIKA_USER} (uid ${MUZIKA_UID})"
    adduser --system --uid "$MUZIKA_UID" --group \
        --home /srv/muzika --shell /usr/sbin/nologin "$MUZIKA_USER"
else
    log "user ${MUZIKA_USER} already exists"
fi

# ----------------------------------------------------------------------------
# 2. Directories + ownership.
# ----------------------------------------------------------------------------
log "creating directories"
install -d -m 0750 -o "$MUZIKA_USER" -g "$MUZIKA_USER" /srv/muzika
install -d -m 0750 -o "$MUZIKA_USER" -g "$MUZIKA_USER" /srv/muzika/data
install -d -m 0750 -o "$MUZIKA_USER" -g "$MUZIKA_USER" /srv/muzika/data/music
install -d -m 0750 -o "$MUZIKA_USER" -g "$MUZIKA_USER" /var/lib/muzika
install -d -m 0750 -o root             -g "$MUZIKA_USER" /etc/muzika

# ----------------------------------------------------------------------------
# 3. systemd units.
# ----------------------------------------------------------------------------
log "installing systemd units"
install -m 0644 "$REPO_ROOT/deploy/systemd/muzika.service"          /etc/systemd/system/muzika.service
install -m 0644 "$REPO_ROOT/deploy/systemd/muzika-updater.service"  /etc/systemd/system/muzika-updater.service
install -m 0644 "$REPO_ROOT/deploy/systemd/muzika-updater.timer"    /etc/systemd/system/muzika-updater.timer
systemctl daemon-reload

# ----------------------------------------------------------------------------
# 4. Updater + decrypt scripts.
# ----------------------------------------------------------------------------
log "installing updater scripts"
install -m 0755 "$REPO_ROOT/deploy/bin/muzika-update"  /usr/local/bin/muzika-update
install -m 0755 "$REPO_ROOT/deploy/bin/muzika-decrypt" /usr/local/bin/muzika-decrypt

# ----------------------------------------------------------------------------
# 5. age + sops.
# ----------------------------------------------------------------------------
if ! command -v age-keygen >/dev/null 2>&1; then
    log "installing age"
    apt-get update
    apt-get install -y age
else
    log "age already installed"
fi

if ! command -v sops >/dev/null 2>&1; then
    log "installing sops ${SOPS_VERSION} (static binary)"
    curl -fsSL -o /usr/local/bin/sops \
        "https://github.com/getsops/sops/releases/download/${SOPS_VERSION}/sops-${SOPS_VERSION}.linux.${SOPS_ARCH}"
    chmod 0755 /usr/local/bin/sops
else
    log "sops already installed"
fi

# ----------------------------------------------------------------------------
# 6. age keypair.
# ----------------------------------------------------------------------------
if [ ! -f /etc/muzika/age.key ]; then
    log "generating age keypair at /etc/muzika/age.key"
    umask 077
    age-keygen -o /etc/muzika/age.key
    chown root:root /etc/muzika/age.key
    chmod 0400 /etc/muzika/age.key
else
    log "age keypair already exists; keeping current"
fi

pub=$(grep -E '^# public key:' /etc/muzika/age.key | awk '{print $NF}')

# ----------------------------------------------------------------------------
# 7. Print checklist — intentionally do NOT start services.
# ----------------------------------------------------------------------------
cat <<EOF

=============================================================================
Muzika install complete.

Your age public key:

    ${pub}

Next steps (do these in order):

 1. On your dev machine, add the public key above to deploy/.sops.yaml as
    the creation_rules recipient, commit, and push.

 2. Create a plaintext .env from .env.example, fill in secrets, then
    encrypt:

        cp .env.example .env
        # edit .env, fill in MUZIKA_JWT_SECRET, MUZIKA_SOULSEEK_USERNAME, etc.
        sops -e .env > deploy/.env.sops
        git add deploy/.sops.yaml deploy/.env.sops
        git commit -m "deploy: seed encrypted env"
        git push
        rm .env

 3. Push a v* tag to trigger the first release build:

        git tag v0.3.0
        git push --tags

    Wait for the release workflow to finish (github.com/ABCslime/muzikaLite/actions).

 4. On this Pi, start the updater timer — it pulls the new binary on the
    next tick and decrypts the env file:

        sudo systemctl enable --now muzika-updater.timer

The muzika service starts automatically after the first updater tick
installs the binary (the updater calls systemctl restart muzika).

Logs:
    journalctl -u muzika          -f
    journalctl -u muzika-updater  -n 50

=============================================================================
EOF
