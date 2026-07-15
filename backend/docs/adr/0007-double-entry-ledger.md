# ADR 0007 — Double-Entry Ledger Inside the App

- **Status:** Accepted
- **Date:** 2026-07-15
- **Deciders:** Backend Lead, Finance stakeholder
- **Related:** ADR-0001, ADR-0002

---

## Context

Pharmaciano ERP is a financial system: every sale, purchase, payment, transfer
and expiry write-off must reconcile at the end of the day, month and year.
The owner wants:

- Trial balance, P&L, balance sheet, and cash-flow reports.
- Auditor-friendly: every movement traceable to a source event.
- Correction workflow that never edits history (reversing entries only).
- Per-branch views and org-wide roll-ups.

Options considered:

1. **Ad-hoc arithmetic** — sum sales.total, subtract purchases.total, hope.
2. **Single-entry ledger** — one row per money movement.
3. **Full double-entry ledger** — every event posts a balanced journal
   (debits = credits) against a chart of accounts.
4. **Outsource to QuickBooks/Zoho Books via API.**

## Decision

We build a **first-class double-entry ledger** inside Pharmaciano ERP.

Tables:

- `chart_of_accounts` — hierarchical, seeded with a standard COA
  (Assets / Liabilities / Equity / Revenue / Expenses), per organization.
- `journals` — one row per accounting event, immutable, references the
  domain source (module + id).
- `journal_lines` — the individual debits and credits; sum(debit) =
  sum(credit) is enforced by a `BEFORE INSERT/UPDATE` trigger on the
  journal row.
- `account_balances` — monthly snapshots per (org, branch, account, year,
  month), materialised for fast reporting.

Auto-posting map (representative):

| Event | Debit | Credit |
|---|---|---|
| Cash sale | Cash / Bank | Sales Revenue + VAT Payable |
| Credit sale | Accounts Receivable | Sales Revenue + VAT Payable |
| COGS on sale | Cost of Goods Sold | Inventory |
| Purchase received | Inventory | Accounts Payable |
| Purchase payment | Accounts Payable | Cash / Bank |
| Sales return | Sales Revenue + VAT | Cash / AR |
| Warehouse transfer | Stock-in-Transit → Inventory | Inventory → Stock-in-Transit |
| Expiry write-off | Expired Stock Loss | Inventory |
| Manual expense | Expense account | Cash / Bank |

## Rationale

- Any real ERP that touches money **must** run double-entry, or reports lie.
- Immutable journals give us a proper audit trail without inventing a new
  concept.
- Reversing entries make corrections safe: we never edit a historical row.
- Materialised balances keep report queries fast even with millions of
  journal lines.

Outsourcing to a third-party accounting API was rejected because:

- Adds a critical external dependency to every checkout.
- Sync failures corrupt the source of truth.
- Cross-branch consolidation is easier when the ledger is local.

Single-entry was rejected because it cannot produce a balance sheet.

## Enforcement

- The ledger has a public service interface:
  `ledger.Post(ctx, event, lines []Line, meta) (journalID, error)`.
- Domain services (sale, purchase, transfer, expiry) call `ledger.Post`
  **inside the same transaction** as their write. If the ledger post fails,
  the whole business transaction rolls back.
- A `BEFORE INSERT` trigger on `journals` recomputes `total_debit` and
  `total_credit` from `journal_lines` and refuses to commit if they differ.
- No `UPDATE` or `DELETE` on `journals` or `journal_lines`. Corrections
  create a new reversing journal with `is_reversed = true` on both entries.

## Reporting

- Reports read from `account_balances` (fast) and `journals` (drill-down).
- A cron job (Asynq, `low` queue) recomputes balances nightly to catch
  reconciliation drift; discrepancies alert with an anomaly report.
- Fiscal calendar defaults to Bangladesh's July–June year but is
  configurable per organization.

## Consequences

### Positive

- Real, auditable financial reporting.
- Ledger is a single source of truth. If the ledger and the sales table
  disagree, the ledger wins by convention and we investigate.
- Reversing entries mean the audit trail is complete.

### Negative

- Every domain event has extra work at write time (the ledger post). We
  measured this at ~1 ms per journal on the target hardware — well within
  the POS latency budget.
- The team must learn accounting basics. The onboarding doc includes a short
  primer.
- Migrations to the chart of accounts require care. The COA is seeded on
  first boot and extended, never renumbered.

## Guardrails

- No domain code sets balances directly. Only `ledger.Post` writes ledger
  rows.
- Every new domain event that touches money **must** ship with its auto-post
  mapping in the same PR, plus an integration test that verifies the
  journal balances.

## References

- Martin Kleppmann, *Designing Data-Intensive Applications*, Chapter 11
  (event-based accounting).
- Square Engineering, *Immutable Ledger* blog post.
- Wikipedia: Double-entry bookkeeping.