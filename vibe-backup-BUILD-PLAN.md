# Vibe Backup — Build Plan v2

**Module:** `vibe-backup`
**Parent:** Vibe Appliance (meta-installer) — bundled, non-optional
**Target operator:** a novice IT person at a 3–30 person CPA firm, or the firm's part-time MSP
**Design mandate:** zero backup decisions, one-button restore, loud failure, and a recovery path that works when the appliance is gone

---

## Decisions locked

| # | Decision | Resolution | Consequence |
|---|---|---|---|
| D-1 | Product boundary | **Bundled, non-optional.** Priced into the base band, no separate license key | An optional backup module would be declined by exactly the firms most likely to need it |
| D-2 | B2 account ownership | **Firm owns the account.** Wizard provisions into it; Kisaes never holds firm data | Keeps Kisaes out of the data-processor role; firm's direct Backblaze contract satisfies Safeguards §314.4(f). Requires a guided account-creation flow and a billing-health check |
| D-3 | Append-only mechanism | **B2 Object Lock only.** No `rest-server`, no second service to host | Simplest possible topology. Cost: `restic prune` becomes impossible — resolved by D-4 |
| D-4 | Prune / rotation | **Repository generations.** Fresh repo + fresh passphrase + new Recovery Kit every 12 months; old generation expires on its own | One mechanism solves prune bloat, passphrase rotation, and custodian offboarding |
| D-5 | Retention | **90-day Object Lock retention.** Backups are for recovery, not records retention | Old generation is fully expired ~9 months before rotation, so exactly one live kit exists at nearly all times |
| D-6 | Connect key model | **Firm-level escrow.** Content keys wrapped for recipients *and* a firm master key held in `.env` | Firm never loses access; DB exfiltration still yields ciphertext. **Requires relabeling Connect** — see §4.5 |
| D-7 | Secrets placement | **Same repository as data** | One kit, one passphrase. Accepts that the kit is the master key (§4.3) |
| D-8 | Support blackout | **Narrow DR exemption at premium rate.** Appliance unrecoverable, data loss, or ransomware only | Not "I deleted a file." Made affordable by BK-9 hitting its 60-minute self-service target |
| D-9 | Deployment modes | **Two: appliance and standalone.** `vibe-backup` runs alongside a single module (T&B, MyBooks, Trial Balance) as readily as alongside the full suite | Discovery moves from manifest to Docker labels (§4.2); the wizard, kit issuance, and status UI move into `vibe-backup` itself (§8) so it is self-contained |

---

## 0. The design problem, stated honestly

The technical research is settled: restic to Backblaze B2 with Object Lock, logical `pg_dump` of ParadeDB. That stack is correct and it is also more moving parts than a novice can safely assemble. A firm admin asked to create a bucket, enable Object Lock, mint a scoped key, and safeguard a 64-character passphrase will get one step wrong and will not discover which one until the day they need a restore.

So the engineering problem is not "which backup engine." It is **collapsing a five-decision setup into a three-field form, and making the failure modes self-announcing.**

Four design rules follow, and every phase below serves them:

1. **The operator makes no backup decisions.** No retention math, no schedule choice, no bucket configuration. The wizard provisions everything through the B2 API. The operator never opens the Backblaze console.
2. **The passphrase is generated, never chosen — and activation is gated on proof it was saved.** A lost passphrase is unrecoverable data loss. This is the largest risk in the design and it is a human risk, not a software one.
3. **The health metric is "last verified restore," not "last successful backup."** A backup that has never been restored is an untested assertion.
4. **Restore must not require the appliance to be alive.** Ransomware, dead disk, and flood are the scenarios that matter. Recovery starts from a printed kit and a fresh Linux box.

---

## 1. Scope

### In scope
- Scheduled, verified, encrypted backup of all Vibe appliance state on the host
- Automated monthly restore drills with per-module decrypt canaries
- Console UI: status, browse, restore, recovery-kit management
- Bare-metal recovery bootstrap (`vibe-restore.sh`)
- Compliance evidence feed to Vibe Sentinel

### Adjacent scope — the Filer bucket
Filer's **document bodies live in the firm's B2 bucket, not on the appliance.** The backup captures Filer's database — folder↔client bindings, sentinel-file state, routing metadata — but the files themselves were never on the box to capture.

