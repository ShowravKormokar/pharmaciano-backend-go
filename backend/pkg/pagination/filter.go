package pagination

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"backend/pkg/times"
)

const (
	// MaxQueryLen caps the ?q= search string — an unbounded search term is a cheap way to force an expensive trigram scan.
	MaxQueryLen = 200
	MaxExtras   = 50
	// MaxFields caps the ?fields= sparse-fieldset list.
	MaxFields = 30
)

// ErrInvalidFilter is returned by FromQuery for any malformed standard filter value (from/to/branch_id).
var ErrInvalidFilter = errors.New("pagination: invalid filter value")

// Filter is a bag of typed filter values parsed from query strings.
type Filter struct {
	Q              string
	Status         string
	DateRange      times.Range // zero value, IsValid()==false, means "no date filter"
	BranchID       *uuid.UUID
	IncludeDeleted bool // SUPER_ADMIN only per API.md — caller must still authorize this
	Fields         []string
	Extras         map[string]string // remaining ?key=value pairs; validate against a per-endpoint whitelist before using in SQL
}

// FromQuery reads standard filters + all extras from url.Values.
func FromQuery(v url.Values) (Filter, error) {
	f := Filter{Extras: map[string]string{}}
	var fromStr, toStr string

	for k, vs := range v {
		if len(vs) == 0 {
			continue
		}
		val := vs[0]
		switch strings.ToLower(k) {
		case "q":
			q := strings.TrimSpace(val)
			if len(q) > MaxQueryLen {
				q = q[:MaxQueryLen]
			}
			f.Q = q
		case "status":
			f.Status = strings.TrimSpace(val)
		case "from":
			fromStr = val
		case "to":
			toStr = val
		case "branch_id":
			id, err := uuid.Parse(val)
			if err != nil {
				return Filter{}, fmt.Errorf("%w: branch_id %q: %v", ErrInvalidFilter, val, err)
			}
			f.BranchID = &id
		case "include_deleted":
			f.IncludeDeleted = parseBool(val)
		case "fields":
			for _, name := range strings.Split(val, ",") {
				name = strings.TrimSpace(name)
				if name != "" && len(f.Fields) < MaxFields {
					f.Fields = append(f.Fields, name)
				}
			}
		case "page", "limit", "cursor", "sort":
			// reserved — handled by Offset/CursorQuery, not Filter
		default:
			if len(f.Extras) < MaxExtras {
				f.Extras[k] = val
			}
		}
	}

	dr, err := times.ParseDateRange(fromStr, toStr)
	if err != nil {
		return Filter{}, fmt.Errorf("%w: %v", ErrInvalidFilter, err)
	}
	f.DateRange = dr

	return f, nil
}

// Bool reads an "extra" flag ("true"/"1"/"yes") case-insensitively.
func (f Filter) Bool(key string) bool {
	v, ok := f.Extras[key]
	return ok && parseBool(v)
}

// Int reads an "extra" as int; returns def on missing/invalid.
func (f Filter) Int(key string, def int) int {
	if v, ok := f.Extras[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func (f Filter) IntClamped(key string, def, min, max int) int {
	n := f.Int(key, def)
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// UUID reads an "extra" as UUID; returns nil on missing/invalid.
func (f Filter) UUID(key string) *uuid.UUID {
	if v, ok := f.Extras[key]; ok {
		if id, err := uuid.Parse(v); err == nil {
			return &id
		}
	}
	return nil
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "t", "true", "y", "yes":
		return true
	}
	return false
}
