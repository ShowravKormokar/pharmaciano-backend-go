package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// genericMedicineColumns mirrors the declaration order of
// 000005_master_data.up.sql:208-219.
const genericMedicineColumns = `id, code, name, description, therapeutic_class_id, ` +
	`is_controlled, is_active, created_at, updated_at, deleted_at`

// genericMedicineTable describes generic_medicines.
//
// therapeutic_class_id is NOT the spec's parentCol. ListMasterDataQuery contracts
// ?parent_id= to the three self-referencing hierarchies, and generic_medicines is not
// one of them — the column is a link to another resource, not a place in a tree the
// client can walk. It is indexed (000005 ix_generic_medicines_tc) and offered as a
// filter only if the API contract later defines one.
//
// is_controlled is sortable because it is a boolean the client filters and orders by
// in practice; unlike tax_rates.is_default there is no unresolved "exactly one"
// invariant attached to it.
var genericMedicineTable = tableSpec{
	table:        "generic_medicines",
	columns:      genericMedicineColumns,
	insertCols:   `code, name, description, therapeutic_class_id, is_controlled, is_active`,
	updateCols:   `name, description, therapeutic_class_id, is_controlled, is_active`,
	sortColumns:  genericMedicineSortColumns,
	defaultOrder: defaultOrder,
}

// scanGenericMedicine maps one row into a GenericMedicine.
func scanGenericMedicine(row rowScanner) (*GenericMedicine, error) {
	var g GenericMedicine
	if err := row.Scan(
		&g.ID, &g.Code, &g.Name, &g.Description, &g.TherapeuticClassID, &g.IsControlled,
		&g.IsActive, &g.CreatedAt, &g.UpdatedAt, &g.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &g, nil
}

// genericMedicineValues binds g as [id, code, then every mutable column].
func genericMedicineValues(g *GenericMedicine) []any {
	return []any{g.ID, g.Code, g.Name, g.Description, g.TherapeuticClassID, g.IsControlled, g.IsActive}
}

// CreateGenericMedicine inserts a generic medicine and returns the stored row.
// 000005 already indexes .name with GIN trigram, so ?q= here is index-backed.
func (r *Repository) CreateGenericMedicine(ctx context.Context, g *GenericMedicine) (*GenericMedicine, error) {
	return createRow(ctx, r, genericMedicineTable, g, genericMedicineValues, scanGenericMedicine)
}

// GetGenericMedicine returns the live generic medicine with the given id, or
// db.ErrNoRows.
func (r *Repository) GetGenericMedicine(ctx context.Context, id uuid.UUID) (*GenericMedicine, error) {
	return getRow(ctx, r, genericMedicineTable, id, scanGenericMedicine, false)
}

// GetGenericMedicineForUpdate is GetGenericMedicine with a row lock.
func (r *Repository) GetGenericMedicineForUpdate(ctx context.Context, id uuid.UUID) (*GenericMedicine, error) {
	return getRow(ctx, r, genericMedicineTable, id, scanGenericMedicine, true)
}

// ListGenericMedicines returns a filtered, paginated page plus the total count.
func (r *Repository) ListGenericMedicines(ctx context.Context, f ListFilter, o pagination.Offset) ([]GenericMedicine, int64, error) {
	return listRows(ctx, r, genericMedicineTable, scanGenericMedicine, f, o)
}

// UpdateGenericMedicine writes the mutable columns of g and returns the refreshed
// row. code and id are never written.
func (r *Repository) UpdateGenericMedicine(ctx context.Context, g *GenericMedicine) (*GenericMedicine, error) {
	return updateRow(ctx, r, genericMedicineTable, g, genericMedicineValues, scanGenericMedicine)
}

// SoftDeleteGenericMedicine marks the generic medicine deleted and reports whether a
// row was affected.
func (r *Repository) SoftDeleteGenericMedicine(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, genericMedicineTable, id)
}

// IsGenericMedicineReferenced reports whether any live medicine still points at this
// generic medicine via medicines.generic_medicine_id (000006:9).
//
// This is the one reference in the taxonomy chain that matters operationally: a
// therapeutic class may be deleted while unused, but a generic medicine that a
// stocked medicine points at may not be soft-deleted without stranding it.
func (r *Repository) IsGenericMedicineReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByMedicine(ctx, r, "generic_medicine_id", id)
}
