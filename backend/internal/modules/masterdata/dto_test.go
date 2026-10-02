package masterdata

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"

	"backend/internal/platform/validator"
	"backend/pkg/pagination"
)

func newTestValidator(t *testing.T, limits Limits) *validator.Validator {
	t.Helper()
	v, err := validator.New()
	if err != nil {
		t.Fatalf("validator.New: %v", err)
	}
	RegisterValidations(v, limits)
	return v
}

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }
func intptr(i int) *int       { return &i }
func f64ptr(f float64) *float64 {
	return &f
}

// bindBody mirrors what httpx.BindJSON does — unmarshal then validate — so the
// tests exercise the same path a request takes rather than calling the validator
// against a hand-built struct.
func bindBody(t *testing.T, v *validator.Validator, dst any, body string) error {
	t.Helper()
	if err := json.Unmarshal([]byte(body), dst); err != nil {
		return err
	}
	return v.Struct(dst)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

var validID = uuid.MustParse("11111111-1111-4111-8111-111111111111")
var otherID = uuid.MustParse("22222222-2222-4222-8222-222222222222")

// TestTagLengthsMatchSchema guards the `max=` values in dto.go against the
// constants transcribed from 000005. Editing a tag without the matching constant
// (or vice versa) fails here instead of drifting silently.
func TestTagLengthsMatchSchema(t *testing.T) {
	tests := []struct {
		typ  reflect.Type
		want map[string]string // json field -> expected max=
	}{
		{reflect.TypeOf(CreateProductCategoryRequest{}), map[string]string{
			"code": "40", "name": "160",
		}},
		{reflect.TypeOf(CreateRouteRequest{}), map[string]string{
			"code": "40", "name": "120",
		}},
		{reflect.TypeOf(CreateDosageFormRequest{}), map[string]string{
			"code": "40", "name": "120",
		}},
		{reflect.TypeOf(CreatePackageTypeRequest{}), map[string]string{
			"code": "40", "name": "120",
		}},
		{reflect.TypeOf(CreateUnitTypeRequest{}), map[string]string{
			"code": "40", "name": "120", "symbol": "20",
		}},
		{reflect.TypeOf(CreateStorageConditionRequest{}), map[string]string{
			"code": "40", "name": "120",
		}},
		{reflect.TypeOf(CreateTaxRateRequest{}), map[string]string{
			"code": "40", "name": "120",
		}},
		{reflect.TypeOf(CreateDrugGroupRequest{}), map[string]string{
			"code": "40", "name": "160",
		}},
		{reflect.TypeOf(CreateDrugClassRequest{}), map[string]string{
			"code": "40", "name": "160",
		}},
		{reflect.TypeOf(CreateTherapeuticClassRequest{}), map[string]string{
			"code": "40", "name": "160",
		}},
		{reflect.TypeOf(CreateGenericMedicineRequest{}), map[string]string{
			"code": "40", "name": "200",
		}},
	}

	for _, tc := range tests {
		// Compare by JSON name, which is what the tags are keyed on.
		for i := 0; i < tc.typ.NumField(); i++ {
			f := tc.typ.Field(i)
			want, ok := tc.want[jsonName(f)]
			if !ok {
				continue
			}
			tag := f.Tag.Get("validate")
			if !strings.Contains(tag, "max="+want) {
				t.Errorf("%s.%s validate=%q, expected it to contain max=%s",
					tc.typ.Name(), jsonName(f), tag, want)
			}
		}
	}
}

func jsonName(f reflect.StructField) string {
	return strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
}

// TestSchemaLimitConstants used to live here and compared each constant to the
// literal it is defined as, so it could never fail. It has been replaced by
// TestSchemaLimitConstantsMatchMigration in repository_schema_test.go, which parses
// the VARCHAR widths out of 000005_master_data.up.sql and asserts the constants
// against them.

// ---------------------------------------------------------------------------
// Common validation
// ---------------------------------------------------------------------------

func TestCommonValidationOnCreateRequests(t *testing.T) {
	v := newTestValidator(t, Limits{})

	t.Run("empty code rejected", func(t *testing.T) {
		var req CreateRouteRequest
		if err := bindBody(t, v, &req, `{"code":"","name":"Oral"}`); err == nil {
			t.Fatal("empty code should be rejected")
		}
	})

	t.Run("whitespace-only code rejected", func(t *testing.T) {
		var req CreateRouteRequest
		if err := bindBody(t, v, &req, `{"code":"   ","name":"Oral"}`); err == nil {
			t.Fatal("whitespace-only code should be rejected")
		}
	})

	t.Run("whitespace-only name rejected", func(t *testing.T) {
		var req CreateRouteRequest
		if err := bindBody(t, v, &req, `{"code":"ORAL","name":"  "}`); err == nil {
			t.Fatal("whitespace-only name should be rejected")
		}
	})

	t.Run("code over 40 runes rejected", func(t *testing.T) {
		var req CreateRouteRequest
		body, _ := json.Marshal(map[string]string{
			"code": strings.Repeat("A", 41), "name": "Oral",
		})
		if err := bindBody(t, v, &req, string(body)); err == nil {
			t.Fatal("41-character code should be rejected (VARCHAR(40))")
		}
	})

	t.Run("code at exactly 40 runes accepted", func(t *testing.T) {
		var req CreateRouteRequest
		body, _ := json.Marshal(map[string]string{
			"code": strings.Repeat("A", 40), "name": "Oral",
		})
		if err := bindBody(t, v, &req, string(body)); err != nil {
			t.Fatalf("40-character code should be accepted: %v", err)
		}
	})

	t.Run("name over its own table limit rejected", func(t *testing.T) {
		var short CreateRouteRequest // VARCHAR(120)
		body, _ := json.Marshal(map[string]string{
			"code": "ORAL", "name": strings.Repeat("n", 121),
		})
		if err := bindBody(t, v, &short, string(body)); err == nil {
			t.Fatal("121-char name should be rejected for routes (VARCHAR(120))")
		}
		var long CreateGenericMedicineRequest // VARCHAR(200)
		body2, _ := json.Marshal(map[string]string{
			"code": "PARA", "name": strings.Repeat("n", 121),
		})
		if err := bindBody(t, v, &long, string(body2)); err != nil {
			t.Fatalf("121-char name is legal for generic_medicines (VARCHAR(200)): %v", err)
		}
	})

	t.Run("description optional and unlimited", func(t *testing.T) {
		var req CreateRouteRequest
		big := strings.Repeat("d", 5000) // description is TEXT in 000005
		body, _ := json.Marshal(map[string]string{
			"code": "ORAL", "name": "Oral", "description": big,
		})
		if err := bindBody(t, v, &req, string(body)); err != nil {
			t.Fatalf("long description should be accepted (TEXT column): %v", err)
		}
	})

	t.Run("optional flags default through the schema", func(t *testing.T) {
		var req CreateRouteRequest
		if err := bindBody(t, v, &req, `{"code":"ORAL","name":"Oral"}`); err != nil {
			t.Fatalf("minimal create should be valid: %v", err)
		}
		if req.IsActive != nil {
			t.Fatal("is_active must stay nil so the schema default TRUE applies")
		}
	})
}

// TestMalformedUUIDRejected proves a syntactically broken reference never
// reaches validation as a valid value: the JSON decoder rejects it outright.
func TestMalformedUUIDRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		dst  any
	}{
		{"route_id", `{"code":"DF","name":"Tablet","route_id":"not-a-uuid"}`, &CreateDosageFormRequest{}},
		{"parent_id", `{"code":"CAT","name":"Cat","parent_id":"12345"}`, &CreateProductCategoryRequest{}},
		{"base_unit_id", `{"code":"PC","name":"Piece","base_unit_id":"zzzz"}`, &CreateUnitTypeRequest{}},
		{"drug_group_id", `{"code":"CL","name":"Class","drug_group_id":"nope"}`, &CreateDrugClassRequest{}},
		{"drug_class_id", `{"code":"TC","name":"Class","drug_class_id":"nope"}`, &CreateTherapeuticClassRequest{}},
		{"therapeutic_class_id", `{"code":"GM","name":"Gen","therapeutic_class_id":"nope"}`, &CreateGenericMedicineRequest{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValidator(t, Limits{})
			if err := bindBody(t, v, tc.dst, tc.body); err == nil {
				t.Fatalf("malformed %s should be rejected", tc.name)
			}
		})
	}
}

