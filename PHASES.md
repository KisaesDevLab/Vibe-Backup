# Phases

Authoritative detail lives in `vibe-backup-BUILD-PLAN.md` §9. This file tracks status only.
Build order (plan §8): BK-0 → 1 → 2 → 2a → 3/3a/4 (‖ 2c in Vibe-Connect) → 2b fan-out → 5/5a/6 → 7/7a/8/8a → 9 → 10/10a/11 → 12 → 13 → 14.

| Phase | Deliverable | Exit criteria | Status |
|-------|-------------|---------------|--------|
| BK-0 | Repo scaffold, CLAUDE.md, manifest entry, CI, license alignment | Builds and publishes to GHCR | **In progress** |
| BK-1 | Sidecar container: restic static binary, minimal base, non-root, no baked secrets | Image < 60 MB | Not started |
| BK-2 | Backup orchestrator: enumeration, staging, ordering, pre/post hooks | Complete, correct stage from a live stack | Not started |
| BK-2a | Module backup contract: schema, label discovery, hook invocation, fatal gating | Refuses to snapshot when a fatal secret is unreadable | Not started |
| BK-2b | Contract authoring across 12 module repos (cross-repo fan-out) | Every module declares key material; working decrypt canaries | Not started — blocked on BK-2a schema freeze; Connect blocked on BK-2c |
| BK-2c | Connect firm-escrow key model (D-6) — in Vibe-Connect | Firm-recoverable; no unsupported E2EE claim | Not started (cross-repo) |
| BK-3 | ParadeDB dump/restore round-trip, globals, image-tag recording | Dump → fresh container → restore → canary passes | Not started |
| BK-3a | Atomic secret/DB pairing under one snapshot ID | Desynchronized KEK/ciphertext detected and rejected | Not started |
| BK-4 | restic lifecycle: init, backup, local GFS forget/prune, check | Snapshots created and verifiable on both targets | Not started |
| BK-5 | B2 provisioning, firm-owned (D-2): guided flow, bucket + Object Lock, write-only key | Operator never opens B2 console; key provably cannot delete | Not started |
| BK-5a | Filer bucket protection in same wizard pass | Compromised appliance cannot delete firm documents | Not started |
| BK-6 | Recovery Kit: passphrase gen, PDF, QR, activation gate, custodians | Backups refuse to start until kit confirmed | Not started |
| BK-7 | Object Lock enforcement + no-prune B2 model, red-team write-only key | Root-compromised appliance cannot delete history — demonstrated | Not started |
| BK-7a | Repository generations (D-4): annual re-init, new kit, expiry tracking | Rollover completes with no operator work beyond kit gate | Not started |
| BK-8 | Automated restore drill: ephemeral restore + every module canary | Monthly unattended; broken KEK pair fails it | Not started |
| BK-8a | Post-restore reconciliation: re-auth checklist, drift detection | Explicit checklist after any T1/T2 restore | Not started |
| BK-9 | vibe-restore.sh bare-metal bootstrap, both deployment modes | Clean Ubuntu → restored host < 60 min | Not started |
| BK-10 | Self-contained UI: wizard, status card, T0/T1 restore, kit management | Usability-tested; works standalone | Not started |
| BK-10a | Appliance console proxy — in Vibe-Appliance | No duplicated UI logic | Not started (cross-repo) |
| BK-11 | Monitoring: dead-man's switch, 24h silence alert, B2 billing health | Alert < 24h of silent failure; suspended account ≠ upload failure | Not started |
| BK-12 | Sentinel integration: evidence feed, WISP section, vendor template | Compliance-ready artifact with real dates | Not started |
| BK-13 | Docs: quickstart, restore runbook, kit policy, WISP language, scope disclaimer | Reviewed against IRS Pub 5708 | Not started |
| BK-14 | Field beta, 2–3 firms, forced full restore, mid-season rehearsal | Real novice completes T2 restore from printed kit | Not started |

Deferred to v1.1: WAL-G PITR, Shamir split escrow, multi-host, firm-wide endpoint backup.
