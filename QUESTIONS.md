# Questions

Open decisions for the user. Resolved items move to the bottom with their answer.

## Open

1. **Licensing across deployment modes (plan §10.1, D-1 + D-9).** Backup is bundled non-optional in the appliance base band — but does it also ship free with every standalone module (T&B-only, MyBooks-only firms)? Plan's lean: yes, absorbed as support-and-liability reduction. Does the Commercial License Agreement need a DR/availability clause given the D-8 support blackout exemption?
2. **License text divergence.** Vibe-Appliance's LICENSE (used here per BK-0 "alignment with base appliance") contains a noncompete-style "Internal Use Limitation"; Vibe-Tax-Research-Chat uses stock PolyForm Internal Use with "Internal Business Use" instead. Which text is canonical for the suite? This repo currently follows the appliance's.
3. **Firm declines B2 entirely (plan §10.3).** Local-only is supported but forfeits off-site protection. Persistent console warning, Sentinel finding, or silent acceptance?
4. **Drill cost control (plan §10.4).** `check --read-data` against B2 incurs egress; exact subset-check budget needs setting in BK-4.
5. **Manifest field semantics.** `.appliance/manifest.json` mirrors Tax-Research-Chat's shape but hasn't been validated against Vibe-Appliance's loader (code search couldn't locate the consumer). Verify at BK-10a — or earlier if the appliance rejects unknown modules.
6. **Socket proxy scope (new, from implementation).** The plan's "CONTAINERS:1 list/inspect-only" proxy cannot support the plan's own exec-based dumps, hooks, canaries, and drill container. docs/socket-proxy.md records the real endpoint allowlist and the threat trade-off. Confirm this deviation is acceptable, or the drill design changes.
7. **Backup schedule default (new).** Implemented: nightly 01:30 firm-local + monthly day-1 drill. The plan's console mock shows "last backup 41 minutes ago," which implies something more frequent. Is nightly right for v1, or should the DB dump run hourly with volumes nightly?
8. **BK-5a wiring (new).** Filer's bucket protection is implemented (`ProtectFilerBucket`) but not yet callable from the wizard: the wizard doesn't know Filer's bucket ID until Filer's own provisioning story is settled. Where does Filer's bucket identity come from in standalone mode?

## Resolved

- **Naming (plan §10.2)** — 2026-08-29: no collision. "vault" does not appear in Vibe-Connect (the Vaultwarden reference is Sentinel's). "Vibe Backup" confirmed as the module name.
- **Language** — 2026-08-29: Go, driven by the < 60 MB image criterion and restic pairing. See STATE.md.
- **Repo visibility** — 2026-08-29: public, matching Vibe-Appliance and nearly all module repos.
