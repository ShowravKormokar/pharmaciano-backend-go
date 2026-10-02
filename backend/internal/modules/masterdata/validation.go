// Reusable, deterministic validation for the eleven MasterData resources.
//
// Everything here is pure: no database, cache, network or clock access. The
// split of responsibility is deliberate and matches contract §12:
//
//   - THIS FILE — transport-shape rules only. Required, length, numeric
//     representability, syntax of things carried in the payload, and pair rules
//     that can be decided from the payload alone.
//   - SERVICE LAYER (Phase 05/06) — "does this parent row exist?", "does this
//     PATCH merge into a still-valid row?", "is this a multi-node cycle?".
//   - POSTGRESQL — the authoritative backstop, including the constraints added
//     by Phase 02 in 000020_masterdata_integrity.
//
// A rule is only enforced here when it is derivable from the Phase 02 schema
// (representability) or when the owner has directed it explicitly (tax sign).
// Business bounds that contract §11.2 still lists as UNRESOLVED are NOT
// enforced — see the notes on conversion_factor and the tax maximum.

package masterdata

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"

	pkgvalidator "github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	uuidx "backend/internal/platform/uuid"
	"backend/internal/platform/validator"
	"backend/pkg/pagination"
)

// ---------------------------------------------------------------------------
// Schema limits — transcribed from migrations/000005_master_data.up.sql
// ---------------------------------------------------------------------------
//
// These mirror the `max=` values in the dto.go tags. TestTagLengthsMatchSchema
// asserts the two stay in step, so editing one without the other fails a test
// rather than drifting silently.

const (
	// MaxCodeLen is code VARCHAR(40), identical on all fifteen 000005 tables.
	MaxCodeLen = 40

	// MaxNameLenShort covers routes, dosage_forms, package_types, unit_types,
	// storage_conditions and tax_rates — name VARCHAR(120).
	MaxNameLenShort = 120

	// MaxNameLenMedium covers product_categories, drug_groups, drug_classes and
	// therapeutic_classes — name VARCHAR(160).
	MaxNameLenMedium = 160

	// MaxNameLenLong covers generic_medicines — name VARCHAR(200).
	MaxNameLenLong = 200

	// MaxSymbolLen is unit_types.symbol VARCHAR(20).
	MaxSymbolLen = 20
)

// NUMERIC(p,s) declarations from 000005. Only the precision bound is enforced:
// it is the one that makes PostgreSQL raise 22003 (numeric field overflow). A
// value carrying more decimals than `scale` is rounded by PostgreSQL rather than
// rejected, so enforcing scale here would reject input the database accepts and
// is deliberately left out.
const (
	conversionFactorPrecision = 15
	conversionFactorScale     = 6

	temperaturePrecision = 5
	temperatureScale     = 2

	ratePercentPrecision = 6
	ratePercentScale     = 3
)

// ---------------------------------------------------------------------------
// Sorting
// ---------------------------------------------------------------------------

// baseSortColumns mirrors warehouse/repository.go:130-135 — the four columns
// every MasterData list endpoint may sort by. `id` is deliberately absent; the
// repository appends it as a tie-breaker, exactly as warehouse does.
var baseSortColumns = map[string]struct{}{
	"code":       {},
	"name":       {},
	"created_at": {},
	"is_active":  {},
}

// Per-resource sort allow-lists, derived from baseSortColumns plus the columns
// that only make sense for that resource.
var (
	productCategorySortColumns  = withSortColumns("sort_order")
	dosageFormSortColumns       = baseSortColumns
	routeSortColumns            = baseSortColumns
	packageTypeSortColumns      = baseSortColumns
	unitTypeSortColumns         = baseSortColumns
	storageConditionSortColumns = baseSortColumns
	taxRateSortColumns          = withSortColumns("is_default", "rate_percent")
	drugGroupSortColumns        = baseSortColumns
	drugClassSortColumns        = baseSortColumns
	therapeuticClassSortColumns = baseSortColumns
	genericMedicineSortColumns  = withSortColumns("is_controlled")
)

// withSortColumns returns the base allow-list plus extra columns, as a fresh map
// so a caller can never mutate the shared base set.
func withSortColumns(extra ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(baseSortColumns)+len(extra))
	for k := range baseSortColumns {
		out[k] = struct{}{}
	}
	for _, k := range extra {
		out[k] = struct{}{}
	}
	return out
}

