# Vibe Backup

Backup and verified-restore sidecar for the [Vibe Appliance](https://github.com/KisaesDevLab/Vibe-Appliance) and standalone Vibe modules. restic to a firm-owned Backblaze B2 bucket with compliance-mode Object Lock, monthly restore drills with per-module decrypt canaries, a printed Recovery Kit, and a bare-metal recovery path that works when the appliance is gone.

**Status: pre-alpha scaffold (BK-0).** Nothing here backs anything up yet. See `vibe-backup-BUILD-PLAN.md` for the design and `PHASES.md` for progress.

## Scope — read this before relying on it

Vibe Backup protects **the Vibe appliance's state**: module databases, volumes, secrets, and configuration. It does **not** back up workstations, file servers, Exchange, tax-software data (UltraTax, Lacerte), or anything else on the host outside the Vibe stack.

## Design pillars

- The operator makes **no backup decisions** — the wizard provisions everything.
- The passphrase is **generated, never chosen**, and backups don't start until the printed Recovery Kit is confirmed saved.
- The health metric is **"last verified restore,"** not "last successful backup."
- Restore **must not require the appliance to be alive** — recovery starts from a printed kit and a fresh Linux box.

## License

PolyForm Internal Use 1.0.0 — see [LICENSE](LICENSE). Copyright (c) 2026 Kisaes LLC. Bundles restic (BSD-2-Clause).
