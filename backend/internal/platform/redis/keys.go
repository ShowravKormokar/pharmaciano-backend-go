package redis

import (
	"fmt"
	"github.com/google/uuid"
)

const KeyPrefix = "mc"

// Sessions and Tokens
func SessionKey(sessionID uuid.UUID) string {
	return fmt.Sprintf("%s:sess:%s", KeyPrefix, sessionID)
}

// SessionCacheKey is the versioned projection key for the Authenticate hot path
// (ADR §17). The trailing generation makes a revoked/rotated session's cached
// projection unusable at once: the row's security_generation counter advances
// on every revoke and the new lookup writes under a new key, so the old entry
// is naturally orphaned and the TTL reaps it.
func SessionCacheKey(sessionID uuid.UUID, generation int64) string {
	return fmt.Sprintf("%s:sess:%s:g%d", KeyPrefix, sessionID, generation)
}

func UserSessionsKey(userID uuid.UUID) string {
	return fmt.Sprintf("%s:sess:user:%s", KeyPrefix, userID)
}

func RefreshTokenKey(hash string) string {
	return fmt.Sprintf("%s:refresh:%s", KeyPrefix, hash)
}

func RefreshFamilyKey(familyID uuid.UUID) string {
	return fmt.Sprintf("%s:refresh:family:%s", KeyPrefix, familyID)
}

func AccessTokenBlacklistKey(jti string) string {
	return fmt.Sprintf("%s:jwt:blacklist:%s", KeyPrefix, jti)
}

// Login and Password
func LoginAttemptsByEmailKey(email string) string {
	return fmt.Sprintf("%s:login:email:%s", KeyPrefix, email)
}

func LoginAttemptsByIPKey(ip string) string {
	return fmt.Sprintf("%s:login:ip:%s", KeyPrefix, ip)
}

func PasswordResetKey(tokenHash string) string {
	return fmt.Sprintf("%s:pwreset:%s", KeyPrefix, tokenHash)
}

// Rate limiter

// RateLimitKey builds a per-policy key.
//
//	policy  — "public", "auth_write", "pos_checkout", ...
//	subject — user_id / ip / email depending on the policy
//	scope   — optional endpoint or route family (may be empty)
func RateLimitKey(policy, subject, scope string) string {
	if scope == "" {
		return fmt.Sprintf("%s:rl:%s:%s", KeyPrefix, policy, subject)
	}
	return fmt.Sprintf("%s:rl:%s:%s:%s", KeyPrefix, policy, subject, scope)
}

// Idepotency
func IdempotencyKey(userID uuid.UUID, key string) string {
	return fmt.Sprintf("%s:idem:%s:%s", KeyPrefix, userID, key)
}

// RBAC and catalog caches
func UserPermissionsKey(userID uuid.UUID) string {
	return fmt.Sprintf("%s:rbac:user:%s", KeyPrefix, userID)
}
// AuthorizationContextKey is the versioned projection key used by the RBAC
// authorization cache (ADR §25). It includes every server-side dimension that
// changes the resolved Access for a (user, org) pair — the user's durable
// authz_version and the org's rbac_generation — so a single bump on either
// side makes the cached projection unreachable without per-entry invalidation
// (ADR §30). A stale entry expires on its TTL.
func AuthorizationContextKey(orgID, userID uuid.UUID, authzVersion int64) string {
	return fmt.Sprintf("%s:authz:v1:org:%s:user:%s:uv:%d", KeyPrefix, orgID, userID, authzVersion)
}

// OrgRBACGenerationKey is the per-organization counter used by the RBAC cache
// for O(1) org-wide invalidation (ADR §30). A read-through cache populates it
// lazily; a write on role/permission changes advances it in the same tx.
func OrgRBACGenerationKey(orgID uuid.UUID) string {
	return fmt.Sprintf("%s:rbac:orggen:%s", KeyPrefix, orgID)
}
func RolePermissionsKey(roleID uuid.UUID) string {
	return fmt.Sprintf("%s:rbac:role:%s", KeyPrefix, roleID)
}
func MedicineCatalogKey(medicineID uuid.UUID) string {
	return fmt.Sprintf("%s:cat:med:%s", KeyPrefix, medicineID)
}
func BranchInventoryKey(branchID, medicineID uuid.UUID) string {
	return fmt.Sprintf("%s:inv:%s:%s", KeyPrefix, branchID, medicineID)
}

// Locks (distributed)
func LockKey(name string) string {
	return fmt.Sprintf("%s:lock:%s", KeyPrefix, name)
}

// Pub/Sub channels
const (
	ChannelNotifications = "mc:events:notifications"
	ChannelSessions      = "mc:events:sessions"
	ChannelInventory     = "mc:events:inventory"
	ChannelSales         = "mc:events:sales"
	ChannelAudit         = "mc:events:audit"
)
