# Phases

Authoritative detail lives in `vibe-backup-BUILD-PLAN.md` §9. This file tracks status only.
Build order (plan §8): BK-0 → 1 → 2 → 2a → 3/3a/4 (‖ 2c in Vibe-Connect) → 2b fan-out → 5/5a/6 → 7/7a/8/8a → 9 → 10/10a/11 → 12 → 13 → 14.

| Phase | Deliverable | Exit criteria | Status |
|-------|-------------|---------------|--------|
| BK-0 | Repo scaffold, CLAUDE.md, manifest entry, CI, license alignment | Builds and publishes to GHCR | **Done** 2026-08-29 (v0.1.0) |
| BK-1 | Sidecar container: restic static binary, minimal base, non-root, no baked secrets | Image < 60 MB | **Done** 2026-08-29 — restic v0.18.1 in image (33 MB), CI size gate + smoke-runs |
| BK-2 | Backup orchestrator: enumeration, staging, ordering, pre/post hooks | Complete, correct stage from a live stack | **Done** 2026-08-29 — proven by e2e harness against a live stack |
| BK-2a | Module backup contract: schema, label discovery, hook invocation, fatal gating | Refuses to snapshot when a fatal secret is unreadable | **Done** 2026-08-29 — fatal-secret gating, error-not-skip, cross-check all tested |
| BK-2b | Contract authoring across 12 module repos (cross-repo fan-out) | Every module declares key material; working decrypt canaries | Not started — cross-repo fan-out; e2e harness ready to gate module canaries |
| BK-2c | Connect firm-escrow key model (D-6) — in Vibe-Connect | Firm-recoverable; no unsupported E2EE claim | Not started (cross-repo) |
| BK-3 | ParadeDB dump/restore round-trip, globals, image-tag recording | Dump → fresh container → restore → canary passes | **Done*** 2026-08-29 — round-trip proven vs postgres:16; ParadeDB/BM25 rebuild still to verify against a real ParadeDB image |
| BK-3a | Atomic secret/DB pairing under one snapshot ID | Desynchronized KEK/ciphertext detected and rejected | **Done** 2026-08-29 — desync detected and rejected (unit + e2e restore path) |
| BK-4 | restic lifecycle: init, backup, local GFS forget/prune, check | Snapshots created and verifiable on both targets | **Done** 2026-08-29 — init/backup/GFS local prune/check; B2 subset check wired |
| BK-5 | B2 provisioning, firm-owned (D-2): guided flow, bucket + Object Lock, write-only key | Operator never opens B2 console; key provably cannot delete | **Code done*** — provisioning + no-delete assertions unit-tested vs API fake; NOT yet run against live B2 (needs a real account) |
| BK-5a | Filer bucket protection in same wizard pass | Compromised appliance cannot delete firm documents | **Code done*** 2026-08-29 — wizard provisions the Filer bucket (locked, lifecycle, no-delete key) in the same pass; retrofit path kept; live-B2 run pending; installer must inject the key into Filer's env |
| BK-6 | Recovery Kit: passphrase gen, PDF, QR, activation gate, custodians | Backups refuse to start until kit confirmed | **Done** 2026-08-29 — gate refusal + activation proven in e2e; PDF kit with QR |
| BK-7 | Object Lock enforcement + no-prune B2 model, red-team write-only key | Root-compromised appliance cannot delete history — demonstrated | **Partial** — capability assertions + VerifyCannotDelete red-team implemented; live-B2 demonstration pending |
| BK-7a | Repository generations (D-4): annual re-init, new kit, expiry tracking | Rollover completes with no operator work beyond kit gate | **Done** 2026-08-29 — rotation lifecycle unit-tested; annual trigger scheduled; passphrase expiry after window |
| BK-8 | Automated restore drill: ephemeral restore + every module canary | Monthly unattended; broken KEK pair fails it | **Done** 2026-08-29 — drill with ephemeral DB + decrypt canary proven in e2e; monthly schedule implemented |
| BK-8a | Post-restore reconciliation: re-auth checklist, drift detection | Explicit checklist after any T1/T2 restore | **Done** 2026-08-29 — checklist + restored-secret mismatch warning |
| BK-9 | vibe-restore.sh bare-metal bootstrap, both deployment modes | Clean Ubuntu → restored host < 60 min | **Script written*** — scripts/vibe-restore.sh with pairing verification; the <60-min clean-Ubuntu rehearsal has NOT been run |
| BK-10 | Self-contained UI: wizard, status card, T0/T1 restore, kit management | Usability-tested; works standalone | **Done*** 2026-08-29 — self-contained UI (status card, wizard, gate, T0/T1, drill); usability test with a real novice pending |
| BK-10a | Appliance console proxy — in Vibe-Appliance | No duplicated UI logic | Not started (cross-repo) — needs 3 files there: console/manifests/vibe-backup.json (vendor ours verbatim; it passes the loader's preflight), apps/vibe-backup.yml overlay (sidecar + socket proxy + state volume), env-templates/per-app/vibe-backup.env.tmpl |
| BK-11 | Monitoring: dead-man's switch, 24h silence alert, B2 billing health | Alert < 24h of silent failure; suspended account ≠ upload failure | **Done** 2026-08-29 — 26h silence alert, suspended-vs-unauthorized-vs-unreachable B2 states, dead-man ping URL |
| BK-12 | Sentinel integration: evidence feed, WISP section, vendor template | Compliance-ready artifact with real dates | **Feed side done** — evidence JSONL + /api/evidence; Sentinel ingestion is cross-repo |
| BK-13 | Docs: quickstart, restore runbook, kit policy, WISP language, scope disclaimer | Reviewed against IRS Pub 5708 | **Drafted*** — quickstart, runbook, kit policy, WISP language, socket-proxy spec; human/Pub-5708 review pending |
| BK-14 | Field beta, 2–3 firms, forced full restore, mid-season rehearsal | Real novice completes T2 restore from printed kit | Not started — field beta requires real firms |

Deferred to v1.1: WAL-G PITR, Shamir split escrow, multi-host, firm-wide endpoint backup.

\* = the code/deliverable exists and is tested as far as this environment allows; the starred residue is what still needs a real B2 account, real ParadeDB, real hardware, or a real human.