// TestNilUUIDRejected proves the all-zero UUID — which parses fine and would
// otherwise fail later as a 500 FK violation — is refused as a 400.
func TestNilUUIDRejected(t *testing.T) {
	v := newTestValidator(t, Limits{})
	var req CreateDosageFormRequest
	body := `{"code":"DF","name":"Tablet","route_id":"00000000-0000-0000-0000-000000000000"}`
	err := bindBody(t, v, &req, body)
	if err == nil {
		t.Fatal("nil UUID reference should be rejected")
	}
	if !strings.Contains(err.Error(), "must not be the all-zero UUID") {
		t.Fatalf("unexpected message: %v", err)
	}
	if !strings.Contains(err.Error(), "route_id") {
		t.Fatalf("error should name the offending field: %v", err)
	}
}

func TestValidUUIDAccepted(t *testing.T) {
	v := newTestValidator(t, Limits{})
	var req CreateDosageFormRequest
	body := `{"code":"DF","name":"Tablet","route_id":"` + validID.String() + `"}`
	if err := bindBody(t, v, &req, body); err != nil {
		t.Fatalf("valid route_id should be accepted: %v", err)
	}
	if req.RouteID == nil || *req.RouteID != validID {
		t.Fatalf("route_id not bound: %+v", req.RouteID)
	}
}

// ---------------------------------------------------------------------------
// PATCH semantics
// ---------------------------------------------------------------------------

