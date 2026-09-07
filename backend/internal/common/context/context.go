// Package appctx centralises every value we stash on request context. Using
// typed keys (unexported) guarantees no collisions with third-party packages
// and no accidental overwrites. Named "appctx" (not "context") so importers
// can still `import "context"` for the stdlib package in the same file
// without an alias.
package appctx

import (
	"context"
	"strings"
	"time"

	"backend/internal/common/constants"
	"backend/internal/common/enums"

	"github.com/google/uuid"
)

// key is the unexported context-key type.
type key int

const (
	keyRequestID key = iota + 1
	keyStartTime
	keyUserID
	keyOrgID
	keyBranchID
	keyBranchIDs
	keySessionID
	keyRoleName
	keyPermissions
	keyStage
	keyStatus
	keyAuthzVersion
	keyClientIP
	keyUserAgent
	keyDeviceFP
	keyLocale
	keyTraceID
)

// Principal is the compact identity carried on every authenticated request.
// It is populated by the `auth` middleware after JWT validation.
//
// Branch semantics (ADR §19):
//   - BranchID  — the user's *home* branch (a single id, or nil for org-wide).
//     Legacy single-branch principals keep working through this field.
//   - BranchIDs — the *effective subset* the principal may act on. For an
//     org-wide user this is empty (nil). For a branch-bound user it contains
//     the single home branch. For a multi-branch principal (regional
//     manager, etc.) it contains every branch they are currently assigned
//     to. The list is server-derived from user_branch_assignments and is
//     baked into the JWT, so the client never gets to expand it.
type Principal struct {
	UserID       uuid.UUID
	OrgID        uuid.UUID
	BranchID     *uuid.UUID   // home / primary branch; nil for org-wide
	BranchIDs    []uuid.UUID  // effective subset; nil/empty = org-wide scope
	SessionID    uuid.UUID
	RoleName     string       // canonical role (SUPER_ADMIN, MANAGER, ...)
	Permissions  []string
	Stage        enums.UserStage
	Status       enums.UserStatus
	AuthzVersion int64
}

// internals
const maxContextStringLen = 512

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxContextStringLen {
		s = s[:maxContextStringLen]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// Setters — used by middleware only.
func WithRequestID(ctx context.Context, id string) context.Context {
	id = sanitize(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, keyRequestID, id)
}

func WithStartTime(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, keyStartTime, t)
}

// WithPrincipal attaches every field of p to ctx. Non-mutating: returns a
// new context, does not touch p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	ctx = context.WithValue(ctx, keyUserID, p.UserID)
	ctx = context.WithValue(ctx, keyOrgID, p.OrgID)
	if p.BranchID != nil {
		ctx = context.WithValue(ctx, keyBranchID, *p.BranchID)
	}
	if len(p.BranchIDs) > 0 {
		ids := make([]uuid.UUID, len(p.BranchIDs))
		copy(ids, p.BranchIDs)
		ctx = context.WithValue(ctx, keyBranchIDs, ids)
	}
	ctx = context.WithValue(ctx, keySessionID, p.SessionID)
	ctx = context.WithValue(ctx, keyRoleName, p.RoleName)

	if len(p.Permissions) > 0 {
		perms := make([]string, len(p.Permissions))
		copy(perms, p.Permissions)
		ctx = context.WithValue(ctx, keyPermissions, perms)
	}

	ctx = context.WithValue(ctx, keyStage, p.Stage)
	ctx = context.WithValue(ctx, keyStatus, p.Status)
	ctx = context.WithValue(ctx, keyAuthzVersion, p.AuthzVersion)
	return ctx
}

func WithClientIP(ctx context.Context, ip string) context.Context {
	ip = sanitize(ip)
	if ip == "" {
		return ctx
	}
	return context.WithValue(ctx, keyClientIP, ip)
}

func WithUserAgent(ctx context.Context, ua string) context.Context {
	ua = sanitize(ua)
	if ua == "" {
		return ctx
	}
	return context.WithValue(ctx, keyUserAgent, ua)
}

func WithDeviceFP(ctx context.Context, fp string) context.Context {
	fp = sanitize(fp)
	if fp == "" {
		return ctx
	}
	return context.WithValue(ctx, keyDeviceFP, fp)
}

func WithLocale(ctx context.Context, loc string) context.Context {
	loc = sanitize(loc)
	if loc == "" {
		return ctx
	}
	return context.WithValue(ctx, keyLocale, loc)
}

// Getters — safe: return zero-value if missing.
func RequestID(ctx context.Context) string { s, _ := ctx.Value(keyRequestID).(string); return s }

func StartTime(ctx context.Context) time.Time {
	t, _ := ctx.Value(keyStartTime).(time.Time)
	return t
}

func UserID(ctx context.Context) uuid.UUID    { v, _ := ctx.Value(keyUserID).(uuid.UUID); return v }
func OrgID(ctx context.Context) uuid.UUID     { v, _ := ctx.Value(keyOrgID).(uuid.UUID); return v }
func SessionID(ctx context.Context) uuid.UUID { v, _ := ctx.Value(keySessionID).(uuid.UUID); return v }
func RoleName(ctx context.Context) string     { v, _ := ctx.Value(keyRoleName).(string); return v }

