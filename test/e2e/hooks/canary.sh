#!/bin/sh
# Decrypt round-trip canary (§4.2): prove the RESTORED sentinel row
# decrypts under the RESTORED key material. "enc:<kek>" stands in for a
# real ciphertext; the check fails if either half came from a different
# moment than the other.
set -e
[ "$VIBE_DRILL" = "1" ] || { echo "canary: not in drill mode" >&2; exit 2; }
[ -n "$VIBE_DRILL_SECRET_FAKE_KEK" ] || { echo "canary: no restored KEK" >&2; exit 2; }
VAL=$(PGPASSWORD="$VIBE_DRILL_PG_PASSWORD" psql -h "$VIBE_DRILL_PG_HOST" -p "$VIBE_DRILL_PG_PORT" \
  -U "$VIBE_DRILL_PG_USER" -d fake_db -At -c "SELECT payload FROM sentinel WHERE id=1")
if [ "$VAL" != "enc:$VIBE_DRILL_SECRET_FAKE_KEK" ]; then
  echo "canary: decrypt mismatch — restored data does not pair with restored key (got: $VAL)" >&2
  exit 1
fi
echo "canary: decrypt round-trip ok"