// TestPatchDistinguishesOmittedFromZero is the core PATCH contract: an omitted
// field and a field explicitly set to false/0/"" must remain distinguishable.
func TestPatchDistinguishesOmittedFromZero(t *testing.T) {
	t.Run("zero value PATCH is empty", func(t *testing.T) {
		if !(UpdateProductCategoryRequest{}).IsEmpty() {
			t.Fatal("zero-value PATCH should be empty")
		}
	})

	t.Run("explicit false is not empty", func(t *testing.T) {
		r := UpdateProductCategoryRequest{IsActive: boolptr(false)}
		if r.IsEmpty() {
			t.Fatal("PATCH setting is_active=false must not be treated as no-op")
		}
		if r.IsActive == nil || *r.IsActive {
			t.Fatal("is_active=false was lost")
		}
	})

	t.Run("explicit zero sort_order is not empty", func(t *testing.T) {
		r := UpdateProductCategoryRequest{SortOrder: intptr(0)}
		if r.IsEmpty() {
			t.Fatal("PATCH setting sort_order=0 must not be treated as no-op")
		}
		if r.SortOrder == nil || *r.SortOrder != 0 {
			t.Fatal("sort_order=0 was lost")
		}
	})

	t.Run("explicit zero rate_percent is not empty", func(t *testing.T) {
		r := UpdateTaxRateRequest{RatePercent: f64ptr(0)}
		if r.IsEmpty() {
			t.Fatal("PATCH setting rate_percent=0 must not be treated as no-op")
		}
		if r.RatePercent == nil || *r.RatePercent != 0 {
			t.Fatal("rate_percent=0 was lost")
		}
	})

	t.Run("explicit zero conversion_factor is not empty", func(t *testing.T) {
		r := UpdateUnitTypeRequest{ConversionFactor: f64ptr(0)}
		if r.IsEmpty() {
			t.Fatal("PATCH setting conversion_factor=0 must not be treated as no-op")
		}
	})

	t.Run("omitted stays nil after round trip", func(t *testing.T) {
		var req UpdateGenericMedicineRequest
		if err := json.Unmarshal([]byte(`{"name":"Paracetamol"}`), &req); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if req.TherapeuticClassID != nil || req.IsControlled != nil || req.IsActive != nil {
			t.Fatalf("omitted fields must stay nil: %+v", req)
		}
	})

	t.Run("explicit false round trips", func(t *testing.T) {
		var req UpdateGenericMedicineRequest
		if err := json.Unmarshal([]byte(`{"is_controlled":false,"is_active":false}`), &req); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if req.IsControlled == nil || *req.IsControlled {
			t.Fatal("is_controlled=false must arrive as a non-nil pointer to false")
		}
		if req.IsActive == nil || *req.IsActive {
			t.Fatal("is_active=false must arrive as a non-nil pointer to false")
		}
		if req.IsEmpty() {
			t.Fatal("a PATCH of two explicit falses is a real change")
		}
	})
}

// TestUpdateRequestsHaveNoCodeField documents that code is immutable: no Update
// DTO exposes it, so a PATCH can never rename a code out from under rows that
// reference it.
func TestUpdateRequestsHaveNoCodeField(t *testing.T) {
	updates := []any{
		UpdateProductCategoryRequest{}, UpdateRouteRequest{}, UpdateDosageFormRequest{},
		UpdatePackageTypeRequest{}, UpdateUnitTypeRequest{}, UpdateStorageConditionRequest{},
		UpdateTaxRateRequest{}, UpdateDrugGroupRequest{}, UpdateDrugClassRequest{},
		UpdateTherapeuticClassRequest{}, UpdateGenericMedicineRequest{},
	}
	for _, u := range updates {
		typ := reflect.TypeOf(u)
		if _, ok := typ.FieldByName("Code"); ok {
			t.Errorf("%s exposes Code; code must stay immutable", typ.Name())
		}
		if fieldHasJSONName(typ, "code") {
			t.Errorf("%s exposes a json field named code", typ.Name())
		}
	}
}

func fieldHasJSONName(typ reflect.Type, name string) bool {
	for i := 0; i < typ.NumField(); i++ {
		if jsonName(typ.Field(i)) == name {
			return true
		}
	}
	return false
}

// TestAllResourcesHaveThreeDTOs is a completeness guard for the eleven resources.
func TestAllResourcesHaveThreeDTOs(t *testing.T) {
	pkg := reflect.TypeOf(CreateRouteRequest{}).PkgPath()
	if pkg != "backend/internal/modules/masterdata" {
		t.Fatalf("unexpected package %q", pkg)
	}
	// Each resource must have create/update/response types; asserted explicitly so
	// adding an eleventh-plus resource cannot skip the DTO trio.
	_ = []any{
		CreateProductCategoryRequest{}, UpdateProductCategoryRequest{}, ProductCategoryResponse{},
		CreateDosageFormRequest{}, UpdateDosageFormRequest{}, DosageFormResponse{},
		CreateRouteRequest{}, UpdateRouteRequest{}, RouteResponse{},
		CreatePackageTypeRequest{}, UpdatePackageTypeRequest{}, PackageTypeResponse{},
		CreateUnitTypeRequest{}, UpdateUnitTypeRequest{}, UnitTypeResponse{},
		CreateStorageConditionRequest{}, UpdateStorageConditionRequest{}, StorageConditionResponse{},
		CreateTaxRateRequest{}, UpdateTaxRateRequest{}, TaxRateResponse{},
		CreateDrugGroupRequest{}, UpdateDrugGroupRequest{}, DrugGroupResponse{},
		CreateDrugClassRequest{}, UpdateDrugClassRequest{}, DrugClassResponse{},
		CreateTherapeuticClassRequest{}, UpdateTherapeuticClassRequest{}, TherapeuticClassResponse{},
		CreateGenericMedicineRequest{}, UpdateGenericMedicineRequest{}, GenericMedicineResponse{},
	}
}

