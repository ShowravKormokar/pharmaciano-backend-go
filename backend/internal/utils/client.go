package utils

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func GetClientIP(c *gin.Context) string {

	// Cloudflare
	ip := c.GetHeader("CF-Connecting-IP")
	if ip != "" {
		return cleanIP(ip)
	}

	// Nginx / Proxy
	ip = c.GetHeader("X-Real-IP")
	if ip != "" {
		return cleanIP(ip)
	}

	// Load balancer / reverse proxy
	ip = c.GetHeader("X-Forwarded-For")
	if ip != "" {
		parts := strings.Split(ip, ",")
		return cleanIP(parts[0])
	}

	// Gin fallback
	return cleanIP(c.ClientIP())
}

func cleanIP(ip string) string {
	ip = strings.TrimSpace(ip)

	if ip == "::1" {
		return "127.0.0.1"
	}

	return ip
}
