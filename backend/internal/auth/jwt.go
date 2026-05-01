package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	UserID            uuid.UUID `json:"user_id"`
	Role              string    `json:"role"`
	DeviceFingerprint string    `json:"device_fingerprint"`
	SessionID         string    `json:"session_id"`
	jwt.RegisteredClaims
}

func GenerateAccessToken(userID uuid.UUID, role, secret string, ttlMinutes int, deviceFingerprint, sessionID string) (string, time.Time, error) {
	exp := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)
	claims := Claims{
		UserID:            userID,
		Role:              role,
		DeviceFingerprint: deviceFingerprint,
		SessionID:         sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
			Subject:   userID.String(),
			Issuer:    "pharmaciano",
			Audience:  jwt.ClaimStrings{"pharmaciano-users"},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	return signed, exp, err
}

func GenerateRefreshToken(userID uuid.UUID, secret string, ttlMinutes int, deviceFingerprint, sessionID string) (string, error) {
	claims := Claims{
		UserID:            userID,
		Role:              "",
		DeviceFingerprint: deviceFingerprint,
		SessionID:         sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(ttlMinutes) * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
			Issuer:    "pharmaciano",
			Audience:  jwt.ClaimStrings{"pharmaciano-users"},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func ValidateToken(tokenString, secret string) (*Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("pharmaciano"),
		jwt.WithAudience("pharmaciano-users"),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)

	token, err := parser.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	if claims.UserID == uuid.Nil {
		return nil, errors.New("invalid user_id")
	}
	if claims.Subject == "" {
		return nil, errors.New("invalid subject")
	}
	if claims.ID == "" {
		return nil, errors.New("missing jti")
	}
	return claims, nil
}

// GenerateDeviceFingerprint uses IP + User-Agent
func GenerateDeviceFingerprint(ip, userAgent, secret string) string {
	data := ip + ":" + userAgent + ":" + secret
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