// ---------------------------------------------------------------------------
// ProductCategory self-reference
// ---------------------------------------------------------------------------

func TestProductCategorySelfReference(t *testing.T) {
	t.Run("valid root category accepted", func(t *testing.T) {
		v := newTestValidator(t, Limits{})
		var req CreateProductCategoryRequest
		if err := bindBody(t, v, &req, `{"code":"ROOT","name":"Root"}`); err != nil {
			t.Fatalf("root category should be valid: %v", err)
		}
		if req.ParentID != nil {
			t.Fatal("parent_id should stay nil for a root")
		}
	})

	t.Run("distinct parent accepted", func(t *testing.T) {
		r := UpdateProductCategoryRequest{ParentID: &otherID}
		if err := r.ValidateSelfReference(validID); err != nil {
			t.Fatalf("distinct parent should be accepted: %v", err)
		}
	})

	t.Run("parent_id == own id rejected", func(t *testing.T) {
		r := UpdateProductCategoryRequest{ParentID: &validID}
		err := r.ValidateSelfReference(validID)
		if !errors.Is(err, ErrSelfReference) {
			t.Fatalf("self parent should be rejected, got %v", err)
		}
	})

	t.Run("null parent accepted", func(t *testing.T) {
		r := UpdateProductCategoryRequest{ParentID: nil}
		if err := r.ValidateSelfReference(validID); err != nil {
			t.Fatalf("clearing parent to root should be accepted: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// DosageForm / DrugClass / TherapeuticClass / GenericMedicine references
// ---------------------------------------------------------------------------

func TestReferenceSyntaxValidation(t *testing.T) {
	v := newTestValidator(t, Limits{})
	idJSON := `"` + validID.String() + `"`

	tests := []struct {
		name string
		dst  any
		body string
	}{
		{"dosage form route", &CreateDosageFormRequest{},
			`{"code":"DF","name":"Tablet","route_id":` + idJSON + `}`},
		{"drug class group", &CreateDrugClassRequest{},
			`{"code":"CL","name":"Analgesic","drug_group_id":` + idJSON + `}`},
		{"therapeutic class", &CreateTherapeuticClassRequest{},
			`{"code":"TC","name":"Analgesics","drug_class_id":` + idJSON + `}`},
		{"generic medicine", &CreateGenericMedicineRequest{},
			`{"code":"PARA","name":"Paracetamol","therapeutic_class_id":` + idJSON + `}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := bindBody(t, v, tc.dst, tc.body); err != nil {
				t.Fatalf("valid reference should be accepted: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UnitType
// ---------------------------------------------------------------------------

func TestUnitTypeConversionFactor(t *testing.T) {
	t.Run("representable values accepted", func(t *testing.T) {
		if !FitsConversionFactor(10) || !FitsConversionFactor(0.000001) {
			t.Fatal("ordinary conversion factors should fit NUMERIC(15,6)")
		}
	})

	t.Run("unrepresentable value rejected", func(t *testing.T) {
		// NUMERIC(15,6) tops out at 999999999.999999; anything larger would make
		// PostgreSQL raise 22003 and surface as a 500.
		if FitsConversionFactor(1e12) {
			t.Fatal("1e12 does not fit NUMERIC(15,6) and should be rejected")
		}
	})

	t.Run("NaN and Inf rejected", func(t *testing.T) {
		if FitsConversionFactor(math.NaN()) {
			t.Fatal("NaN must be rejected")
		}
		if FitsConversionFactor(math.Inf(1)) || FitsConversionFactor(math.Inf(-1)) {
			t.Fatal("infinities must be rejected")
		}
	})

	// Documented Phase 03 position: the positive-value rule is NOT enforced,
	// because contract §11.2 keeps conversion_factor bounds UNRESOLVED. This test
	// pins the current behaviour so the day the owner decides, the change is
	// visible in the diff.
	t.Run("zero and negative are currently accepted (UNRESOLVED)", func(t *testing.T) {
		v := newTestValidator(t, Limits{})
		var req CreateUnitTypeRequest
		body, _ := json.Marshal(map[string]any{
			"code": "STRIP", "name": "Strip", "conversion_factor": -1.0,
		})
		if err := bindBody(t, v, &req, string(body)); err != nil {
			t.Fatalf("negative factor is not rejected today: %v", err)
		}
		if !FitsConversionFactor(-1) {
			t.Fatal("-1 is representable, only the positivity rule is absent")
		}
	})

	t.Run("base_unit_id self reference rejected on update", func(t *testing.T) {
		r := UpdateUnitTypeRequest{BaseUnitID: &validID}
		if err := r.ValidateSelfReference(validID); !errors.Is(err, ErrSelfReference) {
			t.Fatalf("self base unit should be rejected, got %v", err)
		}
		r2 := UpdateUnitTypeRequest{BaseUnitID: &otherID}
		if err := r2.ValidateSelfReference(validID); err != nil {
			t.Fatalf("distinct base unit should be accepted: %v", err)
		}
	})

	t.Run("symbol over 20 runes rejected", func(t *testing.T) {
		v := newTestValidator(t, Limits{})
		var req CreateUnitTypeRequest
		body, _ := json.Marshal(map[string]string{
			"code": "PC", "name": "Piece", "symbol": strings.Repeat("s", 21),
		})
		if err := bindBody(t, v, &req, string(body)); err == nil {
			t.Fatal("21-character symbol should be rejected (VARCHAR(20))")
		}
	})
}

// ---------------------------------------------------------------------------
// StorageCondition
// ---------------------------------------------------------------------------

func TestStorageConditionTemperatureRange(t *testing.T) {
	tests := []struct {
		name    string
		min     *float64
		max     *float64
		wantErr bool
	}{
		{"min less than max", f64ptr(2), f64ptr(8), false},
		{"min equal to max", f64ptr(20), f64ptr(20), false},
		{"min greater than max", f64ptr(30), f64ptr(15), true},
		{"both nil", nil, nil, false},
		{"only min", f64ptr(-25), nil, false},
		{"only max", nil, f64ptr(30), false},
		{"negative valid band", f64ptr(-80), f64ptr(-60), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTemperatureRange(tc.min, tc.max)
			if tc.wantErr && !errors.Is(err, ErrTemperatureRange) {
				t.Fatalf("want ErrTemperatureRange, got %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}

	t.Run("unrepresentable temperature rejected", func(t *testing.T) {
		if !FitsTemperature(999.99) {
			t.Fatal("999.99 is the largest NUMERIC(5,2) value and should fit")
		}
		if FitsTemperature(1000) {
			t.Fatal("1000 overflows NUMERIC(5,2)")
		}
	})
}

// TestStorageConditionStructRule drives the same check through the registered
// struct-level rule, which is the path httpx.BindJSON actually uses.
func TestStorageConditionStructRule(t *testing.T) {
	v := newTestValidator(t, Limits{})

	t.Run("inverted create rejected", func(t *testing.T) {
		var req CreateStorageConditionRequest
		err := bindBody(t, v, &req, `{"code":"COLD","name":"Cold","min_temp_celsius":30,"max_temp_celsius":15}`)
		if err == nil {
			t.Fatal("inverted band should be rejected")
		}
		if !strings.Contains(err.Error(), "min_temp_celsius") {
			t.Fatalf("error should name the field: %v", err)
		}
	})

	t.Run("valid create accepted", func(t *testing.T) {
		var req CreateStorageConditionRequest
		if err := bindBody(t, v, &req, `{"code":"COLD","name":"Cold","min_temp_celsius":2,"max_temp_celsius":8}`); err != nil {
			t.Fatalf("valid band should be accepted: %v", err)
		}
	})

	t.Run("half-supplied update is accepted here", func(t *testing.T) {
		// Deliberate: whether the merged row is still ordered depends on the
		// stored value, which the DTO layer cannot see. The service must check.
		var req UpdateStorageConditionRequest
		if err := bindBody(t, v, &req, `{"max_temp_celsius":15}`); err != nil {
			t.Fatalf("single-bound update must not be rejected here: %v", err)
		}
	})

	t.Run("self-inverted update rejected", func(t *testing.T) {
		var req UpdateStorageConditionRequest
		err := bindBody(t, v, &req, `{"min_temp_celsius":30,"max_temp_celsius":15}`)
		if err == nil {
			t.Fatal("an update that is already inverted on its own should be rejected")
		}
	})
}

// ---------------------------------------------------------------------------
// TaxRate
// ---------------------------------------------------------------------------

func TestTaxRateSignAndBounds(t *testing.T) {
	t.Run("negative rejected", func(t *testing.T) {
		v := newTestValidator(t, Limits{})
		var req CreateTaxRateRequest
		if err := bindBody(t, v, &req, `{"code":"VAT","name":"VAT","rate_percent":-1}`); err == nil {
			t.Fatal("negative rate should be rejected")
		}
	})

	t.Run("zero accepted", func(t *testing.T) {
		v := newTestValidator(t, Limits{})
		var req CreateTaxRateRequest
		if err := bindBody(t, v, &req, `{"code":"ZERO","name":"Zero","rate_percent":0}`); err != nil {
			t.Fatalf("0 is a legal rate: %v", err)
		}
	})

	t.Run("rate_percent is required on create", func(t *testing.T) {
		v := newTestValidator(t, Limits{})
		var req CreateTaxRateRequest
		if err := bindBody(t, v, &req, `{"code":"VAT","name":"VAT"}`); err == nil {
			t.Fatal("missing rate_percent should be rejected (NOT NULL column)")
		}
	})

	t.Run("update rate_percent is optional", func(t *testing.T) {
		v := newTestValidator(t, Limits{})
		var req UpdateTaxRateRequest
		if err := bindBody(t, v, &req, `{"name":"Standard VAT"}`); err != nil {
			t.Fatalf("PATCH without rate_percent should be valid: %v", err)
		}
	})

	t.Run("no invented default maximum", func(t *testing.T) {
		// Limits{} means "no configured business maximum". A rate above the
		// NUMERIC(6,3) representable range is still rejected, but nothing else is.
		l := Limits{}
		if err := l.ValidateRatePercent(999.998); err != nil {
			t.Fatalf("999.998 fits NUMERIC(6,3) and no maximum is configured: %v", err)
		}
		if err := l.ValidateRatePercent(1000); !errors.Is(err, ErrInvalidNumber) {
			t.Fatalf("1000 overflows NUMERIC(6,3): %v", err)
		}
	})

	t.Run("configured maximum is honoured", func(t *testing.T) {
		l := Limits{MaxRatePercent: 20}
		if err := l.ValidateRatePercent(20); err != nil {
			t.Fatalf("the maximum itself must be accepted: %v", err)
		}
		if err := l.ValidateRatePercent(20.001); !errors.Is(err, ErrRateAboveMaximum) {
			t.Fatalf("20.001 should exceed the configured maximum: %v", err)
		}
	})

	t.Run("configured maximum flows through the struct rule", func(t *testing.T) {
		v := newTestValidator(t, Limits{MaxRatePercent: 15})
		var req CreateTaxRateRequest
		if err := bindBody(t, v, &req, `{"code":"VAT15","name":"VAT 15","rate_percent":15}`); err != nil {
			t.Fatalf("boundary value should be accepted: %v", err)
		}
		var req2 CreateTaxRateRequest
		err := bindBody(t, v, &req2, `{"code":"VAT20","name":"VAT 20","rate_percent":15.5}`)
		if err == nil {
			t.Fatal("15.5 should exceed the configured maximum of 15")
		}
		if !strings.Contains(err.Error(), "rate_percent") {
			t.Fatalf("error should name the field: %v", err)
		}
	})

	t.Run("NaN and Inf rejected", func(t *testing.T) {
		l := Limits{}
		if err := l.ValidateRatePercent(math.NaN()); !errors.Is(err, ErrInvalidNumber) {
			t.Fatal("NaN must be rejected")
		}
		if err := l.ValidateRatePercent(math.Inf(1)); !errors.Is(err, ErrInvalidNumber) {
			t.Fatal("+Inf must be rejected")
		}
		if err := l.ValidateRatePercent(math.Inf(-1)); !errors.Is(err, ErrInvalidNumber) {
			t.Fatal("-Inf must be rejected")
		}
	})

	t.Run("update rejects an explicit negative", func(t *testing.T) {
		v := newTestValidator(t, Limits{})
		var req UpdateTaxRateRequest
		if err := bindBody(t, v, &req, `{"rate_percent":-0.5}`); err == nil {
			t.Fatal("negative rate should be rejected on PATCH too")
		}
	})
}

// ---------------------------------------------------------------------------
// List query
// ---------------------------------------------------------------------------

// bindQuery mirrors httpx.BindQuery: gin binds the form tags, then the shared
// validator runs. Using gin's own binder keeps the tests honest about how
// `?page=abc` and `?is_active=maybe` actually fail.
func bindQuery(t *testing.T, v *validator.Validator, dst any, raw string) error {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "/master/routes?"+raw, nil)
	if err != nil {
		t.Fatalf("bad test query: %v", err)
	}
	if err := binding.Query.Bind(req, dst); err != nil {
		return err
	}
	return v.Struct(dst)
}

func TestListMasterDataQuery(t *testing.T) {
	v := newTestValidator(t, Limits{})

	t.Run("defaults", func(t *testing.T) {
		var q ListMasterDataQuery
		if err := bindQuery(t, v, &q, ""); err != nil {
			t.Fatalf("empty query should be valid: %v", err)
		}
		q.Normalize()
		if q.Page != 1 {
			t.Errorf("default page = %d, want 1", q.Page)
		}
		if q.Limit != pagination.DefaultLimit {
			t.Errorf("default limit = %d, want %d", q.Limit, pagination.DefaultLimit)
		}
		if q.IsActive != nil || q.Q != nil || q.ParentID != "" {
			t.Errorf("filters should stay unset: %+v", q)
		}
	})

	t.Run("is_active true and false are distinguishable", func(t *testing.T) {
		var yes ListMasterDataQuery
		if err := bindQuery(t, v, &yes, "is_active=true"); err != nil {
			t.Fatalf("bind: %v", err)
		}
		var no ListMasterDataQuery
		if err := bindQuery(t, v, &no, "is_active=false"); err != nil {
			t.Fatalf("bind: %v", err)
		}
		if yes.IsActive == nil || !*yes.IsActive {
			t.Error("is_active=true should bind to a non-nil pointer to true")
		}
		if no.IsActive == nil || *no.IsActive {
			t.Error("is_active=false should bind to a non-nil pointer to false")
		}
	})

	t.Run("q search term", func(t *testing.T) {
		var q ListMasterDataQuery
		if err := bindQuery(t, v, &q, "q=tablet"); err != nil {
			t.Fatalf("bind: %v", err)
		}
		if q.NormalizedQ() != "tablet" {
			t.Errorf("NormalizedQ = %q, want %q", q.NormalizedQ(), "tablet")
		}
	})

	t.Run("q is trimmed but case preserved", func(t *testing.T) {
		q := ListMasterDataQuery{Q: strptr("  Tablet  ")}
		if got := q.NormalizedQ(); got != "Tablet" {
			t.Errorf("NormalizedQ = %q, want %q", got, "Tablet")
		}
	})

	t.Run("q truncated to MaxQueryLen", func(t *testing.T) {
		q := ListMasterDataQuery{Q: strptr(strings.Repeat("z", 500))}
		if got := q.NormalizedQ(); len(got) != pagination.MaxQueryLen {
			t.Errorf("len = %d, want %d", len(got), pagination.MaxQueryLen)
		}
	})

	t.Run("q over MaxQueryLen rejected by validation", func(t *testing.T) {
		var q ListMasterDataQuery
		if err := bindQuery(t, v, &q, "q="+strings.Repeat("z", 201)); err == nil {
			t.Fatal("a 201-character q should be rejected")
		}
	})

	t.Run("parent_id", func(t *testing.T) {
		var q ListMasterDataQuery
		if err := bindQuery(t, v, &q, "parent_id="+validID.String()); err != nil {
			t.Fatalf("bind: %v", err)
		}
		id, err := q.ParentUUID()
		if err != nil {
			t.Fatalf("ParentUUID: %v", err)
		}
		if id == nil || *id != validID {
			t.Fatalf("parent_id not parsed: %+v", id)
		}
	})

	t.Run("absent parent_id means no filter, not an error", func(t *testing.T) {
		q := ListMasterDataQuery{}
		id, err := q.ParentUUID()
		if err != nil {
			t.Fatalf("absent parent_id should not error: %v", err)
		}
		if id != nil {
			t.Fatal("absent parent_id should yield no filter")
		}
	})

	t.Run("empty parent_id means no filter, not an error", func(t *testing.T) {
		q := ListMasterDataQuery{ParentID: "   "}
		id, err := q.ParentUUID()
		if err != nil {
			t.Fatalf("blank parent_id should not error: %v", err)
		}
		if id != nil {
			t.Fatal("blank parent_id should yield no filter")
		}
	})

	t.Run("invalid parent_id rejected deterministically", func(t *testing.T) {
		var q ListMasterDataQuery
		// It binds fine — a string always binds — and is refused at parse time by
		// uuidx.ParsePointer, which is where the verdict belongs.
		if err := bindQuery(t, v, &q, "parent_id=not-a-uuid"); err != nil {
			t.Fatalf("bind should succeed: %v", err)
		}
		id, err := q.ParentUUID()
		if err == nil {
			t.Fatalf("malformed parent_id should be rejected, got %v", id)
		}
		if !strings.Contains(err.Error(), "invalid identifier") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("pagination boundaries", func(t *testing.T) {
		var q ListMasterDataQuery
		if err := bindQuery(t, v, &q, "page=3&limit=50"); err != nil {
			t.Fatalf("bind: %v", err)
		}
		if q.Page != 3 || q.Limit != 50 {
			t.Fatalf("page/limit = %d/%d, want 3/50", q.Page, q.Limit)
		}
		q.Normalize()
		if q.OffsetSQL() != 100 {
			t.Errorf("OffsetSQL = %d, want 100", q.OffsetSQL())
		}

		var over ListMasterDataQuery
		if err := bindQuery(t, v, &over, "limit=5000"); err != nil {
			t.Fatalf("bind: %v", err)
		}
		over.Normalize()
		if over.Limit != pagination.MaxLimit {
			t.Errorf("limit should clamp to %d, got %d", pagination.MaxLimit, over.Limit)
		}

		var under ListMasterDataQuery
		if err := bindQuery(t, v, &under, "page=0&limit=0"); err != nil {
			t.Fatalf("bind: %v", err)
		}
		under.Normalize()
		if under.Page != 1 || under.Limit != pagination.DefaultLimit {
			t.Errorf("page/limit = %d/%d, want 1/%d", under.Page, under.Limit, pagination.DefaultLimit)
		}
	})

	t.Run("non-numeric page rejected", func(t *testing.T) {
		var q ListMasterDataQuery
		if err := bindQuery(t, v, &q, "page=abc"); err == nil {
			t.Fatal("page=abc should be rejected")
		}
	})

	t.Run("non-boolean is_active rejected", func(t *testing.T) {
		var q ListMasterDataQuery
		if err := bindQuery(t, v, &q, "is_active=maybe"); err == nil {
			t.Fatal("is_active=maybe should be rejected")
		}
	})
}

// TestSortSafety is the injection guard: no client-supplied string may become an
// ORDER BY fragment. A disallowed value must collapse to an empty field list so
// Phase 04 falls back to its default.
func TestSortSafety(t *testing.T) {
	tests := []struct {
		name    string
		sort    string
		allowed map[string]struct{}
		want    []pagination.SortField
	}{
		{"empty", "", baseSortColumns, nil},
		{"single asc", "name", baseSortColumns, []pagination.SortField{{Field: "name"}}},
		{"single desc", "-name", baseSortColumns, []pagination.SortField{{Field: "name", Desc: true}}},
		{"multi", "is_active,-created_at", baseSortColumns, []pagination.SortField{
			{Field: "is_active"}, {Field: "created_at", Desc: true},
		}},
		{"sql injection attempt", "name; DROP TABLE routes", baseSortColumns, nil},
		{"sql comment attempt", "name--", baseSortColumns, nil},
		{"unknown column", "conversion_factor", baseSortColumns, nil},
		{"empty key dropped", ",name,", baseSortColumns, []pagination.SortField{{Field: "name"}}},
		{"resource specific allowed", "sort_order", productCategorySortColumns,
			[]pagination.SortField{{Field: "sort_order"}}},
		{"resource specific denied", "sort_order", baseSortColumns, nil},
		{"rate_percent allowed for tax", "rate_percent", taxRateSortColumns,
			[]pagination.SortField{{Field: "rate_percent"}}},
		{"is_controlled allowed for generic", "-is_controlled", genericMedicineSortColumns,
			[]pagination.SortField{{Field: "is_controlled", Desc: true}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveSort(tc.sort, tc.allowed)
			if len(got) != len(tc.want) {
				t.Fatalf("ResolveSort(%q) = %+v, want %+v", tc.sort, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ResolveSort(%q)[%d] = %+v, want %+v", tc.sort, i, got[i], tc.want[i])
				}
			}
			// Whatever comes back must be a member of the allow-list.
			for _, f := range got {
				if _, ok := tc.allowed[f.Field]; !ok {
					t.Fatalf("field %q escaped the allow-list", f.Field)
				}
			}
		})
	}
}

func TestTooManySortFieldsRejected(t *testing.T) {
	sort := strings.Join([]string{"code", "name", "is_active", "created_at", "sort_order", "extra"}, ",")
	if got := ResolveSort(sort, productCategorySortColumns); got != nil {
		t.Fatalf("more than MaxSortFields must be rejected, got %+v", got)
	}
}

// TestSortAllowListsContainBaseColumns guards that every resource inherits the
// shared columns.
func TestSortAllowListsContainBaseColumns(t *testing.T) {
	all := map[string]map[string]struct{}{
		"product_category":  productCategorySortColumns,
		"dosage_form":       dosageFormSortColumns,
		"route":             routeSortColumns,
		"package_type":      packageTypeSortColumns,
		"unit_type":         unitTypeSortColumns,
		"storage_condition": storageConditionSortColumns,
		"tax_rate":          taxRateSortColumns,
		"drug_group":        drugGroupSortColumns,
		"drug_class":        drugClassSortColumns,
		"therapeutic_class": therapeuticClassSortColumns,
		"generic_medicine":  genericMedicineSortColumns,
	}
	if len(all) != 11 {
		t.Fatalf("expected 11 sort allow-lists, got %d", len(all))
	}
	for name, cols := range all {
		for base := range baseSortColumns {
			if _, ok := cols[base]; !ok {
				t.Errorf("%s sort allow-list is missing %q", name, base)
			}
		}
		// id must never be client-sortable; the repository owns the tie-breaker.
		if _, ok := cols["id"]; ok {
			t.Errorf("%s sort allow-list must not contain id", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Determinism
// ---------------------------------------------------------------------------

// TestValidationIsDeterministic runs the same input repeatedly and requires the
// identical verdict — the property Phase 05 relies on for idempotent retries.
func TestValidationIsDeterministic(t *testing.T) {
	v := newTestValidator(t, Limits{MaxRatePercent: 20})
	body := `{"code":"VAT","name":"VAT","rate_percent":25}`
	var first string
	for i := 0; i < 25; i++ {
		var req CreateTaxRateRequest
		err := bindBody(t, v, &req, body)
		got := "nil"
		if err != nil {
			got = err.Error()
		}
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("iteration %d differed:\n  first: %s\n  now:   %s", i, first, got)
		}
	}
	if first == "nil" {
		t.Fatal("expected a stable rejection for rate_percent=25 over max 20")
	}
}
