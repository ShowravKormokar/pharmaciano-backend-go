package user

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"backend/internal/common/enums"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool     { return &b }

func TestUpdateRequestIsEmpty(t *testing.T) {
	if !(UpdateUserRequest{}).IsEmpty() {
		t.Fatal("zero-value PATCH should be empty")
	}
	if (UpdateUserRequest{Phone: strptr("0100")}).IsEmpty() {
		t.Fatal("PATCH with a user field set should not be empty")
	}
	if (UpdateUserRequest{FirstName: strptr("Ada")}).IsEmpty() {
		t.Fatal("PATCH with a profile field set should not be empty")
	}
}

func TestTouches(t *testing.T) {
	t.Run("user-only patch", func(t *testing.T) {
		r := UpdateUserRequest{Phone: strptr("0100")}
		if !r.touchesUser() {
			t.Fatal("phone change should touch the user")
		}
		if r.touchesProfile() {
			t.Fatal("phone change must not touch the profile")
		}
	})
	t.Run("profile-only patch", func(t *testing.T) {
		r := UpdateUserRequest{DisplayName: strptr("Ada L.")}
		if r.touchesUser() {
			t.Fatal("display name must not touch the user")
		}
		if !r.touchesProfile() {
			t.Fatal("display name should touch the profile")
		}
	})
	t.Run("empty patch touches nothing", func(t *testing.T) {
		r := UpdateUserRequest{}
		if r.touchesUser() || r.touchesProfile() {
			t.Fatal("empty patch should touch neither")
		}
	})
}