Those documents are primary client data and need equivalent protection, applied to the storage rather than copied onto the appliance: **bucket versioning, Object Lock on document objects, lifecycle rules expiring noncurrent versions, and a write-only application key for the Filer module** so a compromised appliance cannot delete the firm's files. The BK-5 wizard already speaks the B2 API and holds the master key momentarily, so it should provision the Filer bucket's protections in the same pass. Tracked as **BK-5a**.

Re-copying the bucket into the restic repo is the wrong answer — it doubles storage cost and egress for data that is already durable and already off-site.

### Out of scope (v1)
- **Firm-wide backup.** This protects the Vibe appliance, not workstations, file servers, Exchange, or UltraTax/Lacerte data. State this prominently in docs and in the console — underselling the scope is a support-and-liability trap.
- **Anything on the host outside the Vibe stack.** The manifest only knows about Vibe modules. Files a firm drops in a home directory, or containers they add themselves, are invisible to this. Say so in the operator quickstart.
- **Container images and host OS.** Re-derivable by design; see §3.
- Records retention. Backups hold 90 days; the applications own long-term retention (§5).
- Continuous/PITR replication. Deferred behind an RPO threshold.
- Multi-host orchestration.

---

## 2. Architecture

A single sidecar container added to the Compose stack by the meta-installer. No host install, no systemd units.

```
┌────────────────────────────────────────────────┐
│ Vibe Appliance host (Docker)                   │
│                                                │
│  caddy ── console ── [modules...] ── paradedb  │
│                          │                     │
│                   ┌──────▼──────┐              │
│                   │ vibe-backup │  sidecar     │
│                   │  - scheduler│              │
│                   │  - restic   │  static bin  │
│                   │  - drill    │              │
│                   └──────┬──────┘              │
└──────────────────────────┼─────────────────────┘
                           │
              ┌────────────┴────────────┐
              ▼                         ▼
      Local target (USB/NAS)     Backblaze B2 (firm-owned)
      fast restore, prunable     Object Lock, 90d, write-only key
```

**Engine:** restic (BSD-2-Clause), single static Go binary, native B2 backend, client-side AES-256.

**Two destinations (3-2-1).** The local repo is fully managed — `forget`/`prune` run normally there, giving fast restores and bounded size. The B2 repo is append-only by construction and never pruned. If the operator supplies only one destination, warn but proceed; a single-destination backup still beats the nothing they are running today.

**Immutability model (D-3).** The appliance holds a B2 application key with `writeFiles` and `listFiles` and **no** `deleteFiles`, scoped to one bucket. Object Lock in compliance mode is applied via bucket default retention. A fully root-compromised host cannot delete or alter history, and neither can a stolen master key — compliance mode retention cannot be shortened by anyone.

**Why no prune on B2.** Nothing can delete anything before expiry, so unreferenced pack files accumulate. This is a deliberate trade: unbounded-but-slow growth in exchange for a topology with no extra services. Growth is bounded in practice by D-4/D-5 — objects expire at 90 days and the whole repo is replaced annually.

---

## 3. What gets backed up

The orchestrator is manifest-driven, so a newly installed module is covered automatically.

| # | Item | Method | Why |
|---|------|--------|-----|
| 1 | ParadeDB — every module database | `pg_dump -Fc` per DB + `pg_dumpall --globals-only` | Portable across PG minor versions and CPU arch; BM25/pgvector indexes rebuild on restore |
| 2 | `caddy_data` volume | read-only mount, copied to stage | ACME account keys, certs, local CA. Loss risks Let's Encrypt rate limits |
| 3 | **Secrets and env** | direct copy, **atomically with item 1** | Highest criticality item in the list. See §4 |
| 4 | Config bind mounts | direct copy | `docker-compose.yml`, module manifests, Caddy config |
| 5 | Module named volumes | read-only mount per volume | Client documents, workpapers, Filer staging |
| 6 | License activation state | direct copy | Avoids re-activation friction; licensing service remains the fallback |
| 7 | Backup manifest + canaries | generated | Module versions, **exact image tags**, drill sentinels |

**Explicitly excluded:** container images (re-pullable from GHCR), `/var/lib/docker` wholesale, and OS state. The host is disposable by design.

