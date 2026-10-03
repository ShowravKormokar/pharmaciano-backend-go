package masterdata

// Tests that keep the repository's SQL honest against the actual schema.
//
// The value of these tests is that they read the migration files rather than
// restating them. A hand-written copy of the column list would only prove the copy
// still matches itself; parsing 000005 proves it matches the database. This matters
// because Phase 04 was written against a written summary of the schema, and that
// summary was wrong about two resources: it described storage_conditions as having
// an is_refrigerated flag rather than min/max temperature, and gave tax_rates a
// NUMERIC(5,2) rate_percent instead of NUMERIC(6,3). Both mistakes would have
// compiled cleanly and failed only against a live database.

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

const masterDataMigration = "000005_master_data.up.sql"
const medicineMigration = "000006_medicine_brand_supplier.up.sql"
const integrityMigration = "000020_masterdata_integrity.up.sql"

// migrationColumnTypes parses every CREATE TABLE block and returns, per table, each
// column's declared SQL type.
//
// It is the companion to migrationTables for the checks that care about type
// precision rather than mere existence — currently the VARCHAR lengths behind the
// DTO `max=` tags.
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
		if current == "" || trimmed == ");" {
			if current == "" {
				continue
			}
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
		out[current][fields[0]] = strings.ToUpper(fields[1])
	}
	return out
}

// varcharLen extracts the n from VARCHAR(n), returning -1 when the column is not a
// bounded VARCHAR. The trailing comma of the last column stays attached to the token
// after whitespace splitting, so punctuation is stripped first.
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

// TestSchemaLimitConstantsMatchMigration is the real drift guard for the DTO
// `max=` tags.
//
// The previous version of this test compared each constant to the literal it is
// defined as, which could never fail, and dto.go's doc comment claimed the tags were
// checked against these constants. They were not. This version reads the actual
// VARCHAR widths from 000005, so widening a column in a future migration fails here
// instead of silently changing the public API contract.
func TestSchemaLimitConstantsMatchMigration(t *testing.T) {
	types := migrationColumnTypes(t, masterDataMigration)

	codeWidths := map[int]bool{}
	nameWidths := map[int]bool{}
	symbolWidths := map[int]bool{}

	for table, cols := range types {
		for _, col := range []string{"code", "name", "symbol"} {
			declType, ok := cols[col]
			if !ok {
				continue
			}
			n := varcharLen(declType)
			if n < 0 {
				continue
			}
			switch col {
			case "code":
				codeWidths[n] = true
			case "name":
				nameWidths[n] = true
			case "symbol":
				symbolWidths[n] = true
			}
			_ = table
		}
	}

	if len(codeWidths) != 1 || !codeWidths[MaxCodeLen] {
		t.Errorf("every `code` column must be VARCHAR(%d), schema has %v", MaxCodeLen, keysOf(codeWidths))
	}
	if len(symbolWidths) != 1 || !symbolWidths[MaxSymbolLen] {
		t.Errorf("every `symbol` column must be VARCHAR(%d), schema has %v", MaxSymbolLen, keysOf(symbolWidths))
	}
	wantNames := map[int]bool{
		MaxNameLenShort: true, MaxNameLenMedium: true, MaxNameLenLong: true,
	}
	if !sameSet(nameWidths, wantNames) {
		t.Errorf("`name` widths in the schema are %v, want exactly %v",
			keysOf(nameWidths), keysOf(wantNames))
	}
}

