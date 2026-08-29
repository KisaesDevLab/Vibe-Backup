#!/usr/bin/env bash
# End-to-end test of the whole loop against real Docker, restic, and
# Postgres: setup (local-only) → activation gate → backup → drill with
# decrypt canary → T0 file restore → T1 module restore.
#
# Usage: bash test/e2e/run.sh   (needs Docker; builds vibe-backup:dev)
set -euo pipefail
cd "$(dirname "$0")/../.."

# Git Bash on Windows rewrites /paths in args to C:/... — disable that.
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'

NET=vb-e2e-net
PG=vb-e2e-pg
MOD=vb-e2e-mod
VB=vb-e2e-backup
PORT=4400
API="http://localhost:$PORT/api"
PASS=0

cleanup() {
  docker rm -f "$PG" "$MOD" "$VB" vibe-backup-drill-pg >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  docker volume rm vb-e2e-data vb-e2e-state vb-e2e-repo >/dev/null 2>&1 || true
  if [ "$PASS" = "1" ]; then echo "E2E: ALL CHECKS PASSED"; else echo "E2E: FAILED"; fi
}
[ "${KEEP:-0}" = "1" ] || trap cleanup EXIT

fail() { echo "E2E FAIL: $*" >&2; exit 1; }
step() { echo; echo "=== $* ==="; }
TMP=test/e2e/.tmp; rm -rf "$TMP"; mkdir -p "$TMP"
# status <method> <url> [data] -> prints HTTP code (native curl can't
# write to /dev/null on Windows once path conversion is off)
status() { curl -s -o "$TMP/out" -w '%{http_code}' -X "$1" "$2" -H 'Content-Type: application/json' ${3:+-d "$3"}; }

step "build image"
docker build -q --target runtime -t vibe-backup:dev . >/dev/null

step "start stack (postgres + fake module)"
docker network create "$NET" >/dev/null
docker volume create vb-e2e-data >/dev/null
docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_USER=vibe -e POSTGRES_PASSWORD=pgpass \
  postgres:16-alpine >/dev/null
docker run -d --name "$MOD" --network "$NET" \
  -e FAKE_KEK=kek-value-12345 \
  -v vb-e2e-data:/data \
  -l vibe.backup.contract=/contract.yaml \
  postgres:16-alpine sleep infinity >/dev/null
docker cp test/e2e/contract.yaml "$MOD":/contract.yaml
docker cp test/e2e/hooks "$MOD":/hooks
docker exec "$MOD" chmod +x /hooks/pre.sh /hooks/post.sh /hooks/canary.sh
docker exec "$MOD" sh -c 'echo "workpaper-content" > /data/workpaper.txt'

step "seed database with sentinel row"
for i in $(seq 1 30); do
  docker exec "$PG" pg_isready -U vibe >/dev/null 2>&1 && break; sleep 1
done
docker exec "$PG" psql -U vibe -c "CREATE DATABASE fake_db" >/dev/null
docker exec "$PG" psql -U vibe -d fake_db -c \
  "CREATE TABLE sentinel (id int primary key, payload text); \
   INSERT INTO sentinel VALUES (1, 'enc:kek-value-12345'), (2, 'client-row-original')" >/dev/null

step "start vibe-backup sidecar"
docker volume create vb-e2e-state >/dev/null
docker volume create vb-e2e-repo >/dev/null
docker run -d --name "$VB" --network "$NET" -p "$PORT":4000 \
  -u 0 \
  -v //var/run/docker.sock:/var/run/docker.sock \
  -v vb-e2e-state:/var/lib/vibe-backup \
  -v vb-e2e-repo:/repo \
  -e VIBE_BACKUP_LOCAL_REPO=/repo \
  -e VIBE_BACKUP_PG_CONTAINER="$PG" \
  vibe-backup:dev >/dev/null
for i in $(seq 1 30); do
  curl -fsS "$API/ping" >/dev/null 2>&1 && break; sleep 1
done
curl -fsS "$API/ping" >/dev/null || fail "sidecar did not come up"

step "backup before activation must be refused (BK-6)"
CODE=$(status POST "$API/backup/run" '{}')
[ "$CODE" = "500" ] || fail "expected refusal before gate, got HTTP $CODE"

