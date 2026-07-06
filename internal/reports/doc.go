// Package reports owns P&L, VAT return figures, filing calendar, and export pack read models.
//
// VAT Box 4 is intentionally limited in v1: input VAT is read from manual
// quarterly adjustment entries posted to the ledger VAT control account with
// source_module=reports and source_ref prefixed manual-input-vat:. Expense
// recoding does not carry a VAT portion until the receipts feature exists.
package reports