Item 7 records the **exact ParadeDB image tag** the dump came from, and the restore path pulls that tag. This is what reduces the `pg_search`/`pgvector`/`shared_preload_libraries` coupling to a non-issue: you always restore into the image you dumped from.

---

## 4. Encryption and key material

A host-level sweep of Postgres and volumes is **not sufficient for this suite.** Several modules encrypt data at rest with keys that live outside the database. Restoring ciphertext without its key is indistinguishable from having no backup.

### 4.1 Key-material survey by module

| Module | Key material | Where it lives | Failure mode if mishandled |
|---|---|---|---|
| **Connect** | XChaCha20-Poly1305 content keys; **firm master wrapping key (D-6)** | Content keys wrapped per-recipient in DB; firm master key in env | With D-6, firm always recovers. Without it, a lost device meant permanently unreadable history |
| **Tax Research Chat** | AES-256-GCM KEK for stored provider API keys | KEK in env; ciphertext in Postgres | DB restored without matching KEK → all stored keys are garbage |
| **AI Router** | Firm-owned provider keys (Anthropic, DigitalOcean Gradient) | KEK in env; ciphertext in DB | Firm must re-enter every provider key |
| **Filer** | B2 application keys; sentinel-file folder↔client bindings | Env + Postgres + **state that lives in B2** | Old DB against a live bucket causes binding drift in both directions |
| **T&B** | Stripe Connect, GoTo Connect, OpenSign, calendar OAuth | Encrypted in DB | Restore does **not** roll back Stripe. Financial state diverges silently |
| **MyBooks** | Plaid access tokens; payroll connector credentials | Encrypted in DB | Stale tokens fail closed — recoverable if re-auth is documented |
| **1099** | IRIS A2A credentials / TCC, TaxBandits API | Env + DB | IRS-issued. Loss means a re-application cycle mid-season |
| **Sentinel** | Audit-log integrity chain; Vaultwarden DB + attachment keys | Env + own volume | An older Sentinel snapshot creates a log gap that reads like tampering |
| **Caddy** | ACME account keys, certs, local CA | `caddy_data` | Covered as item 2 |

Three classes, handled differently:

- **Class 1 — KEK/ciphertext splits** (Research Chat, AI Router, Filer, T&B, MyBooks, 1099, and now Connect). Payload in Postgres, key in `.env`. Must be captured **atomically and restored as a matched pair.** A snapshot whose DB and env came from different moments is a corrupt backup that looks healthy. This is the most likely silent failure in the design.
- **Class 2 — externally-authoritative state** (Filer/B2, T&B/Stripe, MyBooks/Plaid). Backup cannot restore the other side. Restore triggers reconciliation and re-auth, rather than pretending the snapshot is authoritative.
- **Class 3 — none remaining.** D-6 moves Connect out of the unrecoverable category.

### 4.2 The module backup contract

Rather than `vibe-backup` hardcoding knowledge of a dozen modules — which rots the moment a thirteenth ships — each module declares its requirements in a `backup.contract.yaml` that the meta-installer's manifest discovers:

```yaml
module: vibe-tax-research-chat
schema: 1
databases: [tax_research]
volumes: [trc_uploads]
secrets:
  - name: TRC_API_KEY_KEK
    source: env
    criticality: fatal        # fatal | recoverable | cosmetic
    note: "AES-256-GCM KEK for stored provider keys. Ciphertext in DB is
           worthless without this. Must restore from the same snapshot."
external_state: []
hooks:
  pre_backup:   /app/bin/backup-pre     # quiesce, flush, emit canary
  post_restore: /app/bin/restore-post   # re-derive caches, prompt re-auth
  canary:       /app/bin/canary         # prove a decrypt round-trip works
reauth_required_after_restore: []
```

Three properties make this work:

**Discovery is by Docker label, not by manifest (D-9).** Each module container carries `vibe.backup.contract=/app/backup.contract.yaml`; `vibe-backup` enumerates running containers and reads the contract from each. This works identically whether twelve modules are running under the meta-installer or T&B is running by itself, and it removes a dependency on the appliance repo that would otherwise make standalone impossible.

Container enumeration needs the Docker socket, which is privileged. Mount it through a socket proxy restricted to `CONTAINERS: 1` (list/inspect only) rather than exposing the raw socket to the backup container — Sentinel will flag a bare socket mount, and it would be right to.

