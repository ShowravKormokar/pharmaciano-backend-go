// Persistence layer for the eleven central MasterData resources.
//
// Conventions are inherited from the completed modules (warehouse is the
// reference):
//
//   - A concrete *Repository holding *db.DB, not an interface. No module in this
//     repository declares a repository interface, so neither does this one.
//   - Every method takes ctx first and uses db.FromCtx for writes /
//     db.ReadFromCtx for reads. Both return the caller's transaction when one is
//     attached, so every method is transaction-participating automatically and
//     this package never begins or commits a transaction itself.
//   - Explicit column lists, never SELECT *.
//   - Raw driver errors are returned unwrapped so the service can map them with
//     db.ErrNoRows / db.IsUniqueVilation / db.IsCheckViolation.
//
// This file holds only the mechanics that are genuinely identical across the
// eleven tables. Everything resource-specific — the column list, the scan
// function, the INSERT/UPDATE column set, the parent column — lives in that
// resource's own repository_<resource>.go file. Keeping the SQL for each table
// visible in one place is worth more here than the repetition saved by a generic
// type, which would still need eleven different shapes.
//
// SCOPE: MasterData is central reference data, not tenant-owned (contract §3).
// Every other repository scopes its queries by organization_id and sometimes
// branch_id; this one deliberately scopes by nothing but deleted_at. None of the
// eleven tables has a tenant column. The tenant middleware and RBAC layer still
// run upstream — they are untouched — this package simply has nothing to filter
// on.

package masterdata

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Repository is the MasterData data-access layer. Methods return raw driver
// errors for the service to translate.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// rowScanner is the Scan subset shared by pgx.Row (single-row reads) and
// pgx.Rows (page reads), so one scan helper serves both the Get and the List path.
// warehouse splits this by scanning its list inline against a different column
// set; here every list reads the full row, so sharing the helper is both possible
// and worth it.
type rowScanner interface {
	Scan(dest ...any) error
}

// tableSpec is the shared description of one MasterData table. It is a plain
// struct of compile-time constants, NOT a generic repository: the column list and
// the scan function stay in the resource's own file, so a column order mistake
// still shows up as a plainly wrong scan rather than being hidden by a type
// parameter.
type tableSpec struct {
	// table is the physical table name. Always a literal in this package.
	table string

	// columns is the canonical column order used by every SELECT and by every
	// INSERT/UPDATE ... RETURNING. The matching scan<Resource> helper scans in
	// exactly this order.
	columns string

	// parentCol is the column ListFilter.ParentID filters on, or "" when the
	// resource exposes no parent filter. See ListFilter.ParentID for why only
	// three tables set it.
	parentCol string

	// sortColumns is the allow-list ?sort= may name. Reused from Phase 03
	// (validation.go) so the DTO layer and the repository can never disagree
	// about which sort fields exist.
	sortColumns map[string]struct{}

	// defaultOrder is the fallback ORDER BY.
	defaultOrder string

	// insertCols are the non-generated columns written by Create, in bind order.
	// The first entry is always `code`.
	insertCols string

	// updateCols are the mutable columns written by Update, in bind order. It is
	// exactly insertCols without `code`, because code is immutable by contract.
	// updateRow relies on that positional relationship; TestUpdateColumnsAreInsert
	// ColumnsWithoutCode asserts it for all eleven specs.
	updateCols string
}

// scanner maps one row onto a concrete model. Declared per resource so the shared
// core stays generic without reflection.
type scanner[T any] func(rowScanner) (*T, error)

// valuesOf returns the model's bound values in this order: the id, then `code`,
// then every mutable column — i.e. insertCols preceded by the id. The shared
// createRow/updateRow derive their argument slices from it by position.
type valuesOf[T any] func(*T) []any

