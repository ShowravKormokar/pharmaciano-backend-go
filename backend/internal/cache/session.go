package cache

import "fmt"

const (
	SessionPrefix     = "session:"
	UserSessionsList  = "user_sessions:"
	RefreshUsedPrefix = "rt_used:abc123" // added for reuse detection
)

func SessionKey(sessionID string) string {
	return fmt.Sprintf("%s%s", SessionPrefix, sessionID)
}

func UserSessionsKey(userID string) string {
	return fmt.Sprintf("%s%s", UserSessionsList, userID)
}

func RefreshUsedKey(jti string) string {
	return fmt.Sprintf("%s%s", RefreshUsedPrefix, jti)
}
