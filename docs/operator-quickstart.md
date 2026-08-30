# Vibe Backup — Operator Quickstart

One page. Read it once; the console handles the rest.

## What this protects — and what it does not

Vibe Backup protects **the Vibe appliance**: every module's database,
documents, settings, and encryption keys. Automatically, every night,
verified by a monthly restore drill.

It does **not** protect anything else in your office:

- ❌ Workstations and laptops
- ❌ File servers and NAS shares outside Vibe
- ❌ Email / Exchange / Microsoft 365
- ❌ UltraTax, Lacerte, or any other tax software's data
- ❌ Files someone copies onto the appliance outside of Vibe

Those need their own backup. If you tell clients "we have backups"
because of Vibe Backup alone, you are overpromising.

## Setup (once, ~10 minutes)

1. Create a Backblaze account at backblaze.com (choose **B2 Cloud
   Storage**), add the firm's payment card.
2. In Backblaze: **App Keys → Generate New Master Application Key.**
3. In the Vibe console → Backup: enter the firm name and the master key.
   The wizard creates everything (bucket, immutability lock, restricted
   keys) — you never touch the Backblaze console again. The master key is
   used once and never stored.
4. **Print the Recovery Kit.** Put it in the safe. Type the six words
   from the kit into the console. Backups begin.

That last step is not optional and cannot be skipped: a backup whose
passphrase is lost is not a backup, so the system refuses to start until
the kit is confirmed saved.

## Day to day

Nothing. The console shows one card:

- **Last verified restore** — the number that matters. It means "we
  actually restored the data and every module could decrypt it."
- If the card is red, it says what is wrong in one sentence and has one
  button. The most common cause is an expired card on the Backblaze
  account.

## How you find out something is wrong

Three layers, and you should have all three:

1. **The console card goes red** — but only helps if someone looks.
2. **Email alerts** to the address you gave at setup: a failed backup, a
   failed restore drill, or a Backblaze account problem each send one
   email, repeated at most daily until fixed. Use **Alert settings →
   Send test alert** once to prove it reaches you. (Requires the
   appliance's installer to have configured a mail server; the card's
   "Failure alerts" row says if it can't send.)
3. **The dead-man check** — an outside service that notices when the
   appliance goes *silent*, which is the one thing a dead appliance
   can't email you about. Your installer or MSP wires this
   (docs/deadman-endpoint.md).

## When something goes wrong

| Situation | Do this |
|---|---|
| Deleted a file | Console → Restore → pick the date → download it |
| One module's data is bad | Console → Restore → restore that module (you'll type its name to confirm) |
| Appliance is dead / ransomware / disaster | Get the printed kit, a fresh Ubuntu machine, and follow the kit: `curl -fsSL https://get.kisaes.com/restore \| sudo bash` — see the restore runbook |

After any module or full restore, the console prints a short checklist
(reconnect Stripe, re-authorize bank feeds, and so on). External services
do not roll back with your data; the checklist is how you re-align them.

## Rules that keep this working

- The **Recovery Kit is the master key to all firm and client data.**
  Safe or safe-deposit box. Two named custodians. Losing it is a
  security incident — treat it like a stolen laptop, not a lost receipt.
- When the console announces a **new kit** (once a year): print it,
  confirm it, and only then shred the old one.
- The B2 bill is the firm's. If the card expires, backups stop and the
  console goes red until it's fixed.

## Deployment note (IT / MSP)

The backup service authenticates nobody. It must sit behind the
appliance console's authenticated proxy (appliance mode) or your own
authenticated ingress (standalone). Never publish port 4000 directly.
Docker access must go through a filtered socket proxy — see
`docs/socket-proxy.md` for the exact allowlist.
