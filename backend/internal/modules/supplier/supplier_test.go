package supplier

import (
	"errors"
	"testing"

	"backend/internal/platform/db"
	errs "backend/internal/errors"
)

func strptr(s string) *string     { return &s }
func boolptr(b bool) *bool       { return &b }
func intptr(i int) *int         { return &i }
func floatptr(f float64) *float64 { return &f }

func TestUpdateRequestIsEmpty(t *testing.T) {
	if !(UpdateSupplierRequest{}).IsEmpty() {
		t.Fatal("zero-value PATCH should be empty")
	}
	if (UpdateSupplierRequest{Name: strptr("x")}).IsEmpty() {
		t.Fatal("PATCH with a set field should not be empty")
	}
	if (UpdateSupplierRequest{IsActive: boolptr(true)}).IsEmpty() {
		t.Fatal("PATCH setting is_active should not be empty")
	}
	if (UpdateSupplierRequest{CreditLimit: floatptr(0)}).IsEmpty() {
		t.Fatal("PATCH setting credit_limit to zero should not be empty")
	}
	if (UpdateSupplierRequest{CreditPeriodDays: intptr(0)}).IsEmpty() {
		t.Fatal("PATCH setting credit_period_days to zero should not be empty")
	}
}

func TestApplyPatch(t *testing.T) {
	t.Run("nil leaves unchanged", func(t *testing.T) {
		tel := "123"
		s := &Supplier{Name: "Old", Phone: &tel, IsActive: true}
		NewMapper().ApplyPatch(s, &UpdateSupplierRequest{})
		if s.Name != "Old" || s.Phone == nil || *s.Phone != "123" || !s.IsActive {
			t.Fatalf("unexpected mutation: %+v", s)
		}
	})

	t.Run("set fields overwrite", func(t *testing.T) {
		s := &Supplier{Name: "Old", IsActive: true}
		NewMapper().ApplyPatch(s, &UpdateSupplierRequest{
			Name:             strptr("New"),
			IsActive:         boolptr(false),
			ContactPerson:    strptr("John"),
			Phone:            strptr("555"),
			Email:            strptr("a@b.com"),
			Address:          strptr("Addr"),
			City:             strptr("City"),
			Country:          strptr("Country"),
			DrugLicenseNo:    strptr("DL"),
			TIN:              strptr("TIN"),
			CreditLimit:      floatptr(1000.5),
			CreditPeriodDays: intptr(30),
		})
		if s.Name != "New" || s.IsActive || s.ContactPerson == nil || *s.ContactPerson != "John" ||
			s.Phone == nil || *s.Phone != "555" || s.Email == nil || *s.Email != "a@b.com" ||
			s.Address == nil || *s.Address != "Addr" || s.City == nil || *s.City != "City" ||
			s.Country == nil || *s.Country != "Country" || s.DrugLicenseNo == nil || *s.DrugLicenseNo != "DL" ||
			s.TIN == nil || *s.TIN != "TIN" || s.CreditLimit == nil || *s.CreditLimit != 1000.5 ||
			s.CreditPeriodDays == nil || *s.CreditPeriodDays != 30 {
			t.Fatalf("patch not applied: %+v", s)
		}
	})

	t.Run("code and organization_id are immutable via patch", func(t *testing.T) {
		s := &Supplier{Code: "SUP-1", OrganizationID: "org1"}
		NewMapper().ApplyPatch(s, &UpdateSupplierRequest{Name: strptr("x")})
		if s.Code != "SUP-1" || s.OrganizationID != "org1" {
			t.Fatalf("code/org must be immutable across patch, got code=%q org=%q", s.Code, s.OrganizationID)
		}
	})
}

func TestBuildOrderBy(t *testing.T) {
	tests := []struct {
		name string
		sort string
		want string
	}{
		{"empty falls back", "", "ORDER BY name ASC, id ASC"},
		{"single asc", "code", "ORDER BY code ASC, id ASC"},
		{"single desc", "-created_at", "ORDER BY created_at DESC, id ASC"},
		{"multi", "is_active,-name", "ORDER BY is_active ASC, name DESC, id ASC"},
		{"disallowed falls back", "code; DROP TABLE suppliers", "ORDER BY name ASC, id ASC"},
		{"unknown column falls back", "credit_limit", "ORDER BY name ASC, id ASC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildOrderBy(tc.sort); got != tc.want {
				t.Fatalf("buildOrderBy(%q) = %q, want %q", tc.sort, got, tc.want)
			}
		})
	}
}

func TestMapReadErr(t *testing.T) {
	t.Run("no rows becomes not found", func(t *testing.T) {
		if err := mapReadErr(db.ErrNoRows); !errs.Has(err, errs.CodeNotFound) {
			t.Fatalf("want CodeNotFound, got %s", errs.CodeOf(err))
		}
	})
	t.Run("generic becomes database error", func(t *testing.T) {
		if err := mapReadErr(errors.New("boom")); !errs.Has(err, errs.CodeDatabaseError) {
			t.Fatalf("want CodeDatabaseError, got %s", errs.CodeOf(err))
		}
	})
}

func TestMapWriteErr(t *testing.T) {
	t.Run("no rows becomes not found", func(t *testing.T) {
		if err := mapWriteErr(db.ErrNoRows); !errs.Has(err, errs.CodeNotFound) {
			t.Fatalf("want CodeNotFound, got %s", errs.CodeOf(err))
		}
	})
	t.Run("generic becomes database error", func(t *testing.T) {
		if err := mapWriteErr(errors.New("boom")); !errs.Has(err, errs.CodeDatabaseError) {
			t.Fatalf("want CodeDatabaseError, got %s", errs.CodeOf(err))
		}
	})
}