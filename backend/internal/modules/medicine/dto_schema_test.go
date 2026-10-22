package medicine

// Drift guard between the Medicine DTOs and the schema they describe.
//
// The limits in dto.go are transcribed from
// migrations/000006_medicine_brand_supplier.up.sql. Restating them here would
// only prove that the copy still matches itself, so these tests read the
// migration instead: widening or narrowing a column in a future migration
// fails here rather than silently changing the public API contract (or, worse,
// letting a request through that the database then truncates).

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const medicineMigration = "000006_medicine_brand_supplier.up.sql"

func migrationPath(name string) string {
	return filepath.Join("..", "..", "..", "migrations", name)
}

// migrationColumnTypes parses every CREATE TABLE block and returns, per table,
// each column's declared SQL type.
//
// It is a small, permissive reader rather than a SQL parser: it exists to
// catch a column list that has drifted, not to validate the migration.
func migrationColumnTypes(t *testing.T, file string) map[string]map[string]string {
	t.Helper()

	raw, err := os.ReadFile(migrationPath(file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}

	out := map[string]map[string]string{}
	var current string
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "CREATE TABLE ") {
			rest := strings.TrimPrefix(trimmed, "CREATE TABLE ")
			name := rest
			if i := strings.IndexAny(rest, " ("); i >= 0 {
				name = rest[:i]
			}
			current = strings.TrimSpace(name)
			out[current] = map[string]string{}
			continue
		}
		if current == "" {
			continue
		}
		if trimmed == ");" {
			current = ""
			continue
		}
		if !strings.HasPrefix(line, "  ") || trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		// Skip constraint continuations (a CHECK clause on its own line) and
		// anything else that is not a column definition.
		if !isColumnDefinition(fields[0], fields[1]) {
			continue
		}
		out[current][fields[0]] = strings.ToUpper(fields[1])
	}
	return out
}

// sqlTypePrefixes are the column types this reader recognises.
var sqlTypePrefixes = []string{
	"VARCHAR", "TEXT", "NUMERIC", "DECIMAL", "INTEGER", "INT", "BIGINT",
	"SMALLINT", "BOOLEAN", "BOOL", "UUID", "TIMESTAMPTZ", "TIMESTAMP", "DATE",
	"TIME", "DOUBLE", "REAL", "JSON", "JSONB", "CHAR", "SERIAL", "BIGSERIAL",
}

// isColumnDefinition reports whether the two leading tokens of a line look
// like a column name and its SQL type.
func isColumnDefinition(name, declType string) bool {
	if name == "" || !isIdentifier(name) {
		return false
	}
	for _, prefix := range sqlTypePrefixes {
		if strings.HasPrefix(declType, prefix) {
			return true
		}
	}
	return false
}

// isIdentifier reports whether s is a plain SQL identifier.
func isIdentifier(s string) bool {
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return s != ""
}

// varcharLen extracts the n from VARCHAR(n), returning -1 when the column is
// not a bounded VARCHAR.
func varcharLen(declType string) int {
	declType = strings.TrimSuffix(strings.TrimSpace(declType), ",")
	if !strings.HasPrefix(declType, "VARCHAR(") || !strings.HasSuffix(declType, ")") {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(declType, "VARCHAR("), ")"))
	if err != nil {
		return -1
	}
	return n
}

// dtoFieldRules returns the validate rules declared on the DTO field with the
// given json name, and whether such a field exists at all.
func dtoFieldRules(t *testing.T, value any, jsonName string) (rules string, exists bool) {
	t.Helper()

	rt := reflect.TypeOf(value)
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != jsonName {
			continue
		}
		return field.Tag.Get("validate"), true
	}
	return "", false
}

