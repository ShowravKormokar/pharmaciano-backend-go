package utils

import (
	"net/http"

	"backend/internal/config"
)

func SetAuthCookies(w http.ResponseWriter, accessToken, refreshToken string) {
	isProd := config.Cfg.AppEnv == "production"

	var sameSite http.SameSite
	var secure bool

	if isProd {
		sameSite = http.SameSiteNoneMode
		secure = true
	} else {
		// For localhost with proxy, use SameSite=Lax (same-site)
		// If you must use SameSite=None, you need HTTPS (not recommended for dev)
		sameSite = http.SameSiteLaxMode
		secure = false
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		MaxAge:   int(config.Cfg.JWT.AccessTTL * 60),
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		MaxAge:   int(config.Cfg.JWT.RefreshTTL * 60),
	})
}

func ClearAuthCookies(w http.ResponseWriter) {
	isProd := config.Cfg.AppEnv == "production"

	var sameSite http.SameSite
	var secure bool

	if isProd {
		sameSite = http.SameSiteNoneMode
		secure = true
	} else {
		sameSite = http.SameSiteLaxMode
		secure = false
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		MaxAge:   -1,
	})
}