**Contract discovery rules:**

- **`criticality: fatal` gates the backup.** If a module declares a fatal secret the orchestrator cannot read, the run **fails loudly** rather than producing an unrestorable snapshot.
- **A labeled container with no readable contract is an error, not a skip.** Silently skipping is how a future module ships unprotected while the console shows green. A module that genuinely holds no state declares that explicitly with empty `databases`, `volumes`, and `secrets` — an affirmative statement, not an absence.
- **In appliance mode, cross-check against the manifest.** When the meta-installer is present, reconcile discovered containers against the installed-module list and fail on any gap. Standalone mode has no such list and relies on labels alone.
- **The `canary` hook is a decrypt round-trip, not a ping.** During the monthly drill each module must decrypt a known sentinel using restored key material. That is the only test that proves Class 1 pairing survived.
- **`reauth_required_after_restore`** populates the operator's post-restore checklist, turning Class 2 surprises into expected steps: *"Reconnect Stripe. Re-authorize 3 Plaid connections. Verify Filer bucket binding."*

**Atomicity requirement:** secrets are captured in the same staging transaction as the DB dumps under a single snapshot ID. Never allow a partial refresh of one without the other.

### 4.3 Recovery Kit

**Generation:** the wizard generates a 256-bit repository passphrase. The operator never invents one and never types one during normal operation.

**The kit** is a single-page PDF containing: firm name, appliance ID, generation number and date; the passphrase in large monospace type and as a QR code; the B2 bucket name and a read-only restore key; a six-word verification code; and printed handling instructions.

**Activation gate:** backups do not begin until the operator downloads the kit and types back the six-word code. This is the only deliberate friction in the entire setup, and it converts an invisible catastrophic risk into a visible thirty-second task.

**The kit is the master key.** Because every module KEK lives in `.env` and `.env` lives in the repository, the passphrase unlocks all client taxpayer data on the box — and the raw `pg_dump` output is plaintext regardless of module-level encryption. The kit is also self-contained: bucket, key, and passphrase on one page means a photograph of it is sufficient to reconstruct the firm's dataset on any laptop.

Consequences that must appear in the docs and the WISP:
- Stored in a safe or safe deposit box alongside signed originals. Not a filing cabinet, not a shared drive, not a phone.
- Two named custodians, recorded in the WISP as an **asset**, not a procedure.
- **Loss of the kit is a security incident**, triggering the incident playbook — plausibly an unauthorized acquisition of customer information under the 2024 Safeguards notification amendment and state breach statutes.

**Vendor escrow: no.** KisaesDevLab must not hold a copy. Holding it would mean holding the ability to decrypt client taxpayer data, dragging Kisaes inside the firm's Safeguards perimeter and creating §7216 exposure that cannot be priced. Document this refusal prominently — it is a feature, and it is why the activation gate must be strict.

### 4.4 Repository generations (D-4)

`restic key add`/`remove` only changes which passwords wrap the master key; it does not re-encrypt data, so a leaked master key stays valid forever. Real rotation means a new repository — and Object Lock means the old one cannot be deleted early. Generations resolve both:

- Every **12 months**, the appliance initializes generation N+1 with a fresh passphrase and issues a new Recovery Kit through the same activation gate.
- Generation N stops receiving writes. Its contents expire under 90-day Object Lock retention, so it is fully empty roughly nine months before the next rotation.
- **At nearly all times exactly one kit is live.** This matters more than it sounds: the novice failure mode is not losing a kit, it is holding two and shredding the wrong one. The console states plainly which generation is current and when the prior one expires.
- Off-cycle rotation is triggered by custodian departure or suspected kit compromise.

### 4.5 Connect relabeling (D-6)

With a firm master wrapping key, Connect is **not end-to-end encrypted** in the sense the term normally implies, and describing it that way invites exactly the scrutiny you do not want in a security-sensitive market.

The accurate framing: *encrypted at rest against database compromise, firm-recoverable.* Content keys are wrapped for recipients and for a firm master key held outside the database; exfiltrating the DB yields ciphertext. Reserve the E2EE claim for client-to-firm transfer if you build it there.

This is a **cross-module dependency**: Connect's marketing copy, spec, and `backup.contract.yaml` must all change before BK-2b can honestly state coverage.

---

## 5. Retention model

