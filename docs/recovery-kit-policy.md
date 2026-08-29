# Recovery Kit Handling Policy

Template for the firm's WISP. Replace bracketed items.

## What the kit is

The Recovery Kit is a single printed page holding the backup
passphrase, storage location, and restore key. **Anyone holding the kit
— or a photograph of it — can reconstruct the firm's complete dataset,
including client taxpayer information, on any computer.** It is treated
as an information asset of the highest sensitivity, equivalent to the
firm's complete client file.

## Rules

1. **Storage.** The kit is stored in [the office safe / safe-deposit box
   at ___]. Never: filing cabinets, desk drawers, shared drives, email,
   photos on phones, password managers.
2. **Custodians.** Two named custodians know its location and have
   access: [name 1], [name 2]. Recorded in the Vibe console and in this
   WISP. Update within 5 business days of personnel changes.
3. **Annual rotation.** Once a year the system issues a new kit and the
   console demands confirmation. The custodian prints the new kit,
   stores it, confirms the activation code, and **shreds the old kit
   only after the console shows the new generation active.** At nearly
   all times exactly one kit exists; the dangerous state is two.
4. **Loss or suspected exposure is a security incident.** A lost,
   photographed, or unaccounted-for kit triggers the incident-response
   plan: rotate the kit immediately (console → security → rotate),
   assess notification duties under the FTC Safeguards Rule amendment
   and state breach statutes, and document the assessment.
5. **Custodian departure** triggers an off-cycle rotation the same day.
6. **No vendor copy.** KisaesDevLab does not hold, escrow, or recover
   the kit or passphrase — deliberately, so no vendor breach can expose
   firm data. Consequence: **a lost kit plus a dead appliance means the
   off-site backup is permanently unreadable.** The rules above exist
   because of this.

## Why the design works this way

The passphrase is generated, never chosen, and never typed in daily
operation, so it cannot be weak, reused, or phished. The off-site copy
cannot be altered or deleted for 90 days even by an attacker with full
control of the appliance. The kit is the single deliberate point of
operator responsibility in the system — everything else is automatic.