// Permissions returns a defensive copy of the resolved permission set —
// mutating the returned slice never affects the context's stored value
// (see the matching copy made in WithPrincipal for the other direction).
func Permissions(ctx context.Context) []string {
	v, _ := ctx.Value(keyPermissions).([]string)
	if v == nil {
		return nil
	}
	out := make([]string, len(v))
	copy(out, v)
	return out
}

func BranchID(ctx context.Context) *uuid.UUID {
	if v, ok := ctx.Value(keyBranchID).(uuid.UUID); ok {
		return &v
	}
	return nil
}

// BranchIDs returns the principal's effective branch subset (a defensive
// copy). For an org-wide principal the slice is nil; for a branch-bound
// principal it carries every branch id the principal may act on (one entry
// for a single-branch user, many for a multi-branch user). The slice is
// always nil-safe to range over.
func BranchIDs(ctx context.Context) []uuid.UUID {
	v, _ := ctx.Value(keyBranchIDs).([]uuid.UUID)
	if v == nil {
		return nil
	}
	out := make([]uuid.UUID, len(v))
	copy(out, v)
	return out
}

func Stage(ctx context.Context) enums.UserStage {
	v, _ := ctx.Value(keyStage).(enums.UserStage)
	return v
}

func Status(ctx context.Context) enums.UserStatus {
	v, _ := ctx.Value(keyStatus).(enums.UserStatus)
	return v
}
func AuthzVersion(ctx context.Context) int64 { v, _ := ctx.Value(keyAuthzVersion).(int64); return v }

func ClientIP(ctx context.Context) string  { v, _ := ctx.Value(keyClientIP).(string); return v }
func UserAgent(ctx context.Context) string { v, _ := ctx.Value(keyUserAgent).(string); return v }
func DeviceFP(ctx context.Context) string  { v, _ := ctx.Value(keyDeviceFP).(string); return v }
func Locale(ctx context.Context) string    { v, _ := ctx.Value(keyLocale).(string); return v }
func TraceID(ctx context.Context) string   { v, _ := ctx.Value(keyTraceID).(string); return v }

func CurrentPrincipal(ctx context.Context) (Principal, bool) {
	uid := UserID(ctx)
	if uid == uuid.Nil {
		return Principal{}, false
	}
	return Principal{
		UserID:       uid,
		OrgID:        OrgID(ctx),
		BranchID:     BranchID(ctx),
		BranchIDs:    BranchIDs(ctx),
		SessionID:    SessionID(ctx),
		RoleName:     RoleName(ctx),
		Permissions:  Permissions(ctx),
		Stage:        Stage(ctx),
		Status:       Status(ctx),
		AuthzVersion: AuthzVersion(ctx),
	}, true
}

// Convenience predicates
// IsAuthenticated reports whether a Principal is on the context.
func IsAuthenticated(ctx context.Context) bool {
	return UserID(ctx) != uuid.Nil
}

func HasPermission(ctx context.Context, module, action string) bool {
	want := module + ":" + action
	moduleWildcard := module + constants.ModuleWildcardSuffix
	for _, p := range Permissions(ctx) {
		if p == want || p == moduleWildcard || p == constants.PermissionWildcard {
			return true
		}
	}
	return false
}

// IsSuperAdmin reports whether the current role is SUPER_ADMIN.
func IsSuperAdmin(ctx context.Context) bool {
	return RoleName(ctx) == constants.RoleSuperAdmin
}

// BranchScope reports the effective branch scope of the request — the
// subset of branches the principal may target. Semantics (ADR §19):
//
//   - Org-wide principal (SUPER_ADMIN/ADMIN) with no requested selector →
//     nil (caller filters "no branch" / org-wide queries).
//   - Org-wide principal with an X-Branch-IDs selector → the selector
//     intersected with the org's branches (the org is bounded by
//     appctx.OrgID, so a foreign branch id can never enter here).
//   - Branch-bound principal with no selector → the principal's
//     BranchIDs (single entry for a legacy user, many for a multi-branch
//     regional role).
//   - Branch-bound principal with a selector → the selector intersected
//     with the principal's BranchIDs (defense-in-depth: a client can never
//     widen beyond the subset baked into the JWT).
//
// The bool is false when the principal has NO branch scope and no selector
// either (caller must treat the request as org-wide).
func BranchScope(ctx context.Context) (effective []uuid.UUID, orgWide bool) {
	assigned := BranchIDs(ctx)
	if len(assigned) == 0 {
		// Org-wide role (SUPER_ADMIN / ADMIN) with no branch in the token:
		// caller decides between org-wide queries and a selector-driven subset.
		return nil, true
	}
	// Branch-bound: must narrow to the assigned set; no client escalation.
	return assigned, false
}

func IsActive(ctx context.Context) bool {
	return Status(ctx).CanLogin()
}

// Elapsed returns the time since StartTime; 0 if unset. Useful in access logs.
func Elapsed(ctx context.Context) time.Duration {
	t := StartTime(ctx)
	if t.IsZero() {
		return 0
	}
	return time.Since(t)
}