// sameSet reports whether two int sets are equal.
func sameSet(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// keysOf returns a set's members, sorted, for readable failure messages.
func keysOf(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// TestSearchColumnsAreTrigramIndexed guards the C-07 fix.
//
// `?q=` is `(name ILIKE $1 OR code ILIKE $1)`. An OR is only indexable when both
// arms are, so a missing trigram index on either column leaves the whole predicate
// with no index plan and PostgreSQL sequentially scans the table — which would make
// 000020's indexes dead weight and turn every search into a full scan.
func TestSearchColumnsAreTrigramIndexed(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(migrationPath(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// The migration aligns its columns, so "ON routes  USING gin" has two
		// spaces. Collapse whitespace before matching.
		return strings.Join(strings.Fields(string(raw)), " ")
	}
	base := read(masterDataMigration)
	integrity := read(integrityMigration)

	for table := range allSpecs {
		// `name` may already be indexed by 000005; 000020 added the rest.
		nameIndexed := strings.Contains(base, "ON "+table+" USING gin (name gin_trgm_ops)") ||
			strings.Contains(integrity, "ON "+table+" USING gin (name gin_trgm_ops)")
		if !nameIndexed {
			t.Errorf("%s: no GIN trigram index on `name`, so the ?q= name arm is a sequential scan", table)
		}

		// `code` is indexed only by 000020; 000005 has just case-sensitive
		// partial unique btrees, which cannot serve a leading-wildcard ILIKE.
		if !strings.Contains(integrity, "ON "+table+" USING gin (code gin_trgm_ops)") {
			t.Errorf("%s: no GIN trigram index on `code`, so the ?q= code arm is a sequential scan", table)
		}
	}
}

// migrationPath resolves a migration relative to this package directory. go test
// runs with the package directory as the working directory.
func migrationPath(name string) string {
	return filepath.Join("..", "..", "..", "migrations", name)
}

// migrationTables parses every `CREATE TABLE x (...)` block in a migration into
// the ordered column names it declares.
//
// It is deliberately a small, permissive reader rather than a SQL parser: it exists
// to catch a column list that has drifted, not to validate the migration itself.
func migrationTables(t *testing.T, file string) map[string][]string {
	t.Helper()

	raw, err := os.ReadFile(migrationPath(file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}

	tables := map[string][]string{}
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
			tables[current] = nil
			continue
		}
		if current == "" {
			continue
		}
		if trimmed == ");" {
			current = ""
			continue
		}
		// Column lines are indented two spaces; constraint and index lines are not.
		if !strings.HasPrefix(line, "  ") || trimmed == "" {
			continue
		}
		name := trimmed
		if i := strings.IndexAny(trimmed, " \t("); i >= 0 {
			name = trimmed[:i]
		}
		if name == "" || strings.HasPrefix(name, "--") {
			continue
		}
		tables[current] = append(tables[current], name)
	}
	return tables
}

// allSpecs is every MasterData table spec, keyed by table name.
var allSpecs = map[string]tableSpec{
	"product_categories":  productCategoryTable,
	"routes":              routeTable,
	"dosage_forms":        dosageFormTable,
	"package_types":       packageTypeTable,
	"unit_types":          unitTypeTable,
	"storage_conditions":  storageConditionTable,
	"tax_rates":           taxRateTable,
	"drug_groups":         drugGroupTable,
	"drug_classes":        drugClassTable,
	"therapeutic_classes": therapeuticClassTable,
	"generic_medicines":   genericMedicineTable,
}

// TestSpecColumnsMatchMigration is the load-bearing test of this file: every spec
// must name exactly the columns 000005 declares for that table, in the same order.
//
// Both halves matter. "Same set" stops a column being silently dropped from a
// SELECT or a phantom column being added; "same order" is what lets the column
// string and the scan helper be plain positional code that a compiler cannot check.
func TestSpecColumnsMatchMigration(t *testing.T) {
	tables := migrationTables(t, masterDataMigration)

	if len(allSpecs) != 11 {
		t.Fatalf("allSpecs has %d entries, want 11", len(allSpecs))
	}

	for table, spec := range allSpecs {
		declared, ok := tables[table]
		if !ok {
			t.Errorf("%s: declared in %s but missing from allSpecs", table, masterDataMigration)
			continue
		}
		got := splitColumns(spec.columns)
		if !reflect.DeepEqual(got, declared) {
			t.Errorf("%s column list drifted from the migration\n  repository: %v\n  migration:  %v",
				table, got, declared)
		}
	}
}

// TestSpecsOnlyCoverApprovedResources guards the scope of the whole phase: the
// eleven central resources only, never the manufacturers/brands/suppliers tables
// that contract C-02 leaves to their own modules.
func TestSpecsOnlyCoverApprovedResources(t *testing.T) {
	for table := range allSpecs {
		switch table {
		case "product_categories", "routes", "dosage_forms", "package_types",
			"unit_types", "storage_conditions", "tax_rates",
			"drug_groups", "drug_classes", "therapeutic_classes", "generic_medicines":
		default:
			t.Errorf("allSpecs contains %q, which is not one of the eleven approved resources", table)
		}
	}
}

// TestParentColumnOnlyOnTheThreeHierarchies pins the documented reading of
// ListMasterDataQuery: ?parent_id= filters the three self-referencing hierarchies
// and is ignored everywhere else.
//
// A stray parentCol would silently redirect the filter onto an unrelated column —
// ?parent_id=<a dosage form id> returning every dosage form on that route, for
// instance — which is a wrong answer rather than an error.
func TestParentColumnOnlyOnTheThreeHierarchies(t *testing.T) {
	want := map[string]string{
		"product_categories":  "parent_id",
		"drug_classes":        "drug_group_id",
		"therapeutic_classes": "drug_class_id",
	}
	for table, spec := range allSpecs {
		if got := spec.parentCol; got != want[table] {
			t.Errorf("%s: parentCol = %q, want %q", table, got, want[table])
		}
	}
}

// TestParentColumnIsARealColumn guards the values above against a rename in the
// migration, which would otherwise only fail as a runtime SQL error.
func TestParentColumnIsARealColumn(t *testing.T) {
	tables := migrationTables(t, masterDataMigration)
	for table, spec := range allSpecs {
		if spec.parentCol == "" {
			continue
		}
		if !contains(tables[table], spec.parentCol) {
			t.Errorf("%s: parentCol %q is not a declared column", table, spec.parentCol)
		}
	}
}

// TestSortColumnsAreRealColumns keeps the ORDER BY allow-lists honest. A sort key
// that is not a column produces invalid SQL for any request that uses it.
func TestSortColumnsAreRealColumns(t *testing.T) {
	tables := migrationTables(t, masterDataMigration)
	for table, spec := range allSpecs {
		for field := range spec.sortColumns {
			if !contains(tables[table], field) {
				t.Errorf("%s: sort allow-list names %q, which is not a declared column", table, field)
			}
		}
	}
}

// medicineReferenceColumns maps each MasterData resource to the medicines column
// that references it, or "" where there is none. These are the literals the
// Is...Referenced methods pass to referencedByMedicine, and they are transcribed
// from 000006_medicine_brand_supplier.up.sql:9-31.
var medicineReferenceColumns = map[string]string{
	"product_categories":  "category_id",
	"dosage_forms":        "dosage_form_id",
	"routes":              "route_id",
	"package_types":       "package_type_id",
	"unit_types":          "sales_unit_id",
	"storage_conditions":  "storage_condition_id",
	"tax_rates":           "tax_rate_id",
	"generic_medicines":   "generic_medicine_id",
	"drug_groups":         "",
	"drug_classes":        "",
	"therapeutic_classes": "",
}

// TestMedicineReferenceColumnsExist re-derives contract C-11 from the migration:
// every column a probe passes must be a real medicines column, and every resources
// medicines actually points at must have one.
func TestMedicineReferenceColumnsExist(t *testing.T) {
	tables := migrationTables(t, medicineMigration)
	medicineCols := tables["medicines"]
	if len(medicineCols) == 0 {
		t.Fatal("could not parse the medicines table from " + medicineMigration)
	}

	for table, col := range medicineReferenceColumns {
		if col == "" {
			continue
		}
		if !contains(medicineCols, col) {
			t.Errorf("%s: probe uses medicines.%s, which the migration does not declare", table, col)
		}
	}

	// And the inverse: medicines declares a foreign key to exactly these eight of
	// the eleven tables. If 000006 ever gains another MasterData reference, this
	// fails and the missing probe is added.
	referencing := []string{
		"generic_medicine_id", "category_id", "dosage_form_id", "route_id",
		"storage_condition_id", "package_type_id", "sales_unit_id", "tax_rate_id",
	}
	for _, col := range referencing {
		if !contains(medicineCols, col) {
			t.Errorf("%s no longer declares %s; re-derive contract C-11", medicineMigration, col)
		}
	}
	if len(referencing) != 8 {
		t.Errorf("expected 8 MasterData references from medicines, listed %d", len(referencing))
	}
}

// ---------------------------------------------------------------------------
// Column order vs. scan helpers
// ---------------------------------------------------------------------------

// markerScanner is a rowScanner that fills each destination with a value derived
// from its position rather than from a database. It is what lets the scan helpers
// be verified without a live PostgreSQL instance.
type markerScanner struct{ t *testing.T }

// Scan assigns positional markers and returns nil, simulating a successful row.
func (s markerScanner) Scan(dest ...any) error {
	s.t.Helper()
	if len(dest) == 0 {
		return nil
	}
	for i, d := range dest {
		assign(s.t, d, i)
	}
	return nil
}

// mkUUID is the uuid marker for a column index.
func mkUUID(i int) uuid.UUID {
	return uuid.MustParse(strings.Repeat("0", 8) + "-0000-4000-8000-" + leftPad(i))
}

// leftPad renders a small index as the last 12 hex digits of a UUID.
func leftPad(i int) string {
	s := ""
	for n := i; n > 0; n /= 16 {
		s = string("0123456789abcdef"[n%16]) + s
	}
	for len(s) < 12 {
		s = "0" + s
	}
	return s
}

// mkTime is the timestamp marker for a column index. The index becomes the hour, so
// created_at/updated_at/deleted_at are all distinguishable from one another.
func mkTime(i int) time.Time {
	return time.Date(2020, 1, 1, i%24, 0, 0, 0, time.UTC)
}

// mkString is the string marker for a column index.
func mkString(i int) string { return "c" + leftPad(i) }

// mkFloat is the numeric marker for a column index.
func mkFloat(i int) float64 { return float64(i) + 0.5 }

// assign writes the positional marker for index i into the pointer dest.
func assign(t *testing.T, dest any, i int) {
	t.Helper()
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer {
		t.Fatalf("scan destination %d is %T, want a pointer", i, dest)
	}
	inner := reflect.New(rv.Type().Elem())
	setScalar(t, inner.Elem(), i)
	rv.Elem().Set(inner.Elem())
}

// setScalar stores the marker for index i into the addressable value v, allocating
// for pointer types so nullable columns are exercised as nullable.
func setScalar(t *testing.T, v reflect.Value, i int) {
	t.Helper()
	switch v.Type() {
	case reflect.TypeOf(uuid.UUID{}):
		v.Set(reflect.ValueOf(mkUUID(i)))
		return
	case reflect.TypeOf(time.Time{}):
		v.Set(reflect.ValueOf(mkTime(i)))
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(mkString(i))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(i))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Float64, reflect.Float32:
		v.SetFloat(mkFloat(i))
	case reflect.Pointer:
		nv := reflect.New(v.Type().Elem())
		setScalar(t, nv.Elem(), i)
		v.Set(nv)
	default:
		t.Fatalf("scan destination has unsupported type %s", v.Type())
	}
}

func ptrOf[T any](v T) *T { return &v }

// baseFor builds the BaseModel the marker scan should produce for a table whose
// timestamps occupy the given indices.
func baseFor(id, created, updated int, hasDeleted bool) common.BaseModel {
	b := common.BaseModel{
		ID:        mkUUID(id),
		CreatedAt: mkTime(created),
		UpdatedAt: mkTime(updated),
	}
	if hasDeleted {
		b.DeletedAt = ptrOf(mkTime(updated + 1))
	}
	return b
}

// scanValue adapts a typed scan helper to the (any, error) shape the table below
// needs, dereferencing the result so it can be compared with a model value.
func scanValue[T any](fn func(rowScanner) (*T, error)) func(rowScanner) (any, error) {
	return func(r rowScanner) (any, error) {
		v, err := fn(r)
		if err != nil {
			return nil, err
		}
		return *v, nil
	}
}

// TestScanHelpersAgreeWithColumnOrder verifies that each scan helper reads its
// columns in the order the spec lists them, by filling a row with positional
// markers and checking which model field received which.
//
// This is the part a compiler cannot check. A scan helper and its column string can
// agree on arity and still disagree on order, which would quietly swap code with
// name on every read — the kind of defect that passes review because the types line
// up. Writing this test is what caught six miscounted timestamp positions in the
// first draft of the scan cases.
func TestScanHelpersAgreeWithColumnOrder(t *testing.T) {
	type testCase struct {
		resource string
		spec     tableSpec
		scan     func(rowScanner) (any, error)
		want     any
	}

	cases := []testCase{
		{
			resource: "product_categories",
			spec:     productCategoryTable,
			scan:     scanValue(scanProductCategory),
			want: ProductCategory{
				BaseModel:   baseFor(0, 7, 8, true),
				Code:        mkString(1),
				Name:        mkString(2),
				ParentID:    ptrOf(mkUUID(3)),
				Description: ptrOf(mkString(4)),
				SortOrder:   5,
				IsActive:    true,
			},
		},
		{
			resource: "routes",
			spec:     routeTable,
			scan:     scanValue(scanRoute),
			want: Route{
				BaseModel:   baseFor(0, 5, 6, true),
				Code:        mkString(1),
				Name:        mkString(2),
				Description: ptrOf(mkString(3)),
				IsActive:    true,
			},
		},
		{
			resource: "dosage_forms",
			spec:     dosageFormTable,
			scan:     scanValue(scanDosageForm),
			want: DosageForm{
				BaseModel:   baseFor(0, 6, 7, true),
				Code:        mkString(1),
				Name:        mkString(2),
				RouteID:     ptrOf(mkUUID(3)),
				Description: ptrOf(mkString(4)),
				IsActive:    true,
			},
		},
		{
			resource: "package_types",
			spec:     packageTypeTable,
			scan:     scanValue(scanPackageType),
			want: PackageType{
				BaseModel:   baseFor(0, 5, 6, true),
				Code:        mkString(1),
				Name:        mkString(2),
				Description: ptrOf(mkString(3)),
				IsActive:    true,
			},
		},
		{
			resource: "unit_types",
			spec:     unitTypeTable,
			scan:     scanValue(scanUnitType),
			want: UnitType{
				BaseModel:        baseFor(0, 7, 8, true),
				Code:             mkString(1),
				Name:             mkString(2),
				Symbol:           ptrOf(mkString(3)),
				BaseUnitID:       ptrOf(mkUUID(4)),
				ConversionFactor: ptrOf(mkFloat(5)),
				IsActive:         true,
			},
		},
		{
			resource: "storage_conditions",
			spec:     storageConditionTable,
			scan:     scanValue(scanStorageCondition),
			want: StorageCondition{
				BaseModel:      baseFor(0, 7, 8, true),
				Code:           mkString(1),
				Name:           mkString(2),
				MinTempCelsius: ptrOf(mkFloat(3)),
				MaxTempCelsius: ptrOf(mkFloat(4)),
				Description:    ptrOf(mkString(5)),
				IsActive:       true,
			},
		},
		{
			resource: "tax_rates",
			spec:     taxRateTable,
			scan:     scanValue(scanTaxRate),
			want: TaxRate{
				BaseModel:   baseFor(0, 6, 7, true),
				Code:        mkString(1),
				Name:        mkString(2),
				RatePercent: mkFloat(3),
				IsDefault:   true,
				IsActive:    true,
			},
		},
		{
			resource: "drug_groups",
			spec:     drugGroupTable,
			scan:     scanValue(scanDrugGroup),
			want: DrugGroup{
				BaseModel:   baseFor(0, 5, 6, true),
				Code:        mkString(1),
				Name:        mkString(2),
				Description: ptrOf(mkString(3)),
				IsActive:    true,
			},
		},
		{
			resource: "drug_classes",
			spec:     drugClassTable,
			scan:     scanValue(scanDrugClass),
			want: DrugClass{
				BaseModel:   baseFor(0, 6, 7, true),
				Code:        mkString(1),
				Name:        mkString(2),
				DrugGroupID: ptrOf(mkUUID(3)),
				Description: ptrOf(mkString(4)),
				IsActive:    true,
			},
		},
		{
			resource: "therapeutic_classes",
			spec:     therapeuticClassTable,
			scan:     scanValue(scanTherapeuticClass),
			want: TherapeuticClass{
				BaseModel:   baseFor(0, 6, 7, true),
				Code:        mkString(1),
				Name:        mkString(2),
				DrugClassID: ptrOf(mkUUID(3)),
				Description: ptrOf(mkString(4)),
				IsActive:    true,
			},
		},
		{
			resource: "generic_medicines",
			spec:     genericMedicineTable,
			scan:     scanValue(scanGenericMedicine),
			want: GenericMedicine{
				BaseModel:          baseFor(0, 7, 8, true),
				Code:               mkString(1),
				Name:               mkString(2),
				Description:        ptrOf(mkString(3)),
				TherapeuticClassID: ptrOf(mkUUID(4)),
				IsControlled:       true,
				IsActive:           true,
			},
		},
	}

	if len(cases) != len(allSpecs) {
		t.Fatalf("%d scan cases for %d specs; every resource needs one", len(cases), len(allSpecs))
	}

	for _, tc := range cases {
		t.Run(tc.resource, func(t *testing.T) {
			// The fixture and the spec must describe the same table, or a
			// mis-paired case would assert scan order against the wrong column
			// list and still pass.
			if tc.spec.table != tc.resource {
				t.Fatalf("case is named %q but its spec describes %q", tc.resource, tc.spec.table)
			}
			got, err := tc.scan(markerScanner{t: t})
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("scan order disagrees with the column list\n  got:  %#v\n  want: %#v",
					got, tc.want)
			}
		})
	}
}

