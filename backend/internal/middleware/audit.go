package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	appctx "backend/internal/common/context"
)

const (
	maxAuditBodyBytes = 16 << 10 // 16 KiB

	auditRedaction = "***REDACTED***"
)

// auditSensitiveExact are field names redacted on an exact (case-insensitive) match. They are short/ambiguous, so substring matching would over-redact.
var auditSensitiveExact = map[string]struct{}{
	"pin": {}, "cvv": {}, "cvc": {}, "cvv2": {}, "otp": {},
	"ssn": {}, "mfa": {}, "totp": {}, "code": {},
}

// auditSensitiveContains are redacted when the field name contains any of them. These are distinctive enough that a substring match is safe.
var auditSensitiveContains = []string{
	"password", "passwd", "secret", "token", "authorization",
	"api_key", "apikey", "private_key", "client_secret", "credential",
	"card", "refresh", "access_token", "session",
}

type AuditEntry struct {
	// Correlation.
	RequestID string    `json:"request_id"`
	Timestamp time.Time `json:"timestamp"`

	// Actor and tenant scope.
	UserID    uuid.UUID  `json:"user_id"`
	OrgID     uuid.UUID  `json:"org_id"`
	BranchID  *uuid.UUID `json:"branch_id,omitempty"`
	SessionID uuid.UUID  `json:"session_id"`
	RoleName  string     `json:"role_name"`

	// What was attempted.
	Method    string `json:"method"`
	Route     string `json:"route"` // matched template, low cardinality
	Path      string `json:"path"`  // concrete request path
	Module    string `json:"module,omitempty"`
	Action    string `json:"action,omitempty"`
	ClientIP  string `json:"client_ip"`
	UserAgent string `json:"user_agent,omitempty"`

	// Outcome.
	StatusCode int    `json:"status_code"`
	Success    bool   `json:"success"`
	ErrorCode  string `json:"error_code,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`

	// Masked, size-capped request body (JSON only). nil for non-JSON or empty.
	RequestBody json.RawMessage `json:"request_body,omitempty"`
}

func (m *Middleware) Audit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isMutation(c.Request.Method) {
			c.Next()
			return
		}

		start := m.now()

		var rawBody []byte
		if isJSONRequest(c) {
			rawBody, _ = readAndRestoreBody(c) // best effort; body stays readable
		}

		c.Next()

		ctx := c.Request.Context()
		status := c.Writer.Status()

		entry := AuditEntry{
			RequestID:   appctx.RequestID(ctx),
			Timestamp:   start,
			UserID:      appctx.UserID(ctx),
			OrgID:       appctx.OrgID(ctx),
			BranchID:    appctx.BranchID(ctx),
			SessionID:   appctx.SessionID(ctx),
			RoleName:    appctx.RoleName(ctx),
			Method:      c.Request.Method,
			Route:       routeOf(c),
			Path:        c.Request.URL.Path,
			Module:      c.GetString(ginKeyAuditModule),
			Action:      c.GetString(ginKeyAuditAction),
			ClientIP:    c.ClientIP(),
			UserAgent:   c.Request.UserAgent(),
			StatusCode:  status,
			Success:     status < 400,
			ErrorCode:   c.GetString(ginKeyErrorCode),
			LatencyMS:   m.now().Sub(start).Milliseconds(),
			RequestBody: maskBody(rawBody),
		}

		// context.WithoutCancel keeps trace/request values for correlation while detaching from the request's (possibly already-cancelled) deadline.
		m.audit.Record(context.WithoutCancel(ctx), entry)
	}
}

// isJSONRequest reports whether the body is JSON we can safely parse and mask.
// Non-JSON bodies (uploads, form posts) are not captured.
func isJSONRequest(c *gin.Context) bool {
	ct := c.GetHeader("Content-Type")
	return strings.Contains(strings.ToLower(ct), "application/json")
}

// maskBody parses a JSON body, redacts sensitive fields recursively, and caps the
// serialized result. Returns nil for empty or non-JSON input.
func maskBody(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil // not JSON — don't capture arbitrary bytes
	}
	masked := maskAny(parsed)
	out, err := json.Marshal(masked)
	if err != nil {
		return nil
	}
	if len(out) > maxAuditBodyBytes {
		return json.RawMessage(`{"_truncated":true}`)
	}
	return out
}

// maskAny walks a decoded JSON value and redacts sensitive object keys in place.
func maskAny(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSensitiveKey(k) {
				t[k] = auditRedaction
				continue
			}
			t[k] = maskAny(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = maskAny(val)
		}
		return t
	default:
		return v
	}
}

// isSensitiveKey reports whether a JSON field name should be redacted.
func isSensitiveKey(k string) bool {
	lk := strings.ToLower(strings.TrimSpace(k))
	if _, ok := auditSensitiveExact[lk]; ok {
		return true
	}
	for _, s := range auditSensitiveContains {
		if strings.Contains(lk, s) {
			return true
		}
	}
	return false
}
