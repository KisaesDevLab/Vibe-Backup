# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state

Working system, BK-1..BK-13 implemented (see `PHASES.md` for honest per-phase status — starred entries name what still needs live B2 / real ParadeDB / real hardware / a human). `vibe-backup-BUILD-PLAN.md` is the authoritative design (v2) — read it before substantive work. `STATE.md` is the session log (update both when finishing work); `QUESTIONS.md` holds open decisions for the user — surface new ones there rather than deciding them. Conventional commits throughout.

## Commands

Go is **not installed on this Windows dev box** — build and test through Docker, or lean on CI. Cache volumes `vb-gomod`/`vb-gocache` exist to speed the loop:

- Tests: `docker run --rm -v "${PWD}:/src" -w /src -v vb-gomod:/go/pkg/mod -v vb-gocache:/root/.cache/go-build golang:1.25-alpine sh -c "go vet ./... && go test ./..."` (single test: `go test -run TestName ./internal/<pkg>/`)
- Image: `docker build --target runtime -t vibe-backup:dev .` — multi-stage; final stage `scratch`, non-root, must stay **< 60 MB** (CI gates it; restic is upx-compressed)
- **End-to-end (the real proof): `bash test/e2e/run.sh` in Git Bash** — full loop against real Docker/restic/Postgres. `KEEP=1` leaves the containers up for debugging. Script handles MSYS path-conversion pitfalls; don't add `curl -o /dev/null` or `docker exec` into the scratch image (no shell — use `docker cp`).
- Run locally: `docker run --rm -p 4000:4000 -v //var/run/docker.sock:/var/run/docker.sock -u 0 vibe-backup:dev` (UI at http://localhost:4000; production always uses a socket proxy per docs/socket-proxy.md)

CI (`ci.yml`): vet/test/build, Docker build, size gate, binary smoke-runs. `release.yml` publishes multi-arch to `ghcr.io/kisaesdevlab/vibe-backup` — `:main` on main pushes, semver + `:latest` only on `v*` tags.

## Code map (internal/)

`dockerapi` (own minimal Docker client, stdlib, API v1.44 floor, hijacked exec streaming — NOT the official SDK, image budget) → `discovery` (label-based contract discovery, error-not-skip) → `contract` (schema-1 parser, the cross-repo API — changing it means 12 module updates) → `stage` (staging transaction: fatal-secret gate, hooks, exec pg_dump, volume capture) + `pairing` (BK-3a atomicity seal; verify before EVERY restore) + `snapshot` (manifest, exact image tags) → `resticw` (refuses to prune non-prunable repos — B2 is append-only by design) → `engine` (RunBackup/RunDrill/Rotate/Setup/restores + scheduler; drill creates an ephemeral DB on the live network and execs module canaries with restored secrets) → `b2` (provisioning refuses lock-disabled buckets and delete-capable keys), `kit` (passphrase/six-word code/PDF), `state` (state.json + secrets.json 0600), `health` (status card truth), `server` (API + embedded UI; no auth by design — ingress authenticates).

Invariants that must survive any refactor: backups refuse to run before kit confirmation; a declared-but-uncapturable artifact fails the run loudly; pairing.json is written last and verified before any restore; nothing the appliance holds can delete B2 history; the drill's canary uses restored key material against restored data.

## Suite conventions (learned from sibling repos)

- License: PolyForm Internal Use 1.0.0 aligned with Vibe-Appliance's text (`LicenseRef-PolyForm-Internal-Use-1.0.0` in OCI labels). See QUESTIONS.md #2 for a known divergence between appliance and module license texts.
- `.appliance/manifest.json` is the appliance integration entry, mirroring Vibe-Tax-Research-Chat's shape; its field semantics are unverified against the appliance loader (QUESTIONS.md #5).
- The suite's other repos are TypeScript/pnpm; this repo is deliberately Go (BK-1's < 60 MB image criterion + restic pairing — rationale in STATE.md).

## What this project is

