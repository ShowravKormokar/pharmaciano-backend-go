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

	// DEBUG: [auth/jwt.go] GenerateAccessToken
	// fmt.Printf("[auth/jwt.go] GenerateAccessToken: userID=%s, role=%s, sessionID=%s, deviceFp=%s, exp=%s, tokenJTI=%s\n", userID, role, sessionID, deviceFingerprint, exp.Format(time.RFC3339), claims.ID)

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
	signed, err := token.SignedString([]byte(secret))

	// DEBUG: [auth/jwt.go] GenerateRefreshToken
	// fmt.Printf("[auth/jwt.go] GenerateRefreshToken: userID=%s, sessionID=%s, deviceFp=%s, tokenJTI=%s\n", userID, sessionID, deviceFingerprint, claims.ID)

	return signed, err
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
		// DEBUG: [auth/jwt.go] ValidateToken - parse error
		// fmt.Printf("[auth/jwt.go] ValidateToken: parse error: %v\n", err)
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		// DEBUG: [auth/jwt.go] ValidateToken - invalid token
		// fmt.Printf("[auth/jwt.go] ValidateToken: token invalid or claims type mismatch\n")
		return nil, errors.New("invalid token")
	}

	if claims.UserID == uuid.Nil {
		// DEBUG: [auth/jwt.go] ValidateToken - invalid user_id
		// fmt.Printf("[auth/jwt.go] ValidateToken: user_id is nil\n")
		return nil, errors.New("invalid user_id")
	}
	if claims.Subject == "" {
		// DEBUG: [auth/jwt.go] ValidateToken - invalid subject
		// fmt.Printf("[auth/jwt.go] ValidateToken: subject empty\n")
		return nil, errors.New("invalid subject")
	}
	if claims.ID == "" {
		// DEBUG: [auth/jwt.go] ValidateToken - missing jti
		// fmt.Printf("[auth/jwt.go] ValidateToken: jti missing\n")
		return nil, errors.New("missing jti")
	}

	// DEBUG: [auth/jwt.go] ValidateToken - success
	// fmt.Printf("[auth/jwt.go] ValidateToken: success - userID=%s, role=%s, sessionID=%s, deviceFp=%s, jti=%s\n", claims.UserID, claims.Role, claims.SessionID, claims.DeviceFingerprint, claims.ID)

	return claims, nil
}

func GenerateDeviceFingerprint(ip, userAgent, secret string) string {
	data := ip + ":" + userAgent + ":" + secret
	hash := sha256.Sum256([]byte(data))
	fp := hex.EncodeToString(hash[:])

	// DEBUG: [auth/jwt.go] GenerateDeviceFingerprint
	// fmt.Printf("[auth/jwt.go] GenerateDeviceFingerprint: ip=%s, ua=%s, fp=%s\n", ip, userAgent, fp)

	return fp
}
