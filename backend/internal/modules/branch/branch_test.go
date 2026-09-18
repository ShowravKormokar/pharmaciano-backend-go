package branch

import (
	"errors"
	"testing"

	"backend/internal/platform/db"
	errs "backend/internal/errors"
)

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }

func TestUpdateRequestIsEmpty(t *testing.T) {
	if !(UpdateBranchRequest{}).IsEmpty() {
		t.Fatal("zero-value PATCH should be empty")
	}
	if (UpdateBranchRequest{Name: strptr("x")}).IsEmpty() {
		t.Fatal("PATCH with a set field should not be empty")
	}
	if (UpdateBranchRequest{IsDefault: boolptr(true)}).IsEmpty() {
		t.Fatal("PATCH setting is_default should not be empty")
	}
}

func TestApplyPatch(t *testing.T) {
	t.Run("nil leaves unchanged", func(t *testing.T) {
		phone := "0100"
		b := &Branch{Name: "Old", Phone: &phone, IsActive: true}
		applyPatch(b, &UpdateBranchRequest{})
		if b.Name != "Old" || b.Phone == nil || *b.Phone != "0100" || !b.IsActive {
			t.Fatalf("unexpected mutation: %+v", b)
		}
	})

	t.Run("set fields overwrite", func(t *testing.T) {
		b := &Branch{Name: "Old", IsActive: true}
		applyPatch(b, &UpdateBranchRequest{
			Name:     strptr("New"),
			IsActive: boolptr(false),
			City:     strptr("Dhaka"),
		})
		if b.Name != "New" || b.IsActive || b.City == nil || *b.City != "Dhaka" {
			t.Fatalf("patch not applied: %+v", b)
		}
	})
}

func TestApplyReplaceNullsOmittedFields(t *testing.T) {
	existingCity := "Dhaka"
	b := &Branch{
		Code:     "MAIN", // must be preserved (identity)
		Name:     "Old",
		City:     &existingCity,
		IsActive: true,
	}
	applyReplace(b, &ReplaceBranchRequest{
		Name:     "Replaced",
		IsActive: false,
		// City omitted → must become nil on a full replace.
	})
	if b.Code != "MAIN" {
		t.Fatalf("code must be immutable across replace, got %q", b.Code)
	}
	if b.Name != "Replaced" {
		t.Fatalf("name not replaced: %q", b.Name)
	}
	if b.City != nil {
		t.Fatalf("omitted optional field should be nulled, got %v", *b.City)
	}
	if b.IsActive {
		t.Fatal("is_active should have been replaced with false")
	}
}

func TestDerefBool(t *testing.T) {
	if got := derefBool(nil, true); got != true {
		t.Fatalf("nil should fall back to default, got %v", got)
	}
	if got := derefBool(boolptr(false), true); got != false {
		t.Fatalf("non-nil should win over default, got %v", got)
	}
}

func TestBuildOrderBy(t *testing.T) {
	tests := []struct {
		name string
		sort string
		want string
	}{
		{"empty falls back", "", "ORDER BY code ASC, id ASC"},
		{"single asc", "name", "ORDER BY name ASC, id ASC"},
		{"single desc", "-created_at", "ORDER BY created_at DESC, id ASC"},
		{"multi", "is_active,-name", "ORDER BY is_active ASC, name DESC, id ASC"},
		// A column not on the allow-list must be rejected → deterministic default,
		// never interpolated into SQL (injection safety).
		{"disallowed falls back", "code; DROP TABLE branches", "ORDER BY code ASC, id ASC"},
		{"unknown column falls back", "password", "ORDER BY code ASC, id ASC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildOrderBy(tc.sort); got != tc.want {
				t.Fatalf("buildOrderBy(%q) = %q, want %q", tc.sort, got, tc.want)
			}
		})
	}
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