step "setup (local-only) and kit download"
curl -fsS -X POST "$API/setup" -H 'Content-Type: application/json' \
  -d '{"firmName":"E2E Test Firm"}' >/dev/null || fail "setup"
curl -fsS -o "$TMP/kit.pdf" "$API/kit.pdf" || fail "kit download"
head -c 4 "$TMP/kit.pdf" | grep -q '%PDF' || fail "kit is not a PDF"

step "activation gate: wrong code refused, printed code accepted"
CODE=$(status POST "$API/kit/confirm" '{"code":"wrong words here totally bogus code"}')
[ "$CODE" = "400" ] || fail "wrong code accepted"
# The runtime image is scratch (no shell), so read state via docker cp.
docker cp "$VB":/var/lib/vibe-backup/state.json "$TMP/state.json" >/dev/null
WORDS=$(sed -n 's/.*"verificationCode": "\([^"]*\)".*/\1/p' "$TMP/state.json")
[ -n "$WORDS" ] || fail "no verification code in state"
curl -fsS -X POST "$API/kit/confirm" -H 'Content-Type: application/json' \
  -d "{\"code\":\"$WORDS\",\"custodians\":[\"Pat\",\"Sam\"]}" >/dev/null || fail "confirm"

step "run backup"
OUT=$(curl -fsS -X POST "$API/backup/run" -d '{}') || fail "backup run: $OUT"
echo "$OUT" | grep -q '"ok":true' || fail "backup not ok: $OUT"
echo "$OUT" | grep -q 'local' || fail "local destination missing: $OUT"

step "run drill — decrypt canary must pass (BK-8)"
OUT=$(curl -fsS -X POST "$API/drill/run" -d '{}') || fail "drill run: $OUT"
echo "$OUT" | grep -q '"ok":true' || fail "drill not ok: $OUT"
echo "$OUT" | grep -q '"vibe-fake":"pass"' || fail "canary did not pass: $OUT"

step "status shows verified restore (warn: local-only always warns by design)"
OUT=$(curl -fsS "$API/status")
echo "$OUT" | grep -q '"level":"warn"' || fail "expected warn level: $OUT"
echo "$OUT" | grep -q '"lastVerifiedRestore":"just now"' || fail "headline metric wrong: $OUT"
echo "$OUT" | grep -q 'off-site' || fail "local-only warning missing: $OUT"

step "T0: restore a single file from the snapshot"
SNAP=$(curl -fsS "$API/snapshots" | sed -n 's/.*"short_id":"\([^"]*\)".*/\1/p' | head -1)
[ -n "$SNAP" ] || fail "no snapshot id"
curl -fsS -o test/e2e/.tmp/file "$API/restore/file?snapshot=$SNAP&path=volumes/vibe-fake/vb-e2e-data/workpaper.txt" \
  || fail "T0 restore"
grep -q "workpaper-content" test/e2e/.tmp/file || fail "T0 content mismatch"

step "T1: corrupt live data, restore the module, verify rollback"
docker exec "$PG" psql -U vibe -d fake_db -c \
  "UPDATE sentinel SET payload='RANSOMWARE' WHERE id=2" >/dev/null
CODE=$(status POST "$API/restore/module" "{\"snapshot\":\"$SNAP\",\"module\":\"vibe-fake\",\"confirm\":\"wrong-name\"}")
[ "$CODE" = "400" ] || fail "T1 must demand the typed module name"
OUT=$(curl -fsS -X POST "$API/restore/module" -H 'Content-Type: application/json' \
  -d "{\"snapshot\":\"$SNAP\",\"module\":\"vibe-fake\",\"confirm\":\"vibe-fake\"}") || fail "T1 restore: $OUT"
echo "$OUT" | grep -q "Reconnect fake external service" || fail "checklist missing: $OUT"
VAL=$(docker exec "$PG" psql -U vibe -d fake_db -At -c "SELECT payload FROM sentinel WHERE id=2")
[ "$VAL" = "client-row-original" ] || fail "T1 did not roll data back (got: $VAL)"

step "evidence log recorded the story"
curl -fsS "$API/evidence" | grep -q '"type":"drill"' || fail "no drill evidence"

PASS=1
