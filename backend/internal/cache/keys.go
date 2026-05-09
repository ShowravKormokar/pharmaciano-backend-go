package cache

import "fmt"

const (
	// AUTH
	TokenBlacklistPrefix = "auth:blacklist:"
	RefreshUsedPrefix    = "auth:refresh_used:"
	SessionPrefix        = "auth:session:"
	UserSessionsPrefix   = "auth:user_sessions:"
	// SECURITY
	LastIPPrefix        = "security:last_ip:"
	RiskPrefix          = "security:risk:"
	SecurityAlertPrefix = "security:alert:"
	LoginHistoryPrefix  = "security:login_history:"
	// RBAC
	RolePermissionsPrefix = "rbac:role_permissions:"
	// RATE LIMIT
	RateLimitPrefix = "rl:"
)

func TokenBlacklistKey(jti string) string {
	return fmt.Sprintf("%s%s", TokenBlacklistPrefix, jti)
}

func RefreshUsedKey(jti string) string {
	return fmt.Sprintf("%s%s", RefreshUsedPrefix, jti)
}

func SessionKey(sessionID string) string {
	return fmt.Sprintf("%s%s", SessionPrefix, sessionID)
}

func UserSessionsKey(userID string) string {
	return fmt.Sprintf("%s%s", UserSessionsPrefix, userID)
}

func LastIPKey(userID string) string {
	return fmt.Sprintf("%s%s", LastIPPrefix, userID)
}

func RiskKey(userID string) string {
	return fmt.Sprintf("%s%s", RiskPrefix, userID)
}

func SecurityAlertKey(userID string) string {
	return fmt.Sprintf("%s%s", SecurityAlertPrefix, userID)
}

func LoginHistoryKey(userID string) string {
	return fmt.Sprintf("%s%s", LoginHistoryPrefix, userID)
}

func RolePermissionsKey(role string) string {
	return fmt.Sprintf("%s%s", RolePermissionsPrefix, role)
}
