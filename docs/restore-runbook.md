# Restore Runbook

Three tiers. Work top-down; use the smallest tier that fixes the problem.

## T0 — A file

Console → **Restore** → choose the backup date → enter or browse the
file path → download or restore in place. No confirmation ceremony; this
touches nothing else.

## T1 — One module

Console → **Restore** → choose the date → module restore. You will type
the module's name to confirm, because this **replaces the module's
current data** with the snapshot.

What happens automatically: the snapshot is integrity-verified (nothing
restores if the key material and data came from different moments), the
module's databases are rolled back, its post-restore hook runs, and you
get a checklist of external re-connections (Stripe, Plaid, etc.).

If the console warns that **restored key material differs from the
module's current environment**: the module's env must be updated to the
snapshot's values (shown in the warning) and the module restarted —
otherwise the restored data cannot decrypt.

## T2 — Everything (appliance dead, encrypted, stolen, flooded)

Do not troubleshoot the dead box. You need:

1. The **printed Recovery Kit** from the safe.
2. Any machine with a **fresh Ubuntu** install and internet.

Run:

```
curl -fsSL https://get.kisaes.com/restore | sudo bash
```

Scan the kit's QR code when prompted (or type the bucket, restore key,
and passphrase). Pick the backup date. Everything else is automatic:
Docker and restic install, the snapshot restores, its integrity is
verified **before** anything is used, the database comes up on the exact
image version the backup was taken from, and every database is restored
into it. Budget: under an hour, most of it download time.

The script ends with the re-connection checklist and pointers for
re-running the Vibe installer against the restored state.

**If integrity verification fails**: the script refuses to continue.
Pick an earlier snapshot. Do not force it — a snapshot that fails
pairing verification can restore to data that looks fine and cannot be
decrypted.

## When to call support

Support's disaster-recovery line exists for: appliance unrecoverable,
confirmed data loss, or ransomware — after the T2 path has been tried.
"The restore worked and now I need to reconnect QuickBooks" is the
checklist, not a disaster.
