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
