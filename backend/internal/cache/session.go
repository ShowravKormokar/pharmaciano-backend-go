package cache

import "fmt"

const (
	SessionPrefix    = "session:"
	UserSessionsList = "user_sessions:" // set of session IDs
)

func SessionKey(sessionID string) string {
	return fmt.Sprintf("%s%s", SessionPrefix, sessionID)
}

func UserSessionsKey(userID string) string {
	return fmt.Sprintf("%s%s", UserSessionsList, userID)
}
