package medicine

// Drift guard between the catalogue queries and the indexes they need.
//
// The list endpoints are keyset paginated: the page boundary is the
// (sort key, id) of the last row of the previous page. A keyset query only
// stays fast while an index matches its ORDER BY, so every sort field the
// API accepts needs an index whose leading expression is that sort column
// and whose second column is the id tie-breaker.
//
// Reading the migration rather than restating the index list is the point:
// a new sort field that nobody indexed fails here instead of turning every
// page of the catalogue into a sort of the whole tenant.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const catalogIndexMigration = "000025_medicine_catalog_indexes.up.sql"

var createIndexPattern = regexp.MustCompile(`(?i)CREATE\s+INDEX\s+(\w+)\s+ON\s+(\w+)\s*\(`)

// medicineIndex is one index declared by the migration.
type medicineIndex struct {
	name    string
	table   string
	columns []string
}

// catalogIndexes parses the index declarations of the migration.
//
// The column list is read with a parenthesis-aware scan, because an
// expression index such as (COALESCE(display_name, ”), id) contains a comma
// inside its parentheses.
func catalogIndexes(t *testing.T) []medicineIndex {
	t.Helper()

	raw, err := os.ReadFile(migrationPath(catalogIndexMigration))
	if err != nil {
		t.Fatalf("read %s: %v", catalogIndexMigration, err)
	}
	text := string(raw)

	var indexes []medicineIndex
	for _, match := range createIndexPattern.FindAllStringSubmatchIndex(text, -1) {
		name := text[match[2]:match[3]]
		table := text[match[4]:match[5]]

		start := match[1]
		depth := 1
		end := start
		for end < len(text) && depth > 0 {
			switch text[end] {
			case '(':
				depth++
			case ')':
				depth--
			}
			end++
		}
		if depth != 0 {
			t.Fatalf("unterminated index definition for %q", name)
		}

		indexes = append(indexes, medicineIndex{
			name:    name,
			table:   table,
			columns: splitIndexColumns(text[start : end-1]),
		})
	}
	if len(indexes) == 0 {
		t.Fatalf("no index found in %s", catalogIndexMigration)
	}
	return indexes
}

// splitIndexColumns splits a column list on the commas that are not inside
// parentheses.
func splitIndexColumns(list string) []string {
	var (
		columns []string
		current strings.Builder
		depth   int
	)
	for _, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				columns = append(columns, strings.TrimSpace(current.String()))
				current.Reset()
				continue
			}
		}
		current.WriteRune(r)
	}
	if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
		columns = append(columns, trimmed)
	}
	return columns
}

// indexColumn normalizes a column expression for comparison: the direction
// does not change which index serves the ORDER BY.
func indexColumn(column string) string {
	column = strings.TrimSpace(column)
	column = strings.TrimSuffix(strings.TrimSuffix(column, "DESC"), "ASC")
	return strings.TrimSpace(column)
}

// TestEverySortFieldHasAKeysetIndex checks that each sort field the API
// accepts can be served by an index.
//
// The expected leading expression is the one the repository sorts on, so the
// index and the ORDER BY cannot disagree.
func TestEverySortFieldHasAKeysetIndex(t *testing.T) {
	indexes := catalogIndexes(t)

	expected := map[string]string{
		"name":         "name",
		"display_name": "COALESCE(display_name, '')",
		"sku":          "sku",
		"created_at":   "created_at",
		"updated_at":   "updated_at",
	}

	for field, expression := range expected {
		if _, allowed := allowedMedicineSortFields[field]; !allowed {
			t.Fatalf("the test and the DTO allow-list disagree about %q", field)
		}

		found := false
		for _, index := range indexes {
			if index.table != "medicines" || len(index.columns) < 2 {
				continue
			}
			if indexColumn(index.columns[0]) != expression {
				continue
			}
			if indexColumn(index.columns[1]) != "id" {
				continue
			}
			found = true
			break
		}
		if !found {
			t.Errorf("sort field %q (%s) has no (expression, id) index in %s", field, expression, catalogIndexMigration)
		}
	}
}

// TestKeysetIndexesCoverTheSubResourceLists checks the variant and barcode
// lists, which are keyset paginated as well.
func TestKeysetIndexesCoverTheSubResourceLists(t *testing.T) {
	indexes := catalogIndexes(t)

	cases := []struct {
		table string
		lead  string
	}{
		{table: "medicine_variants", lead: "medicine_id"},
		{table: "medicine_barcodes", lead: "medicine_id"},
	}

	for _, tc := range cases {
		found := false
		for _, index := range indexes {
			if index.table != tc.table || len(index.columns) < 3 {
				continue
			}
			if strings.EqualFold(index.columns[0], tc.lead) && strings.EqualFold(index.columns[2], "id") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s has no (%s, ..., id) index in %s", tc.table, tc.lead, catalogIndexMigration)
		}
	}
}

// TestVariantReferenceChecksAreIndexed checks the indexes the reference
// checks need.
//
// The Medicine module asks whether a variant is still referenced before it
// allows a package change. Without an index on the referencing column, that
// question is a sequential scan of three operational tables.
func TestVariantReferenceChecksAreIndexed(t *testing.T) {
	indexes := catalogIndexes(t)

	for _, table := range []string{"inventory_batches", "purchase_items", "sale_items"} {
		found := false
		for _, index := range indexes {
			if index.table != table || len(index.columns) == 0 {
				continue
			}
			if strings.EqualFold(index.columns[0], "variant_id") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s has no variant_id index in %s", table, catalogIndexMigration)
		}
	}
}