`vibe-backup` is a backup/restore sidecar for the Vibe Appliance — a Docker Compose suite of CPA-firm modules (Connect, Filer, T&B, MyBooks, Tax Research Chat, AI Router, 1099, Sentinel, etc.) backed by ParadeDB (Postgres). The target operator is a novice; the design mandate is **zero backup decisions, one-button restore, loud failure, and recovery that works when the appliance is gone**.

Stack: restic (static Go binary, client-side AES-256) → two destinations: a local repo (GFS-pruned) and firm-owned Backblaze B2 with compliance-mode Object Lock (90-day retention, write-only application key, never pruned). Postgres is captured via logical `pg_dump -Fc` per database plus `pg_dumpall --globals-only`, with the exact ParadeDB image tag recorded so restores always target the image they were dumped from.

## Architecture decisions that constrain all work

The plan opens with nine locked decisions (D-1 through D-9). Do not relitigate them; the ones with the widest code impact:

- **D-9 (two deployment modes):** `vibe-backup` must be fully self-contained — it runs standalone beside a single module as readily as inside the full appliance. Consequences: module discovery is by **Docker label** (`vibe.backup.contract=...`), not by the appliance manifest; the setup wizard, Recovery Kit issuance, and status/restore UI all live in this repo (the appliance console only proxies to them). Docker access goes through a socket proxy restricted to list/inspect (`CONTAINERS: 1`), never a raw socket mount.
- **D-3/D-4/D-5 (immutability + generations):** B2 is append-only by construction — `restic prune` is impossible there by design. Growth is bounded by 90-day Object Lock expiry plus annual repository "generations" (fresh repo + fresh passphrase + new Recovery Kit every 12 months). Only the local repo is pruned.
- **D-6 (Connect escrow):** Connect content keys are also wrapped for a firm master key. Connect must **not** be described as E2EE — the accurate framing is "encrypted at rest against database compromise, firm-recoverable."
- **Vendor escrow is refused by design.** KisaesDevLab never holds the passphrase or firm data (D-2). The activation gate (operator must confirm the printed Recovery Kit by typing back a six-word code before backups start) is the compensating control and must stay strict.

## The module backup contract (§4.2) — the load-bearing abstraction

Each module declares its backup needs in a `backup.contract.yaml` (databases, volumes, secrets with `criticality`, pre/post/canary hooks, `reauth_required_after_restore`), discovered via container label. Rules the orchestrator must enforce:

- A **fatal** secret the orchestrator cannot read fails the whole run loudly — never produce an unrestorable snapshot.
- A labeled container with no readable contract is an **error, not a skip**. Stateless modules declare emptiness affirmatively.
- Secrets and DB dumps are captured **atomically** under one snapshot ID — a KEK from one moment paired with ciphertext from another is a corrupt backup that looks healthy (the plan's most likely silent failure).
- The `canary` hook must be a real decrypt round-trip using restored key material, not a ping.
- The contract schema is a **cross-repo API** implemented by ~12 module repos. Version it explicitly (`schema: 1`) and accept known-older versions; never let one lagging module block every backup.

## Health model

The headline metric is **"last verified restore," not "last successful backup"** — a monthly automated drill (BK-8) restores into ephemeral containers and runs every module's decrypt canary. Console failure states are one plain sentence plus one action button; diagnostics live a click deeper. Bare-metal recovery (`vibe-restore.sh`, BK-9) must take a fresh Ubuntu box to a restored host in under 60 minutes, unattended after credential entry.

## Build order

Phases BK-0 → BK-14 are specified in §9 with exit criteria; the dependency-respecting order is in §8: BK-0 → 1 → 2 → 2a (contract schema freeze) → 3/3a/4 (parallel with 2c in `vibe-connect`) → 2b fan-out (12 parallel module PRs, each gated on its canary passing in the BK-8 drill harness) → 5/5a/6 → 7/7a/8/8a → 9 → 10/10a/11 → 12 → 13 → 14. Work in this repo covers every phase except BK-2b/2c (module and Connect repos), BK-10a (`vibe-appliance`), and BK-12's Sentinel side.

Open questions that block or shape work are listed in §10 of the plan (licensing across deployment modes, naming collision check before BK-0, local-only firms, drill egress budget). Surface these to the user rather than deciding them.
