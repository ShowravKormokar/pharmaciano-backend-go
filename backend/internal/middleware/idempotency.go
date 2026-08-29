package middleware

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/redis"
	"backend/pkg/crypto"
)

const (
	idemMaxCaptureBytes = 1 << 20 // 1 MiB

	// idemDefaultKeyMaxLen is used when config leaves key_max_length unset.
	idemDefaultKeyMaxLen = 255

	// idemStorePending / idemStoreDone are the two record states.
	idemStatePending = "pending"
	idemStateDone    = "done"

	headerIdempotencyReplayed = "Idempotency-Replayed"
)

// idemRecord is the JSON value stored in Redis under the idempotency key.
type idemRecord struct {
	State       string `json:"state"`
	Fingerprint string `json:"fp"`
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"ct,omitempty"`
	Body        string `json:"body,omitempty"` // std base64 of the response body
}

func (m *Middleware) Idempotency() gin.HandlerFunc {
	ttl := 24 * time.Hour
	keyMax := idemDefaultKeyMaxLen
	if m.cfg != nil {
		if m.cfg.Idempotency.TTL > 0 {
			ttl = m.cfg.Idempotency.TTL
		}
		if m.cfg.Idempotency.KeyMaxLength > 0 {
			keyMax = m.cfg.Idempotency.KeyMaxLength
		}
	}

	return func(c *gin.Context) {
		// Only unsafe methods are deduplicated; GET/HEAD/OPTIONS are already
		// idempotent by definition.
		if !isMutation(c.Request.Method) || m.redis == nil {
			c.Next()
			return
		}

		rawKey := c.GetHeader(constants.HeaderIdempotencyKey)
		if rawKey == "" {
			// Allowed, just unprotected. The handler still runs normally.
			c.Next()
			return
		}
		if !validIdempotencyKey(rawKey, keyMax) {
			m.abortError(c, errs.Validation("invalid idempotency key", errs.FieldError{
				Field:   constants.HeaderIdempotencyKey,
				Rule:    "format",
				Message: "must be printable ASCII within the configured length",
			}))
			return
		}

		body, err := readAndRestoreBody(c)
		if err != nil {
			m.abortError(c, errs.Validation("unable to read request body for idempotency", errs.FieldError{
				Field:   "body",
				Rule:    "readable",
				Message: "request body could not be read",
			}))
			return
		}

		fp := crypto.SHA256Hex(c.Request.Method + "\n" + c.Request.URL.Path + "\n" + string(body))
		key := redis.IdempotencyKey(appctx.UserID(c.Request.Context()), rawKey)

		pending, _ := json.Marshal(idemRecord{State: idemStatePending, Fingerprint: fp})
		won, err := m.redis.SetNX(c.Request.Context(), key, string(pending), ttl)
		if err != nil {
			// Redis unavailable: we can't dedupe. Fail open on availability but log — the operation runs unprotected exactly once for this attempt.
			m.logFor(c).Error("idempotency store unavailable; proceeding without dedup",
				zap.Error(err))
			c.Next()
			return
		}

		if !won {
			m.handleExisting(c, key, fp)
			return
		}

		// We own the key. Capture the response for replay.
		capture := &responseCapture{ResponseWriter: c.Writer, body: &bytes.Buffer{}, limit: idemMaxCaptureBytes}
		c.Writer = capture

		completed := false
		defer func() {
			if completed {
				return
			}
			m.releaseKey(key)
		}()

		c.Next()

		status := c.Writer.Status()
		// Do not cache server errors or oversized/truncated bodies: let the client retry and actually succeed.
		if status >= http.StatusInternalServerError || capture.truncated {
			m.releaseKey(key)
			completed = true
			return
		}

		rec := idemRecord{
			State:       idemStateDone,
			Fingerprint: fp,
			Status:      status,
			ContentType: c.Writer.Header().Get("Content-Type"),
			Body:        base64.StdEncoding.EncodeToString(capture.body.Bytes()),
		}
		if payload, mErr := json.Marshal(rec); mErr == nil {
			// Detached context: the request context may be at/near its deadline, but persisting the outcome must still succeed so retries replay.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if sErr := m.redis.Set(ctx, key, string(payload), ttl); sErr != nil {
				m.logFor(c).Warn("failed to persist idempotency result", zap.Error(sErr))
			}
			cancel()
		}
		completed = true
	}
}

// handleExisting resolves a duplicate: replay, conflict, or in-flight.
func (m *Middleware) handleExisting(c *gin.Context, key, fp string) {
	val, err := m.redis.Get(c.Request.Context(), key)
	if err != nil {
		if errors.Is(err, redis.ErrKeyNotFound) {
			// The record expired in the tiny window between SETNX and GET. Rare;
			// proceed unprotected rather than fail the request.
			m.logFor(c).Warn("idempotency key vanished between setnx and get; proceeding")
			c.Next()
			return
		}
		m.logFor(c).Error("idempotency lookup failed; proceeding without dedup", zap.Error(err))
		c.Next()
		return
	}

	var rec idemRecord
	if json.Unmarshal([]byte(val), &rec) != nil {
		// Corrupt record: treat as a hard conflict rather than guess.
		m.abortError(c, errs.New(errs.CodeIdempotencyConflict,
			"stored idempotency record is unreadable"))
		return
	}

	// Same key, different request body → client error, must be surfaced.
	if rec.Fingerprint != fp {
		m.abortError(c, errs.New(errs.CodeIdempotencyConflict,
			"the same Idempotency-Key was used with a different request"))
		return
	}

	if rec.State == idemStatePending {
		// A concurrent duplicate is still executing.
		c.Writer.Header().Set(constants.HeaderRetryAfter, "1")
		m.abortError(c, errs.Conflict("a request with this Idempotency-Key is already being processed"))
		return
	}

	// Completed and matching → replay verbatim.
	m.replay(c, rec)
}

// replay writes a stored response back to the client and stops the chain.
func (m *Middleware) replay(c *gin.Context, rec idemRecord) {
	bodyBytes, err := base64.StdEncoding.DecodeString(rec.Body)
	if err != nil {
		m.abortError(c, errs.New(errs.CodeIdempotencyConflict,
			"stored idempotency response is corrupt"))
		return
	}
	h := c.Writer.Header()
	if rec.ContentType != "" {
		h.Set("Content-Type", rec.ContentType)
	}
	h.Set(headerIdempotencyReplayed, "true")
	c.Writer.WriteHeader(rec.Status)
	_, _ = c.Writer.Write(bodyBytes)
	c.Abort()
}

// releaseKey best-effort deletes a pending idempotency key on a detached context.
func (m *Middleware) releaseKey(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := m.redis.Del(ctx, key); err != nil {
		m.log.Warn("failed to release idempotency key", zap.String("key", key), zap.Error(err))
	}
}

// isMutation reports whether the method can change state and thus warrants idempotency protection.
func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func validIdempotencyKey(k string, max int) bool {
	if len(k) == 0 || len(k) > max {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x21 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

// readAndRestoreBody reads the full body and replaces it with a fresh reader so downstream handlers see an untouched stream.
func readAndRestoreBody(c *gin.Context) ([]byte, error) {
	if c.Request.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

type responseCapture struct {
	gin.ResponseWriter
	body      *bytes.Buffer
	limit     int
	truncated bool
}

func (w *responseCapture) Write(b []byte) (int, error) {
	w.tee(b)
	return w.ResponseWriter.Write(b)
}

func (w *responseCapture) WriteString(s string) (int, error) {
	w.tee([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

func (w *responseCapture) tee(b []byte) {
	if w.truncated {
		return
	}
	if remaining := w.limit - w.body.Len(); remaining > 0 {
		if len(b) > remaining {
			w.body.Write(b[:remaining])
			w.truncated = true
			return
		}
		w.body.Write(b)
	} else if len(b) > 0 {
		w.truncated = true
	}
}

// Idempotency makes unsafe (state-changing) requests safe to retry. A client
// sends the same Idempotency-Key on a retry; this middleware guarantees the
// underlying operation runs at most once and that every retry observes the
// original outcome.
//
// Protocol (all keyed by user + Idempotency-Key, so keys can't collide across
// users):
//
//  1. SET NX a "pending" record. Winning the SET means we are the first and only
//     executor: we buffer the response, run the handler, then overwrite the
//     record with the captured response.
//  2. Losing the SET means the key already exists:
//     - "pending"  → a duplicate is in flight → 409 with a short Retry-After.
//     - "done" + same body fingerprint → replay the stored response verbatim.
//     - "done" + different fingerprint → 409 IDEMPOTENCY_CONFLICT (the key was
//     reused for a genuinely different request — a client bug we must surface,
//     never silently execute).
//
// Safety properties:
//   - The pending record is released (deleted) if the handler panics or returns
//     5xx, so a transient failure never locks the key for the full TTL — the
//     client can legitimately retry. Only sub-500, fully-captured responses are
//     cached.
//   - The key is optional: a mutation sent without one simply isn't dedup-
//     protected (and is logged at debug). A malformed key is rejected 400.
//   - Read-only methods and requests arriving without Redis are passed straight
//     through.