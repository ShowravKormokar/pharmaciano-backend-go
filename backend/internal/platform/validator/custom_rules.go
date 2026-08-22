// Regexes precompiled once at package init. A malformed pattern here panics
// immediately at process startup (MustCompile) — fail fast rather than
// discovering a broken rule mid-request.

package validator

import (
	"backend/internal/platform/config"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/go-playground/validator/v10"
)

const strengthUnit = `mg/ml|mg/5ml|mcg/ml|iu/ml|mg|mcg|g|ml|iu|meq|mmol|%`

var (
	// Bangladeshi mobile numbers: +8801XXXXXXXXX or 01XXXXXXXXX (spaces and
	// dashes are stripped before matching — see phoneBD).
	rePhoneBD = regexp.MustCompile(`^(?:\+?880|0)1[3-9]\d{8}$`)

	// NID: 10 (smart card), 13, or 17 digits. Format-only — this does NOT
	// verify a checksum or that the number is actually issued; it just
	// rejects obviously malformed input before it reaches a service layer.
	reNID = regexp.MustCompile(`^\d{10}$|^\d{13}$|^\d{17}$`)

	// Batch numbers: letters, digits, dash, slash, dot; length 3–32.
	// Case-insensitive because manufacturers are inconsistent about casing
	// on printed batch codes — normalize to uppercase at the service layer
	// if a canonical form is needed for storage/lookup.
	reBatch = regexp.MustCompile(`(?i)^[A-Z0-9][A-Z0-9\-./]{2,31}$`)

	// Strength: "500 mg", "5 mg/ml", "125 mg/5ml", "0.5 %", "100 IU",
	// "100 IU/ml", "5 mEq", "2 mmol", and combination-drug ranges like
	// "500 mg-125 mg" (e.g. Augmentin-style labeling). Not exhaustive of
	// every pharma notation in existence — extend strengthUnit if a real
	// medicine entry gets rejected.
	reStrength = regexp.MustCompile(
		`(?i)^\s*\d+(?:\.\d+)?\s*(?:` + strengthUnit + `)` +
			`(?:\s*-\s*\d+(?:\.\d+)?\s*(?:` + strengthUnit + `))?\s*$`,
	)

	// ISO 4217 — 3 uppercase letters.
	reISOCurrency = regexp.MustCompile(`^[A-Z]{3}$`)
)

// registerCustomRules attaches every custom validator to the instance.
// pwPolicy configures the password_policy rule — see WithPasswordPolicy.
func registerCustomRules(v *validator.Validate, pwPolicy config.PasswordConfig) error {
	rules := map[string]validator.Func{
		"phone_bd":        phoneBD,
		"nid":             nid,
		"batch_no":        batchNo,
		"strength":        strength,
		"iso_currency":    isoCurrency,
		"iana_tz":         ianaTZ,
		"password_policy": passwordPolicyFunc(pwPolicy),
		"not_blank":       notBlank,
	}
	for tag, fn := range rules {
		if err := v.RegisterValidation(tag, fn); err != nil {
			return err
		}
	}
	return nil
}

// phone_bd: Bangladeshi mobile number. Empty is accepted here — pair with
// `required` on the field when the value is mandatory.
func phoneBD(fl validator.FieldLevel) bool {
	s := strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(fl.Field().String()))
	if s == "" {
		return true
	}
	return rePhoneBD.MatchString(s)
}

// nid: 10 / 13 / 17 digits.
func nid(fl validator.FieldLevel) bool {
	s := strings.TrimSpace(fl.Field().String())
	if s == "" {
		return true
	}
	return reNID.MatchString(s)
}

// batch_no: canonical batch number pattern.
func batchNo(fl validator.FieldLevel) bool {
	s := strings.TrimSpace(fl.Field().String())
	if s == "" {
		return true
	}
	return reBatch.MatchString(s)
}

// strength: "500 mg", "5 mg/ml", "0.5 %", "100 IU", "500 mg-125 mg", ...
func strength(fl validator.FieldLevel) bool {
	s := strings.TrimSpace(fl.Field().String())
	if s == "" {
		return true
	}
	return reStrength.MatchString(s)
}

// iso_currency: exactly 3 letters (case-insensitive input, normalized to
// upper before matching).
func isoCurrency(fl validator.FieldLevel) bool {
	s := strings.TrimSpace(fl.Field().String())
	if s == "" {
		return true
	}
	return reISOCurrency.MatchString(strings.ToUpper(s))
}

// iana_tz: parseable via time.LoadLocation.
func ianaTZ(fl validator.FieldLevel) bool {
	s := strings.TrimSpace(fl.Field().String())
	if s == "" {
		return true
	}
	_, err := time.LoadLocation(s)
	return err == nil
}

// passwordPolicyFunc closes over cfg so the rule enforces the app's actual
// configured policy (config.yaml: password.min_length, require_upper, ...)
// instead of a value baked in at compile time. Unlike the other rules
// here, an empty password is never accepted — MinLength already rejects it.
func passwordPolicyFunc(cfg config.PasswordConfig) validator.Func {
	minLen := cfg.MinLength
	if minLen <= 0 {
		minLen = 10 // defensive floor if config validation is ever bypassed
	}
	return func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if len(s) < minLen {
			return false
		}
		var upper, lower, digit, symbol bool
		for _, r := range s {
			switch {
			case unicode.IsUpper(r):
				upper = true
			case unicode.IsLower(r):
				lower = true
			case unicode.IsDigit(r):
				digit = true
			case unicode.IsPunct(r) || unicode.IsSymbol(r):
				symbol = true
			}
		}
		if cfg.RequireUpper && !upper {
			return false
		}
		if cfg.RequireLower && !lower {
			return false
		}
		if cfg.RequireDigit && !digit {
			return false
		}
		if cfg.RequireSymbol && !symbol {
			return false
		}
		return true
	}
}

// not_blank: rejects strings that are only whitespace.
func notBlank(fl validator.FieldLevel) bool {
	return strings.TrimSpace(fl.Field().String()) != ""
}