// ListFilter carries the optional filters for a list query, decoupled from the
// HTTP DTO (warehouse.ListFilter is the precedent). One type serves all eleven
// resources; the notes below say which parts are meaningful to which.
//
//	IsActive every resource.
//
//	Q every resource — a case-insensitive substring match over code and name,
//	  per the ?q= contract in docs/api/API.md §2.6. Bound as a parameter with
//	  LIKE metacharacters escaped (see likePattern).
//
//	ParentID and ParentIsNull the three self-referencing hierarchies only —
//	  product_categories (parent_id), drug_classes (drug_group_id) and
//	  therapeutic_classes (drug_class_id). ListMasterDataQuery documents that
//	  the other eight resources accept ?parent_id= but ignore it, so those specs
//	  leave parentCol empty and the filter is dropped rather than silently
//	  retargeted onto an unrelated column.
//
//	  dosage_forms.route_id, unit_types.base_unit_id and
//	  generic_medicines.therapeutic_class_id are indexed (000005, 000020) but
//	  deliberately NOT filterable: no API parameter exists for them, and
//	  inventing query syntax the contract does not define is not this phase's
//	  call. When one is defined, add the column to the spec and to
//	  ListMasterDataQuery in the same change.
//
//	ParentIsNull additionally has NO HTTP parameter at all, so no request can
//	  currently set it and the branch it guards in buildWhere is unreachable
//	  outside tests. That is a deliberate, recorded gap rather than an oversight:
//	  §18 of the contract forbids inventing query syntax the API contract does not
//	  define, and there is no handler layer yet to bind one. Phase 05/07 must
//	  either expose a parameter for it or delete the field — see contract §28.6.
//	  Until then treat it as an untested capability, not a working filter.
type ListFilter struct {
	IsActive     *bool
	Q            string
	ParentID     *uuid.UUID
	ParentIsNull *bool
}

// defaultOrder is the fallback ORDER BY for every MasterData list.
//
// warehouse defaults to "code ASC, id ASC" (warehouse/repository.go:217) and no
// document in the repository specifies a MasterData-specific default, so all
// eleven follow warehouse rather than each inventing an ordering.
// product_categories.sort_order is therefore sortable but is NOT the default;
// seed/categories.yaml uses 999 as a "Miscellaneous" sentinel, so ordering by it
// first would put miscellaneous items ahead of real ones.
//
// The trailing id is what makes pagination stable: when several rows tie on the
// sort key, a key without a unique tie-break can repeat or skip rows across pages.
const defaultOrder = "code ASC, id ASC"

