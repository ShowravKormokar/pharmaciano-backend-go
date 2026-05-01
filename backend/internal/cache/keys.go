package cache

import "fmt"

const (
	TokenBlacklistPrefix = "bl_token:xyz456" // added for blacklist entries
)

func TokenBlacklistKey(tokenID string) string {
	return fmt.Sprintf("bl_token:%s", tokenID)
}

// func TokenBlacklistKey(tokenID string) string {
// 	return fmt.Sprintf("%s%s", TokenBlacklistPrefix, tokenID)
// }
