# WISP Language — Backup and Recovery Section

Drop-in text for the firm's Written Information Security Plan (per IRS
Pub 4557 / 5708 and the FTC Safeguards Rule §314.4). Sentinel
auto-populates the dated evidence where marked [auto].

---

## Data Backup and Recovery

**Scope.** The firm's Vibe appliance, containing [client records,
workpapers, communications, and related key material], is backed up
automatically. Systems outside the appliance are covered separately in
[section ___].

**Method.** Backups run nightly, encrypted (AES-256) before leaving the
appliance, to (1) a local repository and (2) cloud object storage in the
firm's own Backblaze B2 account under a direct firm–vendor agreement.
The cloud copy is immutable: object-lock retention of 90 days in
compliance mode prevents alteration or deletion by any party, including
a fully compromised appliance and including the storage account's own
master credentials.

**Key management.** The backup passphrase is machine-generated and
exists in exactly two places: the appliance and a printed Recovery Kit
governed by the Recovery Kit Handling Policy [reference]. The software
vendor holds no copy of firm keys or data.

**Verification.** A restore drill runs monthly: the latest backup is
restored into an isolated environment and every module performs a
decryption round-trip against the restored data and restored keys. The
firm's recovery evidence is therefore "last verified restore," not
"last successful backup."
- Last verified restore: [auto]
- Drill results, trailing 12 months: [auto]

**Retention.** Backup retention is 90 days and serves disaster
recovery. Records retention obligations are met by the application
systems and the firm's retention schedule [reference], not by backups.

**Recovery.** Recovery procedures (single file, single module, full
appliance) are documented in the Restore Runbook [reference]. Full
recovery from bare hardware requires only the printed Recovery Kit and
a commodity computer, and is rehearsed [annually / per BK-14 beta
schedule], including once during filing season.

**Vendor.** Backblaze, Inc. provides cloud storage under the firm's own
account (SOC 2 Type II; see vendor assessment [auto — Sentinel
template]). The firm's data is encrypted before transmission; the
vendor cannot read it.
