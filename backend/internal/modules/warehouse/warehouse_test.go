package warehouse

import (
	"errors"
	"testing"

	"backend/internal/platform/db"
	errs "backend/internal/errors"
)

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }
func intptr(i int) *int       { return &i }

func TestUpdateRequestIsEmpty(t *testing.T) {
	if !(UpdateWarehouseRequest{}).IsEmpty() {
		t.Fatal("zero-value PATCH should be empty")
	}
	if (UpdateWarehouseRequest{Name: strptr("x")}).IsEmpty() {
		t.Fatal("PATCH with a set field should not be empty")
	}
	// is_main is the invariant-bearing flag: a PATCH that only toggles it must
	// still be treated as a real change, not a no-op read.
	if (UpdateWarehouseRequest{IsMain: boolptr(true)}).IsEmpty() {
		t.Fatal("PATCH setting is_main should not be empty")
	}
	if (UpdateWarehouseRequest{Capacity: intptr(0)}).IsEmpty() {
		t.Fatal("PATCH setting capacity to zero should not be empty")
	}
}

func TestApplyPatch(t *testing.T) {
	t.Run("nil leaves unchanged", func(t *testing.T) {
		loc := "Aisle 1"
		w := &Warehouse{Name: "Old", Location: &loc, Capacity: intptr(100), IsActive: true}
		applyPatch(w, &UpdateWarehouseRequest{})
		if w.Name != "Old" || w.Location == nil || *w.Location != "Aisle 1" ||
			w.Capacity == nil || *w.Capacity != 100 || !w.IsActive {
			t.Fatalf("unexpected mutation: %+v", w)
		}
	})

	t.Run("set fields overwrite", func(t *testing.T) {
		w := &Warehouse{Name: "Old", IsActive: true, IsMain: false}
		applyPatch(w, &UpdateWarehouseRequest{
			Name:     strptr("New"),
			IsActive: boolptr(false),
			IsMain:   boolptr(true),
			Location: strptr("Dock B"),
			Capacity: intptr(500),
		})
		if w.Name != "New" || w.IsActive || !w.IsMain ||
			w.Location == nil || *w.Location != "Dock B" ||
			w.Capacity == nil || *w.Capacity != 500 {
			t.Fatalf("patch not applied: %+v", w)
		}
	})

	t.Run("branch and code are immutable via patch", func(t *testing.T) {
		// applyPatch has no access to branch_id/code by construction; this test
		// documents that the DTO exposes no such fields, so a patch can never
		// move a warehouse between branches or rename its code.
		w := &Warehouse{Code: "WH-1"}
		applyPatch(w, &UpdateWarehouseRequest{Name: strptr("x")})
		if w.Code != "WH-1" {
			t.Fatalf("code must be immutable across patch, got %q", w.Code)
		}
	})
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
		{"disallowed falls back", "code; DROP TABLE warehouses", "ORDER BY code ASC, id ASC"},
		{"unknown column falls back", "capacity", "ORDER BY code ASC, id ASC"},
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
