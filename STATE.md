# State

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
