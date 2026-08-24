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
	keySessionID
	keyRoleName
	keyPermissions
	keyStage
	keyStatus
	keyClientIP
	keyUserAgent
	keyDeviceFP
	keyLocale
	keyTraceID
)

// Principal is the compact identity carried on every authenticated request.
// It is populated by the `auth` middleware after JWT validation.
type Principal struct {
	UserID      uuid.UUID
	OrgID       uuid.UUID
	BranchID    *uuid.UUID // nil = org-wide user (SUPER_ADMIN)
	SessionID   uuid.UUID
	RoleName    string // canonical role (SUPER_ADMIN, MANAGER, ...)
	Permissions []string
	Stage       enums.UserStage
	Status      enums.UserStatus
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
	ctx = context.WithValue(ctx, keySessionID, p.SessionID)
	ctx = context.WithValue(ctx, keyRoleName, p.RoleName)

	if len(p.Permissions) > 0 {
		perms := make([]string, len(p.Permissions))
		copy(perms, p.Permissions)
		ctx = context.WithValue(ctx, keyPermissions, perms)
	}

	ctx = context.WithValue(ctx, keyStage, p.Stage)
	ctx = context.WithValue(ctx, keyStatus, p.Status)
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

func Stage(ctx context.Context) enums.UserStage {
	v, _ := ctx.Value(keyStage).(enums.UserStage)
	return v
}

func Status(ctx context.Context) enums.UserStatus {
	v, _ := ctx.Value(keyStatus).(enums.UserStatus)
	return v
}

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
		UserID:      uid,
		OrgID:       OrgID(ctx),
		BranchID:    BranchID(ctx),
		SessionID:   SessionID(ctx),
		RoleName:    RoleName(ctx),
		Permissions: Permissions(ctx),
		Stage:       Stage(ctx),
		Status:      Status(ctx),
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
