package organization

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

// ctxWithScope builds a request context carrying the tenant scope the tenant
// middleware would have set, so the pure authorization logic can be tested
// without a live middleware chain.
func ctxWithScope(orgID uuid.UUID, role string) context.Context {
	return appctx.WithPrincipal(context.Background(), appctx.Principal{
		UserID:   uuid.New(),
		OrgID:    orgID,
		RoleName: role,
	})
}

func TestAuthorizeScope(t *testing.T) {
	own := uuid.New()
	other := uuid.New()

	tests := []struct {
		name    string
		ctx     context.Context
		target  uuid.UUID
		wantErr bool
	}{
		{
			name:    "own org is allowed",
			ctx:     ctxWithScope(own, "MANAGER"),
			target:  own,
			wantErr: false,
		},
		{
			name:    "other org is hidden as not found",
			ctx:     ctxWithScope(own, "MANAGER"),
			target:  other,
			wantErr: true,
		},
		{
			name:    "super admin may address any org",
			ctx:     ctxWithScope(own, constants.RoleSuperAdmin),
			target:  other,
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := authorizeScope(tc.ctx, tc.target)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got nil")
				}
				// Cross-tenant access must look identical to a missing resource:
				// NOT_FOUND, never FORBIDDEN, so existence can't be probed.
				if !errs.Has(err, errs.CodeNotFound) {
					t.Fatalf("want CodeNotFound, got %s", errs.CodeOf(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestUpdateRequestIsEmpty(t *testing.T) {
	if !(UpdateOrganizationRequest{}).IsEmpty() {
		t.Fatal("zero-value request should be empty")
	}
	name := "Acme Pharma"
	if (UpdateOrganizationRequest{Name: &name}).IsEmpty() {
		t.Fatal("request with a set field should not be empty")
	}
}

func TestApplyUpdate(t *testing.T) {
	str := func(s string) *string { return &s }

	t.Run("nil fields leave values unchanged", func(t *testing.T) {
		existing := "old@x.com"
		o := &Organization{Name: "Old Name", ContactEmail: &existing}
		applyUpdate(o, &UpdateOrganizationRequest{}) // all nil
		if o.Name != "Old Name" {
			t.Fatalf("Name changed unexpectedly: %q", o.Name)
		}
		if o.ContactEmail == nil || *o.ContactEmail != "old@x.com" {
			t.Fatalf("ContactEmail changed unexpectedly: %v", o.ContactEmail)
		}
	})

	t.Run("non-nil fields overwrite", func(t *testing.T) {
		o := &Organization{Name: "Old Name"}
		applyUpdate(o, &UpdateOrganizationRequest{
			Name:         str("New Name"),
			ContactEmail: str("new@x.com"),
			Currency:     str("USD"),
		})
		if o.Name != "New Name" {
			t.Fatalf("Name not applied: %q", o.Name)
		}
		if o.ContactEmail == nil || *o.ContactEmail != "new@x.com" {
			t.Fatalf("ContactEmail not applied: %v", o.ContactEmail)
		}
		if o.Currency != "USD" {
			t.Fatalf("Currency not applied: %q", o.Currency)
		}
	})

	t.Run("empty string blanks a free-text field", func(t *testing.T) {
		existing := "0123"
		o := &Organization{ContactPhone: &existing}
		applyUpdate(o, &UpdateOrganizationRequest{ContactPhone: str("")})
		if o.ContactPhone == nil || *o.ContactPhone != "" {
			t.Fatalf("ContactPhone should be blanked, got %v", o.ContactPhone)
		}
	})
}

func TestMapReadErr(t *testing.T) {
	t.Run("no rows becomes not found", func(t *testing.T) {
		err := mapReadErr(db.ErrNoRows)
		if !errs.Has(err, errs.CodeNotFound) {
			t.Fatalf("want CodeNotFound, got %s", errs.CodeOf(err))
		}
	})

	t.Run("other errors become database errors", func(t *testing.T) {
		err := mapReadErr(errors.New("connection reset"))
		if !errs.Has(err, errs.CodeDatabaseError) {
			t.Fatalf("want CodeDatabaseError, got %s", errs.CodeOf(err))
		}
	})
}
