package cache

import "fmt"

const (
	TokenBlacklistPrefix = "bl_token:"
)

func TokenBlacklistKey(tokenID string) string {
	return fmt.Sprintf("%s%s", TokenBlacklistPrefix, tokenID)
}