Backups and records retention are different jobs, and conflating them inflates cost and risk for no benefit.

- **Backup retention: 90 days.** Enough to cover ransomware dwell time and slow-discovery corruption. Object Lock enforces it; nothing on the appliance can shorten it.
- **Records retention: the applications' job.** MyBooks, T&B, Filer, and Investments keep firm and client records for as long as the firm requires. Backup is not an archive and must not be sold as one.
- **Local repo:** GFS with normal pruning — keep-daily 14, keep-weekly 8, keep-monthly 3. Bounded and fast to restore from.

Dial to revisit: if a firm's threat model warrants longer ransomware dwell coverage, 180 days doubles B2 storage cost. At small-firm data volumes that is affordable but should be a deliberate choice, not a default.

---

## 6. Console UX

One page. The operator sees, in order:

```
  ✅  Backups healthy
      Last verified restore   2 days ago      ← the headline metric
      Last backup             41 minutes ago
      Off-site copy           B2, locked, 90-day retention
      Local copy              /mnt/backup, 14 days
      Recovery Kit            Gen 2, confirmed saved 2026-03-04
      B2 account              active, billing current

      [ Restore a file ]  [ Restore a module ]  [ Run a drill now ]
```

Failure state replaces the whole card, red, with one plain sentence and one button:

> ⚠️ **Backups have not completed since Tuesday.** Your Backblaze account appears suspended — this is usually an expired payment card. **[Fix this]**

No stack traces, no log tailing, no error codes on the primary surface. Diagnostics live one click deeper.

**Restore tiers:**
- **T0 — a file.** Browse snapshots by date, search filename, restore in place or download.
- **T1 — a module.** Roll one module's database back to a chosen snapshot. Requires typing the module name to confirm. Generates the re-auth checklist.
- **T2 — everything.** Not run from this console, which may be what's broken. Handed to `vibe-restore.sh`.

---

## 7. Bare-metal recovery — `vibe-restore.sh`

The scenario: the box is dead, encrypted, or under water. The operator has a fresh Ubuntu install and the printed kit.

```
curl -fsSL https://get.kisaes.com/restore | sudo bash
```

1. Installs Docker if absent
2. Prompts for bucket, restore key, and passphrase from the kit — QR-scannable to avoid transcription errors
3. Lists snapshots with dates in plain language; asks which
4. Pulls the exact image tags recorded in that snapshot's manifest from GHCR
5. Restores volumes, config, and secrets, then `pg_restore` into a fresh ParadeDB of the recorded version
6. Re-activates licensing
7. Runs every module canary and prints pass/fail plus the re-auth checklist

**Target: under 60 minutes, unattended after step 2, no Vibe support call.** This is the Phase BK-9 acceptance criterion, not an aspiration — and it is what makes the D-8 blackout exemption affordable, since the normal path never reaches you.

Ansible/cloud-init reprovisioning remains the right internal tool for KisaesDevLab support, but it is not novice-operable and is not the customer path.

---

## 8. Repo map

"Bundled, non-optional" (D-1) is a **distribution** decision, not a code-organization one. And because `vibe-backup` must run standalone (D-9), it has to be self-contained — the wizard, kit issuance, and status UI live in `vibe-backup`, not in the appliance.

| Repo | Owns | Phases |
|---|---|---|
| **`vibe-backup`** (new) | Everything operable: orchestrator, restic wrapper, label discovery, contract parser, drill runner, `vibe-restore.sh`, **B2 wizard, Recovery Kit issuance, its own minimal status/restore UI**. Publishes its own image to GHCR and versions independently | BK-0, 1, 2, 2a, 3, 3a, 4, 5, 5a, 6, 7, 7a, 8, 8a, 9, 10, 11 |
| **`vibe-appliance`** | Integration only: Compose entry, socket-proxy wiring, and a console page that **proxies to `vibe-backup`'s UI** rather than reimplementing it. Ingress and auth in appliance mode | BK-10a |
| **`vibe-connect`** | Firm-escrow key model and relabeling — genuinely cross-module, blocks BK-2b | BK-2c |
| **Each module repo** (×12) | Its own `backup.contract.yaml`, the `vibe.backup.contract` label, and three hook scripts (`backup-pre`, `restore-post`, `canary`) | BK-2b |
| **`vibe-sentinel`** | Evidence ingestion, WISP section rendering, Backblaze vendor-assessment template | BK-12 |

