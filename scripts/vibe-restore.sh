#!/usr/bin/env bash
# vibe-restore.sh — bare-metal recovery for the Vibe appliance (BK-9).
#
# The scenario: the appliance is dead, encrypted, or under water. You have
# a fresh Ubuntu machine and the printed Recovery Kit. Target: a restored
# host in under 60 minutes, unattended after credential entry.
#
#   curl -fsSL https://get.kisaes.com/restore | sudo bash
#
# Works for the full appliance and for standalone single-module
# deployments — everything needed is inside the snapshot.
set -euo pipefail

RESTIC_VERSION="0.18.1"
WORK=/opt/vibe-restore
STAGE="$WORK/stage"

say()  { printf '\n\033[1m%s\033[0m\n' "$*"; }
die()  { printf '\033[31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "run with sudo — Docker installation and volume restore need root"

say "Vibe Restore — bare-metal recovery"
echo "You will need the printed Recovery Kit. Everything on it can be scanned"
echo "from the QR code or typed by hand."

# ---- 1. Docker ---------------------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
  say "Installing Docker…"
  curl -fsSL https://get.docker.com | sh
fi
systemctl enable --now docker >/dev/null 2>&1 || true

# ---- 2. restic ---------------------------------------------------------------
if ! command -v restic >/dev/null 2>&1; then
  say "Installing restic…"
  ARCH=$(uname -m); case "$ARCH" in x86_64) A=amd64;; aarch64) A=arm64;; *) die "unsupported arch $ARCH";; esac
  curl -fsSL -o /tmp/restic.bz2 \
    "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_${A}.bz2"
  bunzip2 -f /tmp/restic.bz2 && install -m 0755 /tmp/restic /usr/local/bin/restic
fi

# ---- 3. Credentials from the kit --------------------------------------------
say "Enter the details from the Recovery Kit."
echo "(If you scanned the QR code, paste the JSON it contains; otherwise leave"
echo "blank and answer the prompts.)"
read -r -p "QR JSON (or Enter to type manually): " QR || true
if [ -n "${QR:-}" ]; then
  get() { echo "$QR" | tr ',' '\n' | sed -n "s/.*\"$1\":\"\{0,1\}\([^\",}]*\).*/\1/p" | head -1; }
  BUCKET=$(get bucket); PREFIX=$(get prefix); KEYID=$(get keyId); KEY=$(get key); PASSPHRASE=$(get passphrase)
else
  read -r -p  "B2 bucket name: " BUCKET
  read -r -p  "Path in bucket (e.g. gen2, printed under the bucket name): " PREFIX
  read -r -p  "Restore key ID: " KEYID
  read -r -p  "Restore key: " KEY
  read -r -sp "Repository passphrase (64 characters): " PASSPHRASE; echo
fi
[ -n "$BUCKET" ] && [ -n "$PASSPHRASE" ] || die "bucket and passphrase are required"

export RESTIC_REPOSITORY="b2:${BUCKET}:${PREFIX}"
export RESTIC_PASSWORD="$PASSPHRASE"
export B2_ACCOUNT_ID="$KEYID" B2_ACCOUNT_KEY="$KEY"

# ---- 4. Choose a snapshot ----------------------------------------------------
say "Reading backups from off-site storage…"
restic snapshots --compact || die "cannot read the repository — check the bucket name, key, and passphrase against the kit"
echo
read -r -p "Snapshot ID to restore [latest]: " SNAP
SNAP=${SNAP:-latest}

say "Restoring snapshot $SNAP (this is the long part — coffee, not panic)…"
mkdir -p "$STAGE"
restic restore "$SNAP" --target "$STAGE"

ROOT=$(find "$STAGE" -name manifest.json -exec dirname {} \; | head -1)
[ -n "$ROOT" ] || die "restored data contains no manifest.json"

# ---- 5. Verify atomic pairing BEFORE using anything -------------------------
say "Verifying snapshot integrity…"
python3 - "$ROOT" <<'PYEOF' || die "snapshot failed atomicity verification — do NOT restore from it; try an earlier snapshot"
import hashlib, json, os, sys
root = sys.argv[1]
p = json.load(open(os.path.join(root, "pairing.json")))
actual = {}
for dirpath, _, files in os.walk(root):
    for f in files:
        full = os.path.join(dirpath, f)
        rel = os.path.relpath(full, root).replace(os.sep, "/")
        if rel == "pairing.json":
            continue
        h = hashlib.sha256()
        with open(full, "rb") as fh:
            for chunk in iter(lambda: fh.read(1 << 20), b""):
                h.update(chunk)
        actual[rel] = h.hexdigest()