// ResolveSort parses a raw `sort=` string against an allow-list and returns only
// fields that survived whitelist validation.
//
// A malformed or disallowed value yields an empty slice rather than an error, so
// the caller falls back to its deterministic default ORDER BY. This mirrors
// warehouse.buildOrderBy (repository.go:214-218) and is the reason no client
// input can ever reach the ORDER BY clause: an unrecognised field is dropped
// here, never passed through.
func ResolveSort(sort string, allowed map[string]struct{}) []pagination.SortField {
	fields, err := pagination.ParseSort(sort, allowed)
	if err != nil {
		return nil
	}
	return fields
}

// ---------------------------------------------------------------------------
// Numeric representability
// ---------------------------------------------------------------------------

// fitsNumeric reports whether v is representable by a PostgreSQL NUMERIC(p,s).
//
// The value is first rounded to `scale` decimals — PostgreSQL rounds rather than
// rejecting extra precision — and the magnitude is then compared against the
// precision limit. Rounding first matters: NUMERIC(5,2) rejects 999.999 because
// it rounds up to 1000.00 and overflows, but accepts 999.99.
//
// NaN and ±Inf are always rejected: they have no NUMERIC representation, and
// encoding/json cannot carry them in a body anyway — this guards the
// form-bound and programmatic paths.
func fitsNumeric(v float64, precision, scale int) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	factor := math.Pow10(scale)
	rounded := math.Round(v*factor) / factor
	return math.Abs(rounded) < math.Pow10(precision-scale)
}

// FitsConversionFactor reports whether v fits unit_types.conversion_factor
// NUMERIC(15,6).
//
// It checks representability ONLY. The positive-value rule requested for this
// field is NOT applied: contract §11.2 keeps conversion_factor bounds UNRESOLVED
// and seed/unit_types.yaml:25 describes the column as a per-medicine default
// rather than an invariant, so `gt=0` here would invent a business rule.
func FitsConversionFactor(v float64) bool {
	return fitsNumeric(v, conversionFactorPrecision, conversionFactorScale)
}

// FitsTemperature reports whether v fits the NUMERIC(5,2) columns on
// storage_conditions.
func FitsTemperature(v float64) bool {
	return fitsNumeric(v, temperaturePrecision, temperatureScale)
}

// FitsRatePercent reports whether v fits tax_rates.rate_percent NUMERIC(6,3).
func FitsRatePercent(v float64) bool {
	return fitsNumeric(v, ratePercentPrecision, ratePercentScale)
}

// ---------------------------------------------------------------------------
// Limits — configuration-driven validation
// ---------------------------------------------------------------------------

// Limits carries the MasterData validation bounds that are business policy
// rather than schema shape, so they live in exactly one place instead of being
// duplicated across validators.
type Limits struct {
	// MaxRatePercent is the upper bound for tax_rates.rate_percent.
	//
	// There is deliberately no default. Contract §11.2 and Phase 02 (§26.4) both
	// record the tax maximum as UNRESOLVED, and no key in config/config.yaml
	// defines one, so this phase will not invent 100. A zero or negative value
	// means "no business maximum configured", in which case only the
	// schema-derived rules apply. Wire the real value through RegisterValidations
	// once the owner decides it.
	MaxRatePercent float64
}

// ValidateRatePercent applies the rules that can be decided from the value
// alone: representability for NUMERIC(6,3) and, when configured, the business
// maximum.
//
// Sign is deliberately NOT checked here — the `gte=0` tag on the DTO owns it.
func (l Limits) ValidateRatePercent(v float64) error {
	if !FitsRatePercent(v) {
		return fmt.Errorf("%w: rate_percent %v is not a finite NUMERIC(6,3) value", ErrInvalidNumber, v)
	}
	if l.MaxRatePercent > 0 && v > l.MaxRatePercent {
		return fmt.Errorf("%w: rate_percent %v exceeds the configured maximum %v",
			ErrRateAboveMaximum, v, l.MaxRatePercent)
	}
	return nil
}

// Sentinel errors. These carry no HTTP mapping — Phase 05 turns them into
// errs.Validation — but they make the failure modes assertable in tests.
var (
	// ErrInvalidNumber means the value cannot be represented by its column type.
	ErrInvalidNumber = errors.New("masterdata: value not representable by column type")

	// ErrRateAboveMaximum means rate_percent exceeds Limits.MaxRatePercent.
	ErrRateAboveMaximum = errors.New("masterdata: rate_percent above configured maximum")

	// ErrTemperatureRange means min_temp_celsius > max_temp_celsius.
	ErrTemperatureRange = errors.New("masterdata: min_temp_celsius exceeds max_temp_celsius")

	// ErrNilReference means a foreign-key field carried the all-zero UUID.
	ErrNilReference = errors.New("masterdata: reference must not be the nil UUID")

	// ErrSelfReference means a foreign key points at the row's own id.
	ErrSelfReference = errors.New("masterdata: reference must not point at the resource itself")
)