One UI, two hosts. In appliance mode the console proxies it so the operator sees one surface; in standalone mode `vibe-backup` serves it directly behind whatever ingress that deployment uses. Building it once in `vibe-backup` avoids a second implementation drifting out of sync, which is the failure mode of the alternative.

Two consequences worth planning around:

**BK-2b is twelve parallel pull requests, not one phase.** Each module must author a contract file, add the discovery label, and — critically — ship a working decrypt canary. This is the widest-fanout work in the plan and the most likely place for it to stall. Sequence it as: freeze the contract schema in BK-2a, then fan out, gating each module's PR on its canary passing in the BK-8 drill harness so "contract written" cannot be confused with "contract correct."

**The contract schema is a cross-repo API.** Once twelve modules implement `schema: 1`, changing it means twelve coordinated updates. Version it explicitly from the start and have the orchestrator accept known-older schema versions rather than failing closed — otherwise a single lagging module blocks every backup on the host.

The build order that respects the dependencies: BK-0 → 1 → 2 → 2a (schema freeze) → 3/3a/4 in `vibe-backup` while 2c runs in `vibe-connect` → 2b fan-out → 5/5a/6 → 7/7a/8/8a → 9 → 10/10a/11 → 12 → 13 → 14.

---

## 9. Phases

For autonomous Claude Code execution per the standard `CLAUDE.md` / `PHASES.md` / `STATE.md` / `QUESTIONS.md` pattern. Conventional commits throughout.

| Phase | Deliverable | Exit criteria |
|-------|-------------|---------------|
| **BK-0** | Repo scaffold, `CLAUDE.md`, manifest entry, CI, license alignment with base appliance | Builds and publishes to GHCR |
| **BK-1** | Sidecar container: restic static binary, minimal base, non-root, no baked secrets | Image < 60 MB |
| **BK-2** | Backup orchestrator: manifest-driven enumeration, staging, ordering, pre/post hooks | Produces a complete, correct stage from a live stack |
| **BK-2a** | **Module backup contract** (§4.2): schema, manifest discovery, hook invocation, `criticality: fatal` gating | Orchestrator refuses to snapshot when a fatal secret is unreadable |
| **BK-2b** | **Contract authoring across the suite**: Connect, Research Chat, AI Router, Filer, T&B, MyBooks, 1099, Entity, Investments, Calculators, Trial Balance, Sentinel | Every module declares key material and hooks; each has a working decrypt canary. *Blocked on BK-2c for Connect* |
| **BK-2c** | **Connect firm-escrow key model (D-6)** — cross-module work in the Connect repo: firm master wrapping key, key derivation, relabeling of spec and marketing copy | Connect content is firm-recoverable; no remaining "E2EE" claim that the architecture doesn't support |
| **BK-3** | ParadeDB dump/restore round-trip, globals, per-module DBs, image-tag recording | Dump → fresh container → restore → canary passes; BM25 rebuild verified |
| **BK-3a** | **Atomic secret/DB pairing**: env and secrets captured in the same staging transaction, single snapshot ID | Deliberately desynchronized KEK/ciphertext is detected and rejected, not restored |
| **BK-4** | restic lifecycle: init, backup, local-repo GFS `forget`/`prune`, `check --read-data-subset` | Snapshots created and verifiable on both targets |
| **BK-5** | **B2 provisioning, firm-owned (D-2)**: guided account-creation flow, master key accepted once, bucket + versioning + 90-day Object Lock created, write-only key minted, master discarded | Operator never opens the B2 console; appliance key provably cannot delete |
| **BK-5a** | **Filer bucket protection (§1)**: versioning, Object Lock on document objects, lifecycle rules for noncurrent versions, write-only key for the Filer module — provisioned in the same wizard pass | A compromised appliance cannot delete the firm's stored documents; console reports Filer bucket protection status alongside backup status |
| **BK-6** | **Recovery Kit**: passphrase generation, PDF render, QR encode, activation gate, custodian recording, annual re-confirmation | Backups refuse to start until the kit is confirmed |
| **BK-7** | **Object Lock enforcement + no-prune B2 model (D-3)**: verify compliance-mode retention, red-team the write-only key | A root-compromised appliance cannot delete or alter snapshot history — demonstrated, not assumed |
| **BK-7a** | **Repository generations (D-4)**: annual re-init, new kit issuance through the gate, old-generation expiry tracking, console display of current generation | Generation rollover completes without operator intervention beyond the kit gate |
| **BK-8** | **Automated restore drill**: ephemeral ParadeDB + volumes, restore latest snapshot, run **every module's decrypt canary**, teardown, record result | Monthly drill runs unattended; a broken KEK/ciphertext pair fails it; result drives the console headline |
| **BK-8a** | **Post-restore reconciliation**: re-auth checklist generation, Filer↔B2 drift detection, Stripe/Plaid divergence warnings | Operator sees an explicit checklist after any T1/T2 restore |
| **BK-9** | `vibe-restore.sh` bare-metal bootstrap, **both deployment modes** | Clean Ubuntu → fully restored host in **< 60 min**, unattended after credential entry. Tested for full-suite *and* single-module standalone restore |
| **BK-10** | **Self-contained UI in `vibe-backup`**: setup wizard, status card, plain-English failure states, T0/T1 restore flows, kit management | Usability-tested against a non-technical operator; works standalone with no appliance console present |
| **BK-10a** | **Appliance console proxy**: single-pane integration so appliance operators see one surface, not two | No duplicated UI logic; appliance and standalone render the same screens |
| **BK-11** | Monitoring: dead-man's-switch, 24h silence alert, **B2 account/billing health check (D-2)**, status JSON for the manifest console | Alert fires within 24h of simulated silent failure; suspended-account state is detected distinctly from upload failure |
| **BK-12** | **Sentinel integration**: backup/drill evidence, auto-populated WISP backup-and-recovery-testing section, Backblaze vendor-assessment template | Sentinel renders a compliance-ready artifact with real dates |
| **BK-13** | Docs: one-page operator quickstart, restore runbook, kit-handling and custodian policy, WISP language, **explicit scope disclaimer** (§1) | Reviewed against IRS Pub 5708 expectations |
| **BK-14** | Field beta on 2–3 firms, including one forced full restore and **one mid-season (Feb–Mar) restore rehearsal (D-8)** | A real novice completes a T2 restore using only the printed kit, under season load |

