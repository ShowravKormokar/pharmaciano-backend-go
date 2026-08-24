package pagination

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/pkg/crypto"
)

// Default & hard limits — FALLBACK values only.
const (
	DefaultLimit = 20
	MaxLimit     = 100
	MinLimit     = 1
	// MaxSortFields caps how many "sort=" keys a single request may specify — an unbounded sort list lets a client force an arbitrarily wide ORDER BY.
	MaxSortFields = 5
)

// Offset pagination

// Offset is classical page/limit pagination.
type Offset struct {
	Page  int    `json:"page"  form:"page"`
	Limit int    `json:"limit" form:"limit"`
	Sort  string `json:"sort"  form:"sort"` // e.g. "-created_at,name"
}

// Normalize clamps values into [MinLimit, MaxLimit].
func (o *Offset) Normalize() { o.NormalizeWithMax(MaxLimit) }

func (o *Offset) NormalizeWithMax(maxLimit int) {
	if o.Page < 1 {
		o.Page = 1
	}
	if maxLimit <= 0 {
		maxLimit = MaxLimit
	}
	if o.Limit < MinLimit {
		o.Limit = DefaultLimit
	}
	if o.Limit > maxLimit {
		o.Limit = maxLimit
	}
}

// OffsetSQL returns the SQL OFFSET (row skip count).
func (o Offset) OffsetSQL() int {
	page, limit := o.Page, o.Limit
	if page < 1 {
		page = 1
	}
	if limit < MinLimit {
		limit = DefaultLimit
	}
	return (page - 1) * limit
}

// Response meta (the "pagination" block in the envelope)

// Meta is the pagination block placed in the response envelope — see pkg/response.List.
type Meta struct {
	Page       int    `json:"page,omitempty"`
	Limit      int    `json:"limit"`
	Total      int64  `json:"total,omitempty"`
	TotalPages int    `json:"total_pages,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
	PrevCursor string `json:"prev_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// BuildOffsetMeta computes the response meta for offset pagination.
func BuildOffsetMeta(o Offset, total int64) Meta {
	pages := 0
	if o.Limit > 0 {
		pages = int(math.Ceil(float64(total) / float64(o.Limit)))
	}
	return Meta{
		Page:       o.Page,
		Limit:      o.Limit,
		Total:      total,
		TotalPages: pages,
		HasMore:    int64(o.Page)*int64(o.Limit) < total,
	}
}

// BuildCursorMeta computes the response meta for cursor pagination — the
// counterpart to BuildOffsetMeta that was previously missing, leaving every
// cursor-based endpoint (the exact "hot endpoints" this package's own doc
// comment says cursor is preferred for) with no shared way to build its
// response meta.
func BuildCursorMeta(limit int, hasMore bool, nextCursor string) Meta {
	return Meta{Limit: limit, HasMore: hasMore, NextCursor: nextCursor}
}

// Cursor pagination — signed, tamper-evident

// Cursor is the payload encoded into an opaque pagination token.
type Cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"i"`
	SortKey   string    `json:"s,omitempty"`
}

var ErrInvalidCursor = errors.New("pagination: invalid cursor")

// ValidateSortKey reports an error if c was issued for a different sort order than expected.
func (c Cursor) ValidateSortKey(expected string) error {
	if c.SortKey != expected {
		return fmt.Errorf("%w: issued for a different sort order", ErrInvalidCursor)
	}
	return nil
}

// CursorCodec encodes/decodes cursors as HMAC-signed, URL-safe tokens (see
// pkg/crypto.SignToken/VerifyToken) — a plain base64(JSON) cursor, as this
// package originally shipped, is fully client-forgeable: any client can
// hand-craft an arbitrary (created_at, id) pair.
type CursorCodec struct {
	key []byte
}

// NewCursorCodec builds a codec from a signing key.
func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("pagination: cursor signing key must be at least 32 bytes (got %d)", len(key))
	}
	return &CursorCodec{key: key}, nil
}

// Encode returns a signed, URL-safe cursor string.
func (cc *CursorCodec) Encode(c Cursor) string {
	payload, _ := json.Marshal(c) // never fails for this struct
	return crypto.SignToken(cc.key, payload)
}

// Decode verifies and parses a cursor string.
func (cc *CursorCodec) Decode(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, ErrInvalidCursor
	}
	payload, ok := crypto.VerifyToken(cc.key, s)
	if !ok {
		return Cursor{}, ErrInvalidCursor
	}
	var c Cursor
	if err := json.Unmarshal(payload, &c); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	if c.ID == uuid.Nil {
		return Cursor{}, ErrInvalidCursor
	}
	return c, nil
}

// CursorQuery holds normalized cursor pagination inputs.
type CursorQuery struct {
	Cursor string `form:"cursor"`
	Limit  int    `form:"limit"`
	Sort   string `form:"sort"`
}

// Normalize clamps the limit into range. See Offset.Normalize's note on
// this being a fallback vs config-driven limits.
func (q *CursorQuery) Normalize() { q.NormalizeWithMax(MaxLimit) }

// NormalizeWithMax is Normalize with a caller-supplied max limit override.
func (q *CursorQuery) NormalizeWithMax(maxLimit int) {
	if maxLimit <= 0 {
		maxLimit = MaxLimit
	}
	if q.Limit < MinLimit {
		q.Limit = DefaultLimit
	}
	if q.Limit > maxLimit {
		q.Limit = maxLimit
	}
}

// Sorting

// SortField is one parsed "sort=" key. A typed struct (not [2]any, which the caller had to blind-type-assert) so field/desc are compile-time safe.
type SortField struct {
	Field string
	Desc  bool
}

// SortDirection reports the field name and direction for a single sort key, e.g. "-created_at" → ("created_at", true).
func SortDirection(key string) (field string, desc bool) {
	if strings.HasPrefix(key, "-") {
		return strings.TrimPrefix(key, "-"), true
	}
	return key, false
}

// ParseSort splits "sort=-created_at,name" into ordered SortFields.
func ParseSort(sort string, allowed map[string]struct{}) ([]SortField, error) {
	if sort == "" {
		return nil, nil
	}
	parts := strings.Split(sort, ",")
	if len(parts) > MaxSortFields {
		return nil, fmt.Errorf("pagination: too many sort fields (max %d)", MaxSortFields)
	}
	out := make([]SortField, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		f, desc := SortDirection(part)
		if _, ok := allowed[f]; !ok {
			return nil, fmt.Errorf("pagination: sort field %q not allowed", f)
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, SortField{Field: f, Desc: desc})
	}
	return out, nil
}

// SortKeyString rebuilds a canonical "sort=" string from parsed fields — used to bind a Cursor.
func SortKeyString(fields []SortField) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		if f.Desc {
			parts[i] = "-" + f.Field
		} else {
			parts[i] = f.Field
		}
	}
	return strings.Join(parts, ",")
}
