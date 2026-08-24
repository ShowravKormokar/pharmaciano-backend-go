package strutil

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	slugMultiDash = regexp.MustCompile(`-+`)
	slugAllowed   = regexp.MustCompile(`[^a-z0-9-]`)
)

// Slug converts arbitrary text into a URL-safe, ASCII slug.
func Slug(s string) string {
	original := s
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return r
		case unicode.IsSpace(r), r == '_':
			return '-'
		}
		return r
	}, s)
	s = slugAllowed.ReplaceAllString(s, "")
	s = slugMultiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s != "" {
		return s
	}
	sum := sha256.Sum256([]byte(original))
	return "n-" + hex.EncodeToString(sum[:4])
}

// Mask replaces all but the last `keep` runes with `*`. Rune-based (not
// byte-based), so it never splits a multi-byte character. Useful for
// displaying partial NID / bank account / phone numbers in the UI.
//
//	Mask("017123456789", 4) == "********6789"
func Mask(s string, keep int) string {
	if keep < 0 {
		keep = 0
	}
	r := []rune(s)
	if len(r) <= keep {
		return s
	}
	return strings.Repeat("*", len(r)-keep) + string(r[len(r)-keep:])
}

// MaskEmail keeps the first character of the local part and the full
// domain visible.
//
//	MaskEmail("jhon.doe@acme.com") == "j***@acme.com"
func MaskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 {
		return Mask(email, 0)
	}
	name, domain := email[:at], email[at:]
	nameRunes := []rune(name)
	if len(nameRunes) <= 1 {
		return name + domain
	}
	return string(nameRunes[:1]) + strings.Repeat("*", len(nameRunes)-1) + domain
}

// Normalize collapses internal whitespace runs to a single space and trims
// the edges. Handy for search inputs and canonicalizing free-text fields
// (e.g. batch numbers) before storage/comparison.
func Normalize(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// IsBlank reports whether s contains only whitespace (or is empty).
func IsBlank(s string) bool { return strings.TrimSpace(s) == "" }

// StripControl removes ASCII control characters (0x00–0x1F and 0x7F) from
// s. Any client-supplied free-text field (name, note, header value) should
// pass through this before being logged or persisted — control characters
// have no legitimate place there and can otherwise corrupt console-format
// log lines or enable log/header injection.
func StripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// Truncate cuts `s` to at most n runes; appends "…" if it was cut.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Coalesce returns the first argument that is non-blank after trimming —
// but returns it UNTRIMMED, preserving the caller's exact original value.
// Use IsBlank/TrimSpace explicitly at the call site if you also need the
// returned value normalized.
func Coalesce(vals ...string) string {
	for _, v := range vals {
		if !IsBlank(v) {
			return v
		}
	}
	return ""
}

// PadNumber zero-pads n to at least `width` digits, e.g. PadNumber(123, 6)
// == "000123" — used for invoice/purchase-order number suffixes (see
// constants.InvoiceNoPadding / constants.PurchaseNoPadding). A negative n
// is padded with the '-' sign kept outside the zero-padding, e.g.
// PadNumber(-5, 4) == "-0005".
func PadNumber(n int64, width int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	if width > 0 && len(s) < width {
		s = strings.Repeat("0", width-len(s)) + s
	}
	if neg {
		return "-" + s
	}
	return s
}