// likeEscape is the escape character for ?q= patterns. PostgreSQL's LIKE defaults
// to backslash too, but stating it makes the intent explicit and keeps the
// behaviour stable if standard_conforming_strings changes.
const likeEscape = `\`

// likePattern wraps a search term in wildcards, escaping the LIKE
// metacharacters so a client searching for "50%" or "a_b" matches that literal
// text instead of injecting a wildcard. Without this a one-character term
// becomes "%<char>%" which matches a large fraction of the catalogue and forces
// the query past the trigram index into a sequential scan.
func likePattern(term string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return "%" + replacer.Replace(term) + "%"
}

// normalizeQ re-applies the ?q= bound at the persistence boundary.
//
// ListMasterDataQuery.NormalizedQ already trims and truncates to
// pagination.MaxQueryLen, so a well-behaved caller sends at most that. The
// repository repeats the clamp rather than trusting its caller, for the same
// reason pageBounds repeats the limit clamp: this package must stay safe when
// reached from a path that skips the DTO layer.
func normalizeQ(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	if len(q) > pagination.MaxQueryLen {
		q = q[:pagination.MaxQueryLen]
	}
	return q
}

// buildWhere assembles the WHERE clause and its bind arguments for a list query.
// Every value is bound as a parameter; the only identifiers interpolated into the
// SQL come from spec, and those are literals declared in this package.
func buildWhere(spec tableSpec, f ListFilter) (string, []any) {
	where := []string{"deleted_at IS NULL"}
	args := []any{}

	// The base predicate is argument-free, so appending one value per filter keeps
	// the numbering below trivial.
	if f.IsActive != nil {
		args = append(args, *f.IsActive)
		where = append(where, "is_active = $"+strconv.Itoa(len(args)))
	}
	if term := normalizeQ(f.Q); term != "" {
		args = append(args, likePattern(term))
		where = append(where,
			"(name ILIKE $"+strconv.Itoa(len(args))+
				" ESCAPE '"+likeEscape+"' OR code ILIKE $"+strconv.Itoa(len(args))+
				" ESCAPE '"+likeEscape+"')")
	}
	if spec.parentCol != "" {
		// ParentIsNull wins over ParentID when both are set: it is the more
		// specific statement, and a root filter combined with a parent filter
		// matches nothing, which would be a confusing way to answer a question.
		switch {
		case f.ParentIsNull != nil && *f.ParentIsNull:
			where = append(where, spec.parentCol+" IS NULL")
		case f.ParentID != nil:
			args = append(args, *f.ParentID)
			where = append(where, spec.parentCol+" = $"+strconv.Itoa(len(args)))
		}
	}
	return strings.Join(where, " AND "), args
}

// buildOrderBy turns a validated sort string into an ORDER BY clause.
//
// Sorting is safe by construction: ResolveSort (Phase 03) parses the raw string
// against spec.sortColumns and drops anything not on that allow-list, so the only
// identifiers that can reach the clause are keys of that map — every one of them a
// literal declared in this package. A malformed or disallowed value yields the
// resource default rather than an error, matching warehouse.buildOrderBy.
func buildOrderBy(spec tableSpec, sort string) string {
	fields := ResolveSort(sort, spec.sortColumns)
	if len(fields) == 0 {
		return "ORDER BY " + spec.defaultOrder
	}
	parts := make([]string, 0, len(fields)+1)
	for _, f := range fields {
		dir := "ASC"
		if f.Desc {
			dir = "DESC"
		}
		parts = append(parts, f.Field+" "+dir)
	}
	parts = append(parts, "id ASC")
	return "ORDER BY " + strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// SQL fragment builders
// ---------------------------------------------------------------------------

// splitColumns turns a comma-separated column list into its parts.
func splitColumns(cols string) []string {
	parts := strings.Split(cols, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if c := strings.TrimSpace(p); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// placeholders renders "$1,$2,...,$n" for n bound values.
func placeholders(n int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = "$" + strconv.Itoa(i+1)
	}
	return strings.Join(parts, ",")
}

// setClause renders an UPDATE assignment list, numbering the parameters from
// startParam so a caller can pin earlier parameters (Update pins the id at $1).
func setClause(cols string, startParam int) string {
	parts := splitColumns(cols)
	out := make([]string, len(parts))
	for i, c := range parts {
		out[i] = c + " = $" + strconv.Itoa(startParam+i)
	}
	return strings.Join(out, ", ")
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

// pageBounds clamps the caller's offset into a safe range and returns the values
// for LIMIT/OFFSET.
//
// Phase 03 already normalises the query DTO, but the repository must not trust
// its caller: LIMIT -1 is invalid SQL and an unbounded Limit is handed to make()
// as a capacity, so both are clamped here rather than at the edge.
//
// The offset is computed from the CLAMPED limit rather than delegated to
// pagination.Offset.OffsetSQL(), which multiplies by the caller's raw limit.
// Delegating would desynchronise the pair: Offset{Page: 10, Limit: 100000} would
// become LIMIT 100 OFFSET 900000 — page 9000 of the result set instead of page 10.
func pageBounds(o pagination.Offset) (limit, offset int) {
	limit = o.Limit
	if limit < pagination.MinLimit {
		limit = pagination.DefaultLimit
	}
	if limit > pagination.MaxLimit {
		limit = pagination.MaxLimit
	}

	return limit, offsetFor(o, limit)
}

// ---------------------------------------------------------------------------
// Shared read/write helpers
// ---------------------------------------------------------------------------

// getRow returns the single live row with the given id. forUpdate adds the row
// lock that a PATCH/DELETE read-modify-write needs.
//
// A plain read goes to db.ReadFromCtx so it can be served by a read replica, and
// only a locking read is forced onto the primary — a lock may never be taken on a
// replica. That split is what D-11 freezes and what warehouse does
// (FindByID vs FindByIDForUpdate); keeping it matters as soon as a replica is
// configured, because a primary-only read pool silently stops offloading.
//
// QueryRow does not touch the connection until Scan, so a miss surfaces as
// db.ErrNoRows from the caller's Scan rather than from here.
func getRow[T any](ctx context.Context, r *Repository, spec tableSpec, id uuid.UUID, scan scanner[T], forUpdate bool) (*T, error) {
	q := `SELECT ` + spec.columns + `
		FROM ` + spec.table + `
		WHERE id = $1 AND deleted_at IS NULL`

	querier := r.db.ReadFromCtx(ctx)
	if forUpdate {
		q += ` FOR UPDATE`
		querier = r.db.FromCtx(ctx)
	}
	return scan(querier.QueryRow(ctx, q, id))
}

// createRow inserts v and returns the stored row.
//
// The id, created_at, updated_at and deleted_at columns are left to their schema
// defaults. Every other column is written from the model, INCLUDING is_active —
// see §28.5 of the contract: the service must resolve the Create DTO's *bool
// before calling, or an unset value stores false rather than the column default.
func createRow[T any](ctx context.Context, r *Repository, spec tableSpec, v *T, vals valuesOf[T], scan scanner[T]) (*T, error) {
	all := vals(v)
	// all[0] is the id, which the database generates; all[1:] are insertCols.
	args := all[1:]
	cols := splitColumns(spec.insertCols)

	q := `INSERT INTO ` + spec.table + ` (` + spec.insertCols + `)
		VALUES (` + placeholders(len(cols)) + `)
		RETURNING ` + spec.columns
	return scan(r.db.FromCtx(ctx).QueryRow(ctx, q, args...))
}

// updateRow writes the mutable columns of v and returns the refreshed row.
//
// code and id are never written — code is immutable by contract, and the WHERE
// clause pins the row. Everything else is written in full because the service has
// already merged the PATCH into the full row it read under FOR UPDATE; a partial
// UPDATE here would need COALESCE on every column and would silently write NULLs.
//
// Returns db.ErrNoRows when the row is missing or deleted. A foreign key pointing
// at a missing parent surfaces as a foreign-key violation.
func updateRow[T any](ctx context.Context, r *Repository, spec tableSpec, v *T, vals valuesOf[T], scan scanner[T]) (*T, error) {
	all := vals(v)
	// all[0] is the id ($1); all[1] is code, which is skipped; all[2:] are
	// updateCols, bound from $2 upward.
	args := make([]any, 0, len(all)-1)
	args = append(args, all[0])
	args = append(args, all[2:]...)

	q := `UPDATE ` + spec.table + ` SET ` + setClause(spec.updateCols, 2) + `
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING ` + spec.columns
	return scan(r.db.FromCtx(ctx).QueryRow(ctx, q, args...))
}

// softDelete marks a row deleted and reports whether one was affected.
//
// Every unique index on these tables is partial on `WHERE deleted_at IS NULL`
// (000005), so the code becomes reusable immediately after a soft delete — that is
// why this is an UPDATE and not a DELETE. A false return lets the service answer
// 404 for a second DELETE rather than reporting a silent success.
func softDelete(ctx context.Context, r *Repository, spec tableSpec, id uuid.UUID) (bool, error) {
	q := `UPDATE ` + spec.table + ` SET deleted_at = now()
		WHERE id = $1 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// referencedByMedicine reports whether any live medicines row points at id
