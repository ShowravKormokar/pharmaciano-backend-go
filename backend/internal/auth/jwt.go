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
	SessionID         string    `json:"session_id"` // NEW
	jwt.RegisteredClaims
}

// GenerateAccessToken now accepts sessionID
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

// GenerateRefreshToken now accepts sessionID
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

// Mannual claim verification for JWTv5 since it doesn't support VerifyAudience() and VerifyIssuer(). Parser-based validation

func ValidateToken(tokenString, secret string) (*Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), // enforce algo
		jwt.WithIssuer("pharmaciano"),                                // validate iss
		jwt.WithAudience("pharmaciano-users"),                        // validate aud
		jwt.WithExpirationRequired(),                                 // exp must exist
		jwt.WithIssuedAt(),                                           // validate iat
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

	// ✅ Additional manual safety checks (defense-in-depth)

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

func GenerateDeviceFingerprint(userAgent, secret string) string {
	data := userAgent + ":" + secret
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// GenerateDeviceFingerprint creates a unique hash from request data and secret.
// func GenerateDeviceFingerprint(ip, userAgent, secret string) string {
// 	data := ip + ":" + userAgent + ":" + secret
// 	hash := sha256.Sum256([]byte(data))
// 	return hex.EncodeToString(hash[:])
// }

// ------------------ Deprecated: JWTv5 not support VerifyAudience() and VerifyIssuer()  need Manual claim verification ------------------
// ValidateToken parses and validates both access and refresh tokens.
// Refresh tokens must also contain issuer/audience.
// func ValidateToken(tokenString, secret string) (*Claims, error) {
// 	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
// 		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
// 			return nil, errors.New("unexpected signing method")
// 		}
// 		return []byte(secret), nil
// 	})
// 	if err != nil {
// 		return nil, err
// 	}
// 	claims, ok := token.Claims.(*Claims)
// 	if !ok || !token.Valid {
// 		return nil, errors.New("invalid token")
// 	}

// 	// Explicit verification via embedded RegisteredClaims
// 	// if !claims.RegisteredClaims.VerifyIssuer("pharmaciano", true) {
// 	// 	return nil, errors.New("invalid issuer")
// 	// }
// 	// if !claims.RegisteredClaims.VerifyAudience("pharmaciano-users", true) {
// 	// 	return nil, errors.New("invalid audience")
// 	// }

// 	// Verify issuer manually
// 	if claims.Issuer != "pharmaciano" {
// 		return nil, errors.New("invalid issuer")
// 	}

// 	// Verify audience using embedded RegisteredClaims
// 	if !claims.RegisteredClaims.VerifyAudience("pharmaciano-users", true) {
// 		return nil, errors.New("invalid audience")
// 	}

// 	return claims, nil
// }
