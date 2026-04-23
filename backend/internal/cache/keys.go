package cache

import "fmt"

// Cache key constants and generators
const (
	// Token blacklist keys
	TokenBlacklistPrefix = "token_blacklist:"

	// User cache keys
	UserCachePrefix = "user:"
	UserEmailPrefix = "user:email:"

	// Session cache keys
	SessionPrefix = "session:"

	// Inventory cache keys
	InventoryCachePrefix = "inventory:"

	// Report cache keys
	ReportCachePrefix = "report:"
)

// TokenBlacklistKey generates a token blacklist cache key
func TokenBlacklistKey(token string) string {
	return fmt.Sprintf("%s%s", TokenBlacklistPrefix, token)
}

// UserCacheKey generates a user cache key
func UserCacheKey(userID string) string {
	return fmt.Sprintf("%s%s", UserCachePrefix, userID)
}

// UserEmailCacheKey generates a user email cache key
func UserEmailCacheKey(email string) string {
	return fmt.Sprintf("%s%s", UserEmailPrefix, email)
}

// SessionKey generates a session cache key
func SessionKey(sessionID string) string {
	return fmt.Sprintf("%s%s", SessionPrefix, sessionID)
}

// InventoryCacheKey generates an inventory cache key
func InventoryCacheKey(medicineID string) string {
	return fmt.Sprintf("%s%s", InventoryCachePrefix, medicineID)
}

// ReportCacheKey generates a report cache key
func ReportCacheKey(reportID string) string {
	return fmt.Sprintf("%s%s", ReportCachePrefix, reportID)
}