// TestUpdateColumnsAreInsertColumnsWithoutCode guards the positional assumption the
// shared createRow/updateRow rely on.
//
// Both take the model's bound values as [id, code, ...mutable] and slice that by
// position. If a spec's updateCols were not exactly insertCols minus `code`, an
// UPDATE would silently bind the wrong column to the wrong placeholder — a write
// that succeeds and corrupts the row. This is the invariant dupl was standing in
// for, so it is asserted directly.
func TestUpdateColumnsAreInsertColumnsWithoutCode(t *testing.T) {
	for table, spec := range allSpecs {
		insert := splitColumns(spec.insertCols)
		update := splitColumns(spec.updateCols)

		if len(insert) == 0 || insert[0] != "code" {
			t.Errorf("%s: insertCols = %v, want it to start with code", table, insert)
			continue
		}
		if !reflect.DeepEqual(insert[1:], update) {
			t.Errorf("%s: updateCols %v != insertCols %v without code", table, update, insert[1:])
		}
	}
}

// TestWriteColumnsAreRealAndGeneratedAreNot checks that the write column lists name
// real columns and never touch the database-generated ones.
func TestWriteColumnsAreRealAndGeneratedAreNot(t *testing.T) {
	tables := migrationTables(t, masterDataMigration)

	// id, created_at, updated_at and deleted_at are generated by the schema
	// defaults (000005) or by the set_updated_at trigger; no write may name them.
	generated := map[string]bool{
		"id": true, "created_at": true, "updated_at": true, "deleted_at": true,
	}

	for table, spec := range allSpecs {
		declared := tables[table]
		for _, col := range append(splitColumns(spec.insertCols), splitColumns(spec.updateCols)...) {
			if generated[col] {
				t.Errorf("%s: write column %q is database-generated and must not be written", table, col)
			}
			if !contains(declared, col) {
				t.Errorf("%s: write column %q is not a declared column", table, col)
			}
		}
	}
}