// through column.
//
// This is the D-19 referenced-row probe. It is needed precisely because these
// deletes are SOFT: 000006 declares medicines.category_id ON DELETE RESTRICT,
// but a foreign key only fires for a hard DELETE. Setting deleted_at on a
// category that medicines still reference would silently orphan those rows, so
// the service must ask this first and answer 409 pointing the caller at
// PATCH {is_active:false} (contract C-11).
//
// column is always a literal passed by a typed Is<Resource>Referenced method —
// no caller-supplied text reaches this SQL. See the C-11 table in the contract for
// which of the eleven tables medicines actually references.
func referencedByMedicine(ctx context.Context, r *Repository, column string, id uuid.UUID) (bool, error) {
	q := `SELECT EXISTS (
			SELECT 1 FROM medicines
			WHERE ` + column + ` = $1 AND deleted_at IS NULL
		)`
	var ok bool
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// referencedByChild reports whether any live row of table points at id through
// column.
//
// This is the hierarchy half of the D-19 guard. The eight Is<Resource>Referenced
// probes answer "is a live medicine using this?", which cannot exist for the five
// resources medicines never points at: the two self-referencing ones
// (product_categories.parent_id, unit_types.base_unit_id) and the three taxonomy
// ones (drug_classes.drug_group_id, therapeutic_classes.drug_class_id,
// generic_medicines.therapeutic_class_id).
//
// Their inbound foreign keys are all ON DELETE SET NULL, and a soft delete does
// not fire a foreign key at all, so without this probe the service could orphan a
// whole subtree: removing one drug group would silently detach every drug class
// beneath it, and the caller's tree would lose rows it never asked to lose.
// Contract §20 note 2 requires exactly this check, walking the taxonomy chain
// down to generic_medicines — which does have a medicines probe of its own.
//
// table and column are always literals passed by a typed Has<Resource>Children
// method, never by a caller: no client-supplied text reaches this SQL, exactly as
// in referencedByMedicine.
func referencedByChild(ctx context.Context, r *Repository, table, column string, id uuid.UUID) (bool, error) {
	q := `SELECT EXISTS (
			SELECT 1 FROM ` + table + `
			WHERE ` + column + ` = $1 AND deleted_at IS NULL
		)`
	var ok bool
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// ---------------------------------------------------------------------------
// Page execution
// ---------------------------------------------------------------------------

// listRows runs the COUNT and the page SELECT and collects the rows with scan.
//
// It returns the number of rows matching the same WHERE clause, which the service
// turns into pagination.Meta. When the count is zero the SELECT is skipped
// entirely and the slice stays empty — an empty page is not an error, and querying
// for it would only cost a round trip.
//
// The slice is non-nil when empty, because callers JSON-encode it and a nil slice
// would emit `null` instead of `[]`.
func listRows[T any](
	ctx context.Context,
	r *Repository,
	spec tableSpec,
	scan scanner[T],
	f ListFilter,
	o pagination.Offset,
) ([]T, int64, error) {
	where, args := buildWhere(spec, f)

	var total int64
	countSQL := `SELECT count(*) FROM ` + spec.table + ` WHERE ` + where
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit, _ := pageBounds(o)
	items := make([]T, 0, limit)
	if total == 0 {
		return items, 0, nil
	}

	offset := offsetFor(o, limit)
	pageArgs := append(append([]any{}, args...), limit, offset)

	listSQL := `SELECT ` + spec.columns + `
		FROM ` + spec.table + `
		WHERE ` + where + `
		` + buildOrderBy(spec, o.Sort) + `
		LIMIT $` + strconv.Itoa(len(pageArgs)-1) + ` OFFSET $` + strconv.Itoa(len(pageArgs))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	// rows.Err() reports a failure that ended iteration early — a dropped
	// connection or a context cancellation — which would otherwise look like a
	// short page.
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// offsetFor is the offset half of pageBounds, named separately so listRows can
// keep LIMIT and OFFSET derived from the same clamped limit.
func offsetFor(o pagination.Offset, limit int) int {
	page := o.Page
	if page < 1 {
		page = 1
	}
	return (page - 1) * limit
}
