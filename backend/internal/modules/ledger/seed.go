package ledger

import (
	"context"

	"github.com/google/uuid"

	"backend/internal/platform/db"
)

// SeedSystemAccounts inserts the minimal default chart of accounts for an
// organization if none exist. Idempotent: skips when accounts already present.
func SeedSystemAccounts(ctx context.Context, database *db.DB, orgID uuid.UUID) error {
	repo := NewRepository(database)
	existing, err := repo.ListAccounts(ctx, orgID, nil, "", "", 1, 0)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	defaults := []struct {
		code        string
		name        string
		accountType string
		normalSide  string
		isSystem    bool
	}{
		{"1000", "Cash", "asset", "debit", true},
		{"1100", "Accounts Receivable", "asset", "debit", true},
		{"1200", "Inventory", "asset", "debit", true},
		{"2000", "Accounts Payable", "liability", "credit", true},
		{"2100", "Tax Payable", "liability", "credit", true},
		{"3000", "Equity", "equity", "credit", true},
		{"4000", "Sales Revenue", "revenue", "credit", true},
		{"5000", "Cost of Goods Sold", "expense", "debit", true},
	}
	for _, d := range defaults {
		a := &ChartOfAccount{
			OrganizationID: orgID,
			Code:           d.code,
			Name:           d.name,
			AccountType:    AccountType(d.accountType),
			NormalSide:     NormalSide(d.normalSide),
			IsSystem:       d.isSystem,
			IsActive:       true,
			IsPostable:     true,
		}
		if _, err := repo.InsertAccount(ctx, a); err != nil {
			return err
		}
	}
	return nil
}
