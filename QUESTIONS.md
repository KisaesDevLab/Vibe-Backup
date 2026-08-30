# Questions

Open decisions for the user. Resolved items move to the bottom with their answer.

## Open

- **Appliance EXTRA_PATHS vs non-root (BK-10a residue).** The build plan's §3
  non-module items include the env files and Caddy's cert store, but on the
  appliance those trees are root-600 while this sidecar is non-root by policy
  (BK-1, uid 65532) — listing them in `VIBE_BACKUP_EXTRA_PATHS` would fail
  every backup with EACCES. The appliance integration ships with
  `EXTRA_PATHS=/host/vibe/state.json` only (state.json is 644) and keeps
  Duplicati's whole-tree job as the net. Options to close the gap:
  (a) the appliance grants read ACLs (`setfacl -Rm u:65532:rX` + default
  ACLs) on `/opt/vibe/env` and `/opt/vibe/data/caddy` at enable time;
  (b) a root-owned host-side step (cron or enable-hook) tars those trees
  into the sidecar's own data dir where it can read them;
  (c) accept that per-app key material already travels via BK-2b contracts
  (container-inspect) and scope EXTRA_PATHS to what a non-root reader can
  see. Decide before declaring Duplicati optional.

## Resolved (2026-08-30)

- **Manifest field semantics — verified against the Vibe-Appliance loader.** The appliance does NOT read module repos' `.appliance/` files; `lib/enable-app.sh` validates `console/manifests/<slug>.json` **vendored inside Vibe-Appliance**, requiring `schemaVersion, slug, displayName, description, image, subdomain` (singular string), `ports, routing` (`default_upstream` + optional matchers/deny_paths), `env, health`. Our original mirror of Tax-Research-Chat's repo copy (plural `subdomains`, no `routing`) would have failed preflight — TRC's own repo copy is stale relative to its vendored manifest. Ours is rewritten to the canonical shape and passes the loader's exact validation snippet. *BK-10a needs three files in Vibe-Appliance: `console/manifests/vibe-backup.json` (vendor ours verbatim), `apps/vibe-backup.yml` (compose overlay: sidecar + socket proxy + state volume), `env-templates/per-app/vibe-backup.env.tmpl`. Note the appliance's Postgres container is `vibe-postgres`, encoded via `static:` env.*

## Resolved (Q&A session, 2026-08-29)

- **Bundling (plan §10.1)** — Vibe Backup ships with **every Vibe product at no separate charge**, bundled and non-optional, for appliance and standalone modules alike. Cost absorbed as support-and-liability reduction. *Cross-repo action: pricing/marketing copy for the module products should state backup is included.*
- **CLA DR clause (plan §10.1)** — **Yes**: the Commercial License Agreement gains a DR/availability clause covering the 60-minute self-service target, the D-8 blackout exemption terms, and that recovery depends on the firm safeguarding its Recovery Kit. *Cross-repo action: legal/CLA document, not this repo.*
- **License text divergence** — **Stock PolyForm Internal Use 1.0.0 everywhere** (the Vibe-Tax-Research-Chat text). This repo's LICENSE replaced accordingly (Licensed Work: Vibe Backup). *Cross-repo action: Vibe-Appliance should migrate off its custom noncompete-flavored text.*
- **Local-only firms (plan §10.3)** — **Persistent console warning only**; no Sentinel compliance finding. Current implementation already matches (console warns on every status render; the kit itself prints the local-only warning).
- **Drill egress budget (plan §10.4)** — **Rotating 10% per month** (`--read-data-subset n/10`): every B2 byte re-verified over ~10 months, inside a generation's lifetime and inside B2's 3×-storage free-egress allowance. Implemented (`subsetForDrill`, drives off `state.DrillCounter`); local repo stays full-read.
- **Socket proxy scope** — **Accept the documented allowlist** in docs/socket-proxy.md (containers + exec + image pull + drill lifecycle). The plan's "list/inspect-only" shorthand is superseded; immutability never depends on the appliance's honesty.
- **Backup schedule default** — **Nightly everything** (01:30 firm-local) stands; the plan's "41 minutes ago" console mock was illustrative, not a requirement. Revisit toward hourly DB dumps only if beta firms ask.
- **Filer bucket identity (BK-5a)** — **The wizard provisions Filer's bucket too**: setup's `provisionFilerBucket` flag creates the locked document bucket, lifecycle rules, and a no-delete key in the same authorized pass; stored in state/secrets, surfaced in `/api/status`. *Cross-repo action: the installer injects `FilerBucket`/`FilerKeyID`/`FilerKey` into Filer's environment; retrofit path for pre-existing buckets stays available via `ProtectFilerBucket`.*

## Resolved (earlier)

- **Naming (plan §10.2)** — 2026-08-29: no collision. "vault" does not appear in Vibe-Connect (the Vaultwarden reference is Sentinel's). "Vibe Backup" confirmed as the module name.
- **Language** — 2026-08-29: Go, driven by the < 60 MB image criterion and restic pairing. See STATE.md.
- **Repo visibility** — 2026-08-29: public, matching Vibe-Appliance and nearly all module repos.
