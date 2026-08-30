# Dead-Man Endpoint — Hosted Service Spec

The email alerts in `internal/notify` cover "the appliance is alive but
failing." They cannot cover the scenario that matters most — **the
appliance is dead, encrypted, or unplugged** — because a dead box sends
no email. Only an external service noticing *silence* covers that. This
spec defines the small Kisaes-hosted service that does; the sidecar side
already exists (`VIBE_BACKUP_PING_URL`).

## Privacy posture (why this doesn't violate D-2 / vendor distance)

The ping carries **no firm or client data**: an opaque appliance ID and
an implicit timestamp. Kisaes learns only "appliance X completed a
backup at time T." This does not touch the Safeguards perimeter the way
key escrow would, which is why hosting it is acceptable while hosting
the Recovery Kit never is.

## Sidecar behavior (already implemented)

- After every **successful** backup: `GET $VIBE_BACKUP_PING_URL`
  (10-second timeout, failures logged, never fatal).
- No pings on failure — silence IS the signal.

## Service contract

- **Endpoint:** `https://ping.kisaes.com/b/<appliance-id>`
  - `GET` → 200, body ignored by the sidecar. Unknown IDs are accepted
    and auto-registered as "unclaimed" (installer claims them, below).
- **Registration:** the meta-installer (or standalone installer) claims
  an appliance ID with the firm's alert email at install time:
  `POST /b/<appliance-id>/claim {email, firmName}` authenticated by the
  licensing token it already holds.
- **Alerting rule:** no ping for **26 hours** → email the claimed
  address: *"Your Vibe appliance has not completed a backup in over a
  day. If the appliance is off or unreachable, that is the problem."*
  Repeat daily until pings resume; send an all-clear on resume.
- **Escalation (optional, later):** unresolved after 7 days → notify the
  firm's MSP contact if one is registered.
- **Retention:** last-ping timestamp + alert state only. No history
  needed beyond what the alert rule requires (keep 30 days for support).

## Installer wiring

- Appliance mode: `VIBE_BACKUP_PING_URL=https://ping.kisaes.com/b/<appliance-id>`
  in the vibe-backup env file, plus the claim call. The appliance ID is
  the one already minted for licensing.
- Standalone mode: same, minted at first standalone install.
- Firms that refuse any vendor contact: leave the env var unset; the
  sidecar sends nothing anywhere. The console then shows failure alerts
  only locally (plus SMTP if configured) — the operator accepted that
  trade.

## Until the service exists

`VIBE_BACKUP_PING_URL` also works pointed at any healthchecks.io-style
monitor — an MSP can wire that today with zero code changes.