want = {k: v["sha256"] for k, v in p["files"].items()}
if actual != want:
    missing = set(want) - set(actual); extra = set(actual) - set(want)
    changed = {k for k in set(want) & set(actual) if want[k] != actual[k]}
    print("missing:", *sorted(missing), sep="\n  ", file=sys.stderr)
    print("unexpected:", *sorted(extra), sep="\n  ", file=sys.stderr)
    print("modified:", *sorted(changed), sep="\n  ", file=sys.stderr)
    sys.exit(1)
print("pairing verified: every artifact is from the same moment")
PYEOF

PG_IMAGE=$(python3 -c "import json,sys; print(json.load(open('$ROOT/manifest.json'))['pgImage'])")
SNAP_TIME=$(python3 -c "import json; print(json.load(open('$ROOT/manifest.json'))['createdAt'])")
echo "Snapshot taken: $SNAP_TIME"

# ---- 6. Restore host config, secrets, volumes -------------------------------
say "Restoring configuration and data volumes…"
if [ -d "$ROOT/host" ]; then
  mkdir -p /opt/vibe
  cp -a "$ROOT/host/." /opt/vibe/restored-host/
  echo "Host files (compose, env, caddy) restored to /opt/vibe/restored-host/"
fi
if [ -d "$ROOT/volumes" ]; then
  for moddir in "$ROOT/volumes"/*/; do
    mod=$(basename "$moddir")
    for voldir in "$moddir"*/; do
      vol=$(basename "$voldir")
      docker volume create "$vol" >/dev/null
      docker run --rm -v "$vol":/dst -v "$voldir":/src:ro alpine sh -c 'cp -a /src/. /dst/'
      echo "  volume $vol ($mod) restored"
    done
  done
fi

# ---- 7. Database: exact recorded image, then pg_restore ---------------------
if [ -n "$PG_IMAGE" ] && [ "$PG_IMAGE" != "None" ]; then
  say "Starting database from the EXACT image this backup was taken from:"
  echo "  $PG_IMAGE"
  docker pull "$PG_IMAGE"
  docker volume create vibe-pg-data >/dev/null
  docker rm -f vibe-restore-pg >/dev/null 2>&1 || true
  docker run -d --name vibe-restore-pg \
    -e POSTGRES_USER=vibe -e POSTGRES_PASSWORD="$(openssl rand -hex 16)" \
    -v vibe-pg-data:/var/lib/postgresql/data \
    "$PG_IMAGE" >/dev/null
  say "Waiting for the database…"
  for i in $(seq 1 60); do
    docker exec vibe-restore-pg pg_isready -U vibe >/dev/null 2>&1 && sleep 2 && break
    sleep 2
  done
  docker cp "$ROOT/globals.sql" vibe-restore-pg:/globals.sql 2>/dev/null || true
  docker exec vibe-restore-pg psql -U vibe -f /globals.sql postgres >/dev/null 2>&1 || true
  if [ -d "$ROOT/db" ]; then
    docker cp "$ROOT/db" vibe-restore-pg:/restore-db
    for dump in "$ROOT/db"/*.dump; do
      db=$(basename "$dump" .dump)
      say "Restoring database: $db"
      docker exec vibe-restore-pg pg_restore -U vibe --create --dbname postgres "/restore-db/$(basename "$dump")" \
        || die "pg_restore failed for $db"
    done
  fi
fi

# ---- 8. What's next ---------------------------------------------------------
say "Data restore complete."
cat <<TXT

Restored so far:
  - all data volumes, host config, and module secrets   → /opt/vibe/restored-host
  - every database, into container 'vibe-restore-pg' running $PG_IMAGE

To finish bringing the appliance up:
  1. Re-run the Vibe installer:  curl -fsSL https://get.kisaes.com | sudo bash
     — point it at /opt/vibe/restored-host for existing config; it will
     re-pull the module images recorded in the snapshot manifest and
     reattach the restored volumes and database.
  2. Licensing re-activates from the restored state; if it complains,
     the licensing service can re-issue against your firm record.

Post-restore checklist (external services do NOT roll back with data):
TXT
python3 - "$ROOT" <<'PYEOF'
import json, sys
man = json.load(open(sys.argv[1] + "/manifest.json"))
for m in man.get("modules", []):
    for a in m.get("reauthRequiredAfterRestore") or []:
        print(f"  [ ] {m['name']}: {a}")
    for e in m.get("externalState") or []:
        print(f"  [ ] {m['name']}: verify external state {e!r} against the restored data")
print(f"  [ ] Review work done after {man['createdAt']} — it is not in this restore.")
PYEOF
echo
echo "The snapshot's secrets live under: $ROOT/secrets/  (module env values)."
echo "This machine now holds decrypted firm data — treat it as the appliance."