func TestApplyUserPatch(t *testing.T) {
	t.Run("nil fields leave the user unchanged", func(t *testing.T) {
		phone := "0100"
		u := &User{Username: strptr("ada"), Phone: &phone}
		if err := applyUserPatch(u, &UpdateUserRequest{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.Username == nil || *u.Username != "ada" || u.Phone == nil || *u.Phone != "0100" {
			t.Fatalf("unexpected mutation: %+v", u)
		}
	})

	t.Run("set fields overwrite", func(t *testing.T) {
		et := enums.EmploymentContract
		u := &User{Username: strptr("old")}
		err := applyUserPatch(u, &UpdateUserRequest{
			Username:       strptr("new"),
			Phone:          strptr("0199"),
			EmploymentType: &et,
			JoiningDate:    strptr("2023-02-01"),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.Username == nil || *u.Username != "new" {
			t.Fatalf("username not applied: %+v", u.Username)
		}
		if u.Phone == nil || *u.Phone != "0199" {
			t.Fatalf("phone not applied: %+v", u.Phone)
		}
		if u.EmploymentType == nil || *u.EmploymentType != enums.EmploymentContract {
			t.Fatalf("employment type not applied: %+v", u.EmploymentType)
		}
		if u.JoiningDate == nil || u.JoiningDate.Format(dateLayout) != "2023-02-01" {
			t.Fatalf("joining date not applied: %+v", u.JoiningDate)
		}
	})

	t.Run("malformed joining date is a validation error", func(t *testing.T) {
		u := &User{}
		err := applyUserPatch(u, &UpdateUserRequest{JoiningDate: strptr("31-12-2023")})
		if !errs.Has(err, errs.CodeValidationError) {
			t.Fatalf("want CodeValidationError, got %s", errs.CodeOf(err))
		}
	})
}

func TestApplyProfilePatch(t *testing.T) {
	t.Run("nil fields leave the profile unchanged", func(t *testing.T) {
		p := &UserProfile{FirstName: "Ada", LastName: "Lovelace"}
		if err := applyProfilePatch(p, &UpdateUserRequest{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.FirstName != "Ada" || p.LastName != "Lovelace" {
			t.Fatalf("unexpected mutation: %+v", p)
		}
	})

	t.Run("set fields overwrite", func(t *testing.T) {
		p := &UserProfile{FirstName: "Ada", LastName: "Lovelace"}
		err := applyProfilePatch(p, &UpdateUserRequest{
			FirstName:   strptr("Augusta"),
			DisplayName: strptr("Ada L."),
			DateOfBirth: strptr("1815-12-10"),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.FirstName != "Augusta" {
			t.Fatalf("first name not applied: %q", p.FirstName)
		}
		if p.LastName != "Lovelace" {
			t.Fatalf("last name should be untouched: %q", p.LastName)
		}
		if p.DisplayName == nil || *p.DisplayName != "Ada L." {
			t.Fatalf("display name not applied: %+v", p.DisplayName)
		}
		if p.DateOfBirth == nil || p.DateOfBirth.Format(dateLayout) != "1815-12-10" {
			t.Fatalf("dob not applied: %+v", p.DateOfBirth)
		}
	})

	t.Run("malformed dob is a validation error", func(t *testing.T) {
		p := &UserProfile{}
		err := applyProfilePatch(p, &UpdateUserRequest{DateOfBirth: strptr("not-a-date")})
		if !errs.Has(err, errs.CodeValidationError) {
			t.Fatalf("want CodeValidationError, got %s", errs.CodeOf(err))
		}
	})
}

func TestParseDate(t *testing.T) {
	t.Run("nil yields nil", func(t *testing.T) {
		got, err := parseDate(nil)
		if err != nil || got != nil {
			t.Fatalf("nil in → (nil,nil); got (%v,%v)", got, err)
		}
	})
	t.Run("empty string yields nil", func(t *testing.T) {
		got, err := parseDate(strptr(""))
		if err != nil || got != nil {
			t.Fatalf(`"" in → (nil,nil); got (%v,%v)`, got, err)
		}
	})
	t.Run("valid date parses", func(t *testing.T) {
		got, err := parseDate(strptr("2023-01-15"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || got.Format(dateLayout) != "2023-01-15" {
			t.Fatalf("parsed wrong value: %+v", got)
		}
	})
	t.Run("malformed date is a validation error", func(t *testing.T) {
		_, err := parseDate(strptr("2023/01/15"))
		if !errs.Has(err, errs.CodeValidationError) {
			t.Fatalf("want CodeValidationError, got %s", errs.CodeOf(err))
		}
	})
}

func TestBuildOrderBy(t *testing.T) {
	const def = "ORDER BY u.created_at DESC, u.id ASC"
	tests := []struct {
		name string
		sort string
		want string
	}{
		{"empty falls back", "", def},
		{"single asc", "email", "ORDER BY u.email ASC, u.id ASC"},
		{"single desc", "-created_at", "ORDER BY u.created_at DESC, u.id ASC"},
		{"multi", "status,-last_login_at", "ORDER BY u.status ASC, u.last_login_at DESC, u.id ASC"},
		// Not on the allow-list → deterministic default, never interpolated (injection safety).
		{"injection falls back", "email; DROP TABLE users", def},
		{"unknown column falls back", "password", def},
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
	unique := func(constraint string) error {
		return &pgconn.PgError{Code: "23505", ConstraintName: constraint}
	}
	tests := []struct {
		name string
		err  error
		want errs.Code
	}{
		{"no rows becomes not found", db.ErrNoRows, errs.CodeNotFound},
		{"duplicate email", unique("ux_users_email"), errs.CodeAlreadyExists},
		{"duplicate username", unique("ux_users_username"), errs.CodeAlreadyExists},
		{"duplicate employee code", unique("ux_users_emp_code"), errs.CodeAlreadyExists},
		{"duplicate profile", unique("user_profiles_user_id_key"), errs.CodeAlreadyExists},
		{"unknown unique index", unique("some_other_idx"), errs.CodeAlreadyExists},
		{"foreign key violation", &pgconn.PgError{Code: "23503"}, errs.CodeValidationError},
		{"generic becomes database error", errors.New("boom"), errs.CodeDatabaseError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapWriteErr(tc.err); !errs.Has(got, tc.want) {
				t.Fatalf("mapWriteErr(%v) → %s, want %s", tc.err, errs.CodeOf(got), tc.want)
			}
		})
	}
}

func TestMapWriteErrNamesTheField(t *testing.T) {
	err := mapWriteErr(&pgconn.PgError{Code: "23505", ConstraintName: "ux_users_email"})
	ae := errs.As(err)
	if ae == nil {
		t.Fatalf("expected an *AppError, got %T", err)
	}
	if ae.Message == "" || !strings.Contains(strings.ToLower(ae.Message), "email") {
		t.Fatalf("message should name the offending field, got %q", ae.Message)
	}
}

func TestMapReadErr(t *testing.T) {
	if got := mapReadErr(db.ErrNoRows); !errs.Has(got, errs.CodeNotFound) {
		t.Fatalf("no rows → want CodeNotFound, got %s", errs.CodeOf(got))
	}
	if got := mapReadErr(errors.New("boom")); !errs.Has(got, errs.CodeDatabaseError) {
		t.Fatalf("generic → want CodeDatabaseError, got %s", errs.CodeOf(got))
	}
}

func TestDerefHelpers(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		if got := derefStatus(nil, enums.UserStatusActive); got != enums.UserStatusActive {
			t.Fatalf("nil → default; got %s", got)
		}
		s := enums.UserStatusSuspended
		if got := derefStatus(&s, enums.UserStatusActive); got != enums.UserStatusSuspended {
			t.Fatalf("non-nil should win; got %s", got)
		}
	})
	t.Run("stage", func(t *testing.T) {
		if got := derefStage(nil, enums.UserStageUnverified); got != enums.UserStageUnverified {
			t.Fatalf("nil → default; got %s", got)
		}
		sg := enums.UserStageVerified
		if got := derefStage(&sg, enums.UserStageUnverified); got != enums.UserStageVerified {
			t.Fatalf("non-nil should win; got %s", got)
		}
	})
	t.Run("bool", func(t *testing.T) {
		if got := derefBool(nil, true); got != true {
			t.Fatalf("nil → default; got %v", got)
		}
		if got := derefBool(boolptr(false), true); got != false {
			t.Fatalf("non-nil should win; got %v", got)
		}
	})
}

func TestStatusReason(t *testing.T) {
	t.Run("prefers the caller-supplied note", func(t *testing.T) {
		got := statusReason(&ChangeStatusRequest{Status: enums.UserStatusSuspended, Reason: strptr("policy breach")})
		if got != "policy breach" {
			t.Fatalf("want the supplied reason, got %q", got)
		}
	})
	t.Run("blank note falls back to a derived reason", func(t *testing.T) {
		got := statusReason(&ChangeStatusRequest{Status: enums.UserStatusDeactivated, Reason: strptr("")})
		if got != "status_changed_to_deactivated" {
			t.Fatalf("blank note should derive from status, got %q", got)
		}
	})
	t.Run("nil note falls back to a derived reason", func(t *testing.T) {
		got := statusReason(&ChangeStatusRequest{Status: enums.UserStatusTerminated})
		if got != "status_changed_to_terminated" {
			t.Fatalf("nil note should derive from status, got %q", got)
		}
	})
}