// maxRuleValue returns the n of a "max=n" rule, and whether one is present.
func maxRuleValue(rules string) (int, bool) {
	for _, rule := range strings.Split(rules, ",") {
		rule = strings.TrimSpace(rule)
		if !strings.HasPrefix(rule, "max=") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(rule, "max="))
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// TestSchemaWidthsMatchDTOMaxTags checks every bounded VARCHAR that the
// Medicine write surface exposes against the width the schema declares.
func TestSchemaWidthsMatchDTOMaxTags(t *testing.T) {
	types := migrationColumnTypes(t, medicineMigration)

	cases := []struct {
		table string
		dto   any
	}{
		{"medicines", CreateMedicineRequest{}},
		{"medicine_variants", CreateMedicineVariantRequest{}},
		{"medicine_barcodes", CreateMedicineBarcodeRequest{}},
	}

	for _, tc := range cases {
		columns, ok := types[tc.table]
		if !ok {
			t.Fatalf("table %s not found in %s", tc.table, medicineMigration)
		}

		for column, declType := range columns {
			width := varcharLen(declType)
			if width < 0 {
				continue // TEXT or a non-string column
			}

			rules, exists := dtoFieldRules(t, tc.dto, column)
			if !exists {
				continue // not part of the write surface
			}

			max, hasMax := maxRuleValue(rules)
			if hasMax {
				if max != width {
					t.Errorf("%s.%s is VARCHAR(%d) but the DTO allows max=%d", tc.table, column, width, max)
				}
				continue
			}

			// A bounded column may also be constrained by an enumeration
			// instead of a length, but it must be bounded somehow.
			if !strings.Contains(rules, "oneof=") {
				t.Errorf("%s.%s is VARCHAR(%d) but the DTO rule %q sets no bound", tc.table, column, width, rules)
			}
		}
	}
}

// TestSchemaLimitConstantsMatchMigration ties the limit constants in dto.go to
// the migration, so they cannot drift into documentation that lies.
func TestSchemaLimitConstantsMatchMigration(t *testing.T) {
	types := migrationColumnTypes(t, medicineMigration)

	medicines, ok := types["medicines"]
	if !ok {
		t.Fatalf("table medicines not found in %s", medicineMigration)
	}
	variants, ok := types["medicine_variants"]
	if !ok {
		t.Fatalf("table medicine_variants not found in %s", medicineMigration)
	}

	medicineLimits := map[string]int{
		"sku":               maxSKULen,
		"barcode":           maxBarcodeLen,
		"name":              maxNameLen,
		"display_name":      maxDisplayNameLen,
		"strength":          maxStrengthLen,
		"pack_size":         maxPackSizeLen,
		"importer_name":     maxImporterNameLen,
		"import_license_no": maxImportLicenseNoLen,
		"country_of_origin": maxCountryOfOriginLen,
		"hsn_code":          maxHSNCodeLen,
		"image_url":         maxImageURLLen,
	}

	for column, constant := range medicineLimits {
		width := varcharLen(medicines[column])
		if width != constant {
			t.Errorf("medicines.%s is VARCHAR(%d) but the limit constant is %d", column, width, constant)
		}
	}

	if width := varcharLen(variants["name"]); width != maxVariantNameLen {
		t.Errorf("medicine_variants.name is VARCHAR(%d) but the limit constant is %d", width, maxVariantNameLen)
	}
}

// TestMedicineDTOCoversEveryTableColumn checks that the DTO write surface has
// not silently dropped a column that the table declares: every column of
// medicines, medicine_variants and medicine_barcodes must appear in the
// matching DTO, either as a writable field or as a column the contract
// explicitly reassigns to another module.
func TestMedicineDTOCoversEveryTableColumn(t *testing.T) {
	types := migrationColumnTypes(t, medicineMigration)

	// Columns the contract assigns to another module, plus the columns that
	// carry identity or lifecycle and are therefore never client-writable.
	reassigned := map[string]bool{
		"id":                  true,
		"created_at":          true,
		"updated_at":          true,
		"deleted_at":          true,
		"mrp":                 true,
		"default_cost":        true,
		"min_stock_threshold": true,
		"max_stock_threshold": true,
		"medicine_id":         true,
		"variant_id":          true,
	}

	cases := []struct {
		table string
		dto   any
	}{
		{"medicines", CreateMedicineRequest{}},
		{"medicine_variants", CreateMedicineVariantRequest{}},
		{"medicine_barcodes", CreateMedicineBarcodeRequest{}},
	}

	for _, tc := range cases {
		columns, ok := types[tc.table]
		if !ok {
			t.Fatalf("table %s not found in %s", tc.table, medicineMigration)
		}
		for column := range columns {
			if reassigned[column] {
				continue
			}
			if _, exists := dtoFieldRules(t, tc.dto, column); !exists {
				t.Errorf("%s.%s has no field in %T", tc.table, column, tc.dto)
			}
		}
	}
}