// ---------------------------------------------------------------------------
// Self-reference — needs the row id, so it is a method, not a tag
// ---------------------------------------------------------------------------

// ValidateSelfReference rejects a foreign key that points at the row being
// updated. On CREATE there is no id yet, so this does not apply.
//
// This is a convenience for the service layer's early 400. The authoritative
// enforcement is ck_product_categories_parent_not_self /
// ck_unit_types_base_unit_not_self in 000020, which rejects the write whatever
// path it arrives by.
func ValidateSelfReference(field string, self uuid.UUID, ref *uuid.UUID) error {
	if ref == nil || *ref != self {
		return nil
	}
	return fmt.Errorf("%w: %s equals the resource id", ErrSelfReference, field)
}

// ValidateSelfReference rejects parent_id == the category's own id.
func (r UpdateProductCategoryRequest) ValidateSelfReference(id uuid.UUID) error {
	return ValidateSelfReference("parent_id", id, r.ParentID)
}

// ValidateSelfReference rejects base_unit_id == the unit's own id.
func (r UpdateUnitTypeRequest) ValidateSelfReference(id uuid.UUID) error {
	return ValidateSelfReference("base_unit_id", id, r.BaseUnitID)
}

// ---------------------------------------------------------------------------
// Pair rules
// ---------------------------------------------------------------------------