**Deferred to v1.1:** WAL-G PITR behind an RPO threshold (add above ~100 GB or sub-hour RPO); Shamir split escrow for the kit; multi-host orchestration; firm-wide endpoint backup.

---

## 10. Remaining open questions

1. **Licensing across deployment modes (D-1 + D-9).** Backup is bundled non-optional in the appliance base band — but what about a firm that buys only T&B, or only MyBooks, under PolyForm or BSL? If backup isn't included there too, the "no firm runs Vibe without backup" logic collapses for what is probably the larger share of the installed base. My lean is that it ships with **every** Vibe product at no separate charge, and the cost is absorbed as a support-and-liability reduction rather than a line item. Does the Commercial License Agreement also need a DR/availability clause given D-8?
2. **Naming.** "Vibe Backup" is clear, but confirm no collision with Connect's vault language before BK-0.
3. **What happens when a firm declines B2 entirely?** Some will refuse cloud storage outright. Local-only is supported but forfeits off-site protection and most of the Safeguards story. Does the console warn persistently, does Sentinel flag it as a finding, or is it accepted silently?
4. **Drill cost control.** `check --read-data` against B2 incurs egress. The plan assumes subset checking and local-repo verification, but the exact budget needs setting in BK-4.

---

## 11. Why this satisfies "reliable"

Reliability here is not the backup engine — restic is well-proven. It comes from four properties the plan enforces:

- **The drill (BK-8)** converts "we have backups" into "we restored last Tuesday and every module decrypted." That is the difference between a control and a hope, and it is what IRS Pub 5708 asks a firm to document.
- **The activation gate (BK-6)** eliminates the most common total-loss scenario, which is not disk failure but a lost passphrase.
- **Atomic key pairing (BK-3a)** eliminates the most dangerous scenario, which is a snapshot that passes every check and restores to garbage.
- **Immutability (BK-5/BK-7)** means ransomware — overwhelmingly the realistic threat to a small firm — cannot destroy the recovery path even with full host compromise.

Everything else is plumbing.
