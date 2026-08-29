# State

## 2026-08-29 (later) — full build-out, BK-1 through BK-13

**What exists now:** a working backup system, proven end-to-end by
`test/e2e/run.sh` against real Docker + restic + Postgres: activation
gate → backup (label discovery, contract parsing, fatal-secret gating,
exec-based pg_dump, volume capture, pairing seal, restic to local repo,
GFS prune) → drill (restore, pairing verify, ephemeral DB on the live
network, decrypt canary with restored KEK, repo check) → T0 file and T1
module restore with typed confirmation + reconciliation checklist →
evidence log. PHASES.md has per-phase status; starred items list exactly
what still needs a live B2 account, real ParadeDB image, clean-Ubuntu
rehearsal, or a human.

**Key implementation decisions this session:**
- Dumps and restores exec INSIDE the DB container (no pg client in the
  sidecar): version pairing by construction, smaller image.
- Own minimal Docker API client (stdlib, API floor v1.44) instead of the
  official SDK; hijacked-connection exec streaming with stdcopy demux.
- restic built from source at v0.18.1 (module-checksummed) and
  upx-compressed: image 33 MB vs the 60 MB BK-1 budget; CI enforces.
- "Write-only" B2 key = list/read/write, NO delete (restic needs read);
  provisioning refuses buckets without Object Lock and keys granted any
  delete capability; `VerifyCannotDelete` is the runtime red-team.
- Plan §4.2's "socket proxy CONTAINERS:1 only" is not implementable with
  the plan's own hook/dump/drill requirements — the honest endpoint
  allowlist and threat assessment live in docs/socket-proxy.md.
- Scheduler is fixed calendar (daily 01:30 backup, monthly day-1 drill,
  annual rotation, 6h B2 health), env-overridable for installers only.
- Rotation appends a pending generation; the OLD generation keeps
  backing up until the new kit passes the same activation gate.

**Environment quirks (Windows dev box):** run e2e via Git Bash
(`bash test/e2e/run.sh`); it sets MSYS_NO_PATHCONV, reads container
state via `docker cp` (scratch image has no shell), and never asks
native curl to write to /dev/null.

**Next:** BK-2b fan-out (12 module contracts, gated on the e2e-style
canary), BK-2c in Vibe-Connect, BK-10a console proxy in Vibe-Appliance,
live-B2 verification of BK-5/7, ParadeDB round-trip, and the BK-9
clean-Ubuntu rehearsal.

## 2026-08-29 — BK-0 scaffold

**Done this session:**
- `git init`, repo scaffold, CLAUDE.md, tracking files seeded from build plan §9/§10.
- Language: **Go** (stdlib only so far). Chosen because BK-1's < 60 MB image criterion plus the restic static binary effectively rule out a Node runtime, restic itself is Go, and a single static binary suits the scratch/non-root sidecar model. The rest of the suite is TypeScript/pnpm — this repo deliberately diverges.
- License: PolyForm Internal Use 1.0.0, body taken from Vibe-Appliance's LICENSE per BK-0 "license alignment with base appliance"; Definitions and Additional-terms sections adapted to Vibe Backup (restic BSD-2-Clause noted; no-warranty-of-recovery clause added). Note: Vibe-Tax-Research-Chat's LICENSE uses stock PolyForm text (with "Internal Business Use") while the appliance's has a noncompete-style "Internal Use Limitation" — divergence logged in QUESTIONS.md.
- Naming (plan §10.2): cleared. Code search of Vibe-Connect for "vault" returns zero hits; the Vaultwarden reference lives in Sentinel. "Vibe Backup" stands.
- Manifest entry: `.appliance/manifest.json` mirroring Vibe-Tax-Research-Chat's shape (schemaVersion 1, slug `vibe-backup`, image `ghcr.io/kisaesdevlab/vibe-backup`, port 4000, health/ping endpoints the scaffold binary actually serves). `userFacing: false` until BK-10. Field semantics not yet verified against the appliance's manifest loader — do that in BK-10a.
- CI: `ci.yml` (vet/test/build + single-platform Docker smoke build) and `release.yml` (multi-arch amd64+arm64 GHCR publish, tag scheme and conventions copied from Vibe-Tax-Research-Chat's release.yml, including the latest-only-from-semver-tags rule).
- Binary: `cmd/vibe-backup` serves `/api/health` and `/api/ping` and reports version; honest about being a scaffold.

**Environment notes:**
- Dev box is Windows; Go is NOT installed locally — build and test via Docker or rely on CI.
- `gh` authenticated as KisaesDevLab; Docker 29.x available.

**BK-0 exit criterion verified:** repo at https://github.com/KisaesDevLab/Vibe-Backup (public), tag v0.1.0; ci + both release runs green; `ghcr.io/kisaesdevlab/vibe-backup:latest` published multi-arch (amd64 + arm64), local image 8.2 MB.

**Next:** BK-1 — restic static binary + CA certs into the runtime image, image-size gate (< 60 MB) in CI.