// ValidateTemperatureRange rejects an inverted band. Both bounds are optional:
// a nil end means "unbounded on that side" and is accepted.
func ValidateTemperatureRange(min, max *float64) error {
	if min == nil || max == nil {
		return nil
	}
	if !FitsTemperature(*min) {
		return fmt.Errorf("%w: min_temp_celsius %v is not a finite NUMERIC(5,2) value", ErrInvalidNumber, *min)
	}
	if !FitsTemperature(*max) {
		return fmt.Errorf("%w: max_temp_celsius %v is not a finite NUMERIC(5,2) value", ErrInvalidNumber, *max)
	}
	if *min > *max {
		return fmt.Errorf("%w: %v > %v", ErrTemperatureRange, *min, *max)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Struct-level rules
// ---------------------------------------------------------------------------

// referenceTypes is every request type that can carry a foreign key. These get
// the nil-UUID check.
var referenceTypes = []any{
	CreateProductCategoryRequest{}, UpdateProductCategoryRequest{},
	CreateDosageFormRequest{}, UpdateDosageFormRequest{},
	CreateRouteRequest{}, UpdateRouteRequest{},
	CreatePackageTypeRequest{}, UpdatePackageTypeRequest{},
	CreateUnitTypeRequest{}, UpdateUnitTypeRequest{},
	CreateStorageConditionRequest{}, UpdateStorageConditionRequest{},
	CreateTaxRateRequest{}, UpdateTaxRateRequest{},
	CreateDrugGroupRequest{}, UpdateDrugGroupRequest{},
	CreateDrugClassRequest{}, UpdateDrugClassRequest{},
	CreateTherapeuticClassRequest{}, UpdateTherapeuticClassRequest{},
	CreateGenericMedicineRequest{}, UpdateGenericMedicineRequest{},
	ListMasterDataQuery{},
}

// RegisterValidations installs the struct-level rules for the MasterData DTOs on
// the shared validator. Call it once at startup, next to where validator.New is
// built (cmd/api/main.go), passing the configured Limits.
//
// IMPORTANT — one rule per type. go-playground keys struct-level validators by
// reflect.Type in a single map, so registering a second function for a type
// silently REPLACES the first rather than adding to it. The storage-condition and
// tax-rate types therefore get one combined rule that runs the nil-UUID check
// and their type-specific check; every other type gets the nil-UUID check alone.
func RegisterValidations(v *validator.Validator, limits Limits) {
	if v == nil {
		return
	}
	combined := masterDataStructRule(limits)
	v.RegisterStructValidation(combined,
		CreateStorageConditionRequest{}, UpdateStorageConditionRequest{},
		CreateTaxRateRequest{}, UpdateTaxRateRequest{})

	referenceOnly := make([]any, 0, len(referenceTypes))
	for _, typ := range referenceTypes {
		switch typ.(type) {
		case CreateStorageConditionRequest, UpdateStorageConditionRequest,
			CreateTaxRateRequest, UpdateTaxRateRequest:
			continue
		}
		referenceOnly = append(referenceOnly, typ)
	}
	v.RegisterStructValidation(nilReferencesRule, referenceOnly...)
}

// masterDataStructRule is the combined rule for the four types that need both
// the nil-UUID check and a cross-field numeric check. limits is captured in the
// closure so the tax maximum comes from configuration, not a literal.
func masterDataStructRule(limits Limits) pkgvalidator.StructLevelFunc {
	return func(sl pkgvalidator.StructLevel) {
		nilReferencesRule(sl)
		switch s := sl.Current().Interface().(type) {
		case CreateStorageConditionRequest:
			reportTemperature(sl, s.MinTempCelsius, s.MaxTempCelsius)
		case UpdateStorageConditionRequest:
			reportTemperature(sl, s.MinTempCelsius, s.MaxTempCelsius)
		case CreateTaxRateRequest:
			if s.RatePercent != nil {
				reportRatePercent(sl, limits, *s.RatePercent)
			}
		case UpdateTaxRateRequest:
			if s.RatePercent != nil {
				reportRatePercent(sl, limits, *s.RatePercent)
			}
		}
	}
}

// reportTemperature maps ValidateTemperatureRange onto a rule name the shared
// validator can render as a static message.
func reportTemperature(sl pkgvalidator.StructLevel, min, max *float64) {
	err := ValidateTemperatureRange(min, max)
	switch {
	case err == nil:
	case errors.Is(err, ErrTemperatureRange):
		reportFieldError(sl, "min_temp_celsius", ruleTemperatureRange, err)
	default:
		reportFieldError(sl, "min_temp_celsius", ruleInvalidNumber, err)
	}
}

// reportRatePercent maps Limits.ValidateRatePercent onto a rule name.
func reportRatePercent(sl pkgvalidator.StructLevel, limits Limits, v float64) {
	err := limits.ValidateRatePercent(v)
	switch {
	case err == nil:
	case errors.Is(err, ErrRateAboveMaximum):
		reportFieldError(sl, "rate_percent", ruleAboveMaximum, err)
	default:
		reportFieldError(sl, "rate_percent", ruleInvalidNumber, err)
	}
}

// nilReferencesRule walks the struct and rejects any non-nil *uuid.UUID field
// holding the all-zero UUID. Reflection is used because the reference fields
// carry eleven different names across the request types; a hard-coded field-name
// list would be more code and one more thing to forget when a resource is added.
func nilReferencesRule(sl pkgvalidator.StructLevel) {
	v := sl.Current()
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.Kind() != reflect.Ptr || field.Type().Elem() != reflect.TypeOf(uuid.UUID{}) {
			continue
		}
		if field.IsNil() {
			continue
		}
		if uuidx.IsNil(*field.Interface().(*uuid.UUID)) {
			reportFieldError(sl, jsonFieldName(t.Field(i)), ruleNilUUID, ErrNilReference)
		}
	}
}

// jsonFieldName returns the wire name of a struct field, matching what the
// client's body used, so a field error names "parent_id" and not "ParentID".
func jsonFieldName(f reflect.StructField) string {
	name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
	if name == "" || name == "-" {
		return f.Name
	}
	return name
}

// reportFieldError attaches err to the named field. The field name is passed
// verbatim as the JSON name because struct-level errors bypass the validator's
// TagNameFunc, which would otherwise report the Go identifier.
//
// rule is one of the ruleName* constants rather than a single generic tag: the
// shared validator's humanMessage can only return a static string per tag, so a
// single tag would leave every cross-field failure reading "failed a master data
// cross-field rule" with no indication of which rule tripped.
func reportFieldError(sl pkgvalidator.StructLevel, field, rule string, err error) {
	if err != nil {
		// The library signature is ReportError(field, fieldName, structFieldName,
		// tag, param); fieldName is what Field() returns and what fieldPath in the
		// shared validator surfaces to the client, so it is what carries the JSON
		// name. structFieldName only feeds the namespace, which the platform's
		// fieldPath discards for a top-level struct.
		sl.ReportError(err, field, field, rule, "")
	}
}

// Rule names reported for the struct-level checks. Each has a matching static
// case in the shared validator's humanMessage, so the `rule` a client sees is
// specific and machine-readable.
const (
	ruleNilUUID          = "nil_uuid"
	ruleTemperatureRange = "temperature_range"
	ruleInvalidNumber    = "invalid_number"
	ruleAboveMaximum     = "above_maximum"

	// ruleInvalidUUID is reported by the service, not by a struct rule: a
	// malformed ?parent_id= is only detectable once the raw form value has been
	// parsed, which happens after binding. It is declared here so the
	// service and the validator speak the same rule vocabulary.
	ruleInvalidUUID = "invalid_uuid"
)
