package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func GetGeoLocation(ip string) string {
	if ip == "" || ip == "::1" || ip == "127.0.0.1" {
		return "localhost"
	}

	url := fmt.Sprintf(
		"http://ip-api.com/json/%s?fields=city,country",
		ip,
	)

	client := http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return "unknown"
	}

	defer resp.Body.Close()

	var result struct {
		City    string `json:"city"`
		Country string `json:"country"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "unknown"
	}

	if result.City == "" {
		return "unknown"
	}

	return fmt.Sprintf(
		"%s, %s",
		result.City,
		result.Country,
	)
}