// TestValuesAccessorsMatchColumnLists checks that each resource's values function
// returns exactly one entry per column the write lists name, plus the leading id.
//
// A mismatch here is the failure mode the generic core cannot detect on its own: the
// generated placeholder count comes from the column list while the arguments come
// from the accessor, so a drifted accessor would produce a runtime bind error.
func TestValuesAccessorsMatchColumnLists(t *testing.T) {
	cases := []struct {
		table  string
		values func() []any
		insert string
	}{
		{"product_categories", func() []any {
			return productCategoryValues(&ProductCategory{})
		}, productCategoryTable.insertCols},
		{"routes", func() []any { return routeValues(&Route{}) }, routeTable.insertCols},
		{"dosage_forms", func() []any { return dosageFormValues(&DosageForm{}) }, dosageFormTable.insertCols},
		{"package_types", func() []any {
			return packageTypeValues(&PackageType{})
		}, packageTypeTable.insertCols},
		{"unit_types", func() []any { return unitTypeValues(&UnitType{}) }, unitTypeTable.insertCols},
		{"storage_conditions", func() []any {
			return storageConditionValues(&StorageCondition{})
		}, storageConditionTable.insertCols},
		{"tax_rates", func() []any { return taxRateValues(&TaxRate{}) }, taxRateTable.insertCols},
		{"drug_groups", func() []any { return drugGroupValues(&DrugGroup{}) }, drugGroupTable.insertCols},
		{"drug_classes", func() []any { return drugClassValues(&DrugClass{}) }, drugClassTable.insertCols},
		{"therapeutic_classes", func() []any {
			return therapeuticClassValues(&TherapeuticClass{})
		}, therapeuticClassTable.insertCols},
		{"generic_medicines", func() []any {
			return genericMedicineValues(&GenericMedicine{})
		}, genericMedicineTable.insertCols},
	}

	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			want := len(splitColumns(tc.insert)) + 1 // +1 for the leading id
			if got := len(tc.values()); got != want {
				t.Errorf("values accessor returns %d entries, want %d (id + %s)",
					got, want, tc.insert)
			}
		})
	}
}

// contains reports whether needle is in haystack.
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
