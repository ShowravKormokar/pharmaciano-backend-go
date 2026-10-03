// Service layer for the eleven central MasterData resources.
//
// Conventions are inherited from warehouse/service.go, the reference
// implementation:
//
//   - A concrete *Repository and the shared *db.DB, not interfaces. No module in
//     this repository declares a repository interface, so neither does this one.
//   - This layer is the ONLY place that maps a raw driver error to a domain
//     *errs.AppError, so SQL details never reach a caller.
//   - It owns the transaction boundary via s.db.WithTx, but only where one logical
//     operation is genuinely more than one statement (contract §15).
//   - It must not import gin, net/http or a pgxpool, and must not reference
//     appctx.OrgID or appctx.BranchScope: MasterData is central reference data with
//     no tenant and no branch dimension (contract §3, §13, D-01).
//
// SCOPE — the file holds only what is genuinely identical across the eleven
// resources: the guard, the error mapping, the reference check, the list page, the
// delete-time reference guard and the DTO-to-default helpers. Every resource's own
// rules live in service_<resource>.go, mirroring the repository layout, so the SQL
// for a table and the rules for a table stay in one place each.
//
// This package deliberately does NOT normalise `code` (trim, uppercase or
// otherwise). Contract §11.2 records code normalisation as UNRESOLVED — no
// completed module normalises a code, so there is no established rule to copy and
// inventing one would silently change what two callers sending "Tab" and "TAB" mean
// relative to the partial unique indexes, which are case-sensitive.

package masterdata

import (
	"context"
	"errors"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Resource names as they appear in an error message. Declared once so the eleven
// per-resource files cannot drift into eleven different spellings of the same 404
// ("category not found" vs "product category not found" would be a visible API
// change, because the message is serialized into the response body).
const (
	resProductCategory  = "product category"
	resDosageForm       = "dosage form"
	resRoute            = "route"
	resPackageType      = "package type"
	resUnitType         = "unit type"
	resStorageCondition = "storage condition"
	resTaxRate          = "tax rate"
	resDrugGroup        = "drug group"
	resDrugClass        = "drug class"
	resTherapeuticClass = "therapeutic class"
	resGenericMedicine  = "generic medicine"
)

// Service holds the MasterData business rules: the SUPER_ADMIN write guard, the
// cross-row reference and hierarchy checks, the PATCH merge semantics, the D-19
// referenced-row guard, and the translation of driver errors into domain errors.
type Service struct {
	repo   *Repository
	db     *db.DB
	limits Limits
}

// NewService assembles the service from its repository, the shared database handle
// (needed for the transactions that keep multi-statement writes atomic) and the
// configured validation bounds.
func NewService(repo *Repository, database *db.DB, limits Limits) *Service {
	return &Service{repo: repo, db: database, limits: limits}
}

// ---------------------------------------------------------------------------
// Authorization
// ---------------------------------------------------------------------------

// requireSuperAdmin rejects a write by anyone who is not a SUPER_ADMIN.
//
// Contract §7.3: the RBAC seed matrix grants ADMIN masterdata:create|update|delete
// (C-03), and the eleven tables are central reference data shared by every tenant,
// so a per-org grant would be a global capability. The route still requires
// masterdata:<action> — RBAC answers "may this caller ever?", this guard answers "is
// this caller a SUPER_ADMIN?", mirroring organization/service.go:132. Reads carry no
// such guard, per §13.
func (s *Service) requireSuperAdmin(ctx context.Context) error {
	if appctx.IsSuperAdmin(ctx) {
		return nil
	}
	return errs.Forbidden("only a super administrator may modify master data")
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

// mapReadErr maps a repository read error: a missing or soft-deleted row becomes
// NotFound, anything else becomes DatabaseError.
func mapReadErr(err error, resource string) error {
	if errors.Is(err, db.ErrNoRows) {
		return errs.NotFound(resource)
	}
	return errs.DatabaseError(err)
}

// mapWriteErr maps a repository write error.
//
// It handles all four driver conditions contract §18 requires, because a
// RESTRICT foreign key from medicines.category_id surfaces as 23503 rather than
// 23505 and would otherwise be reported as a 500:
//
//	ErrNoRows               -> 404  the row went away between the read and the write
//	unique violation (23505) -> 409  duplicate code among live rows
//	foreign key    (23503)  -> 409  a referenced row does not exist
//	check          (23514)  -> 422  a database-level rule rejected the value
//
// Every message is a fixed string built from resource. The driver error is kept as
// the Cause, which is logged but never serialized (httpx.go:86-90), so constraint
// names and SQL text stay server-side.
func mapWriteErr(err error, resource string) error {
	switch {
	case errors.Is(err, db.ErrNoRows):
		return errs.NotFound(resource)
	// IsUniqueVilation is spelled without the second 'o'. That typo lives in
	// platform/db/postgres.go:202 and is shared with every completed module, so
	// this package calls it as-is rather than renaming a platform symbol from a
	// MasterData file.
	case db.IsUniqueVilation(err):
		return errs.AlreadyExists(resource + " code")
	case db.IsForeignKeyViolation(err):
		return errs.Conflict(resource + " references a record that does not exist")
	case db.IsCheckViolation(err):
		return errs.Wrap(err, errs.CodeBusinessRuleViolation,
			resource+" value is rejected by a database constraint")
	default:
		return errs.DatabaseError(err)
	}
}

// ruleViolation turns a domain-rule failure into a 422 BUSINESS_RULE_VIOLATION.
//
// err is always one of this package's own composed errors (validation.go sentinels
// wrapped with a field name and a value), never a driver error, so passing its
// message through is safe.
func ruleViolation(err error) error {
	return errs.Wrap(err, errs.CodeBusinessRuleViolation, err.Error())
}

// ---------------------------------------------------------------------------
// Cross-row reference checks
// ---------------------------------------------------------------------------

// requireReference proves that a foreign key points at a live row.
//
// A nil reference means "no parent", which is legal on every optional foreign key
// in this schema and needs no check. Otherwise the referenced row must exist and
// not be soft-deleted, which contract §12 (D-12) places in the repository: this
// reads through the resource's own Get, so the deleted_at filter is the one the
// resource already uses rather than a second, divergent notion of "exists".
//
// A missing parent answers 404 naming the PARENT, not the resource being written,
// because the caller's mistake is the reference and that is what they must fix.
// This also matches §18's "Missing/deleted row -> 404".
func requireReference[T any](
	ctx context.Context,
	ref *uuid.UUID,
	get func(context.Context, uuid.UUID) (*T, error),
	resource string,
) error {
	if ref == nil {
		return nil
	}
	if _, err := get(ctx, *ref); err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return errs.NotFound(resource)
		}
		return errs.DatabaseError(err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Delete-time reference guard (D-19)
// ---------------------------------------------------------------------------

// referenceProbe is one "is this row still in use?" question, in the shape both
// referencedByMedicine and referencedByChild expose through their typed wrappers.
type referenceProbe func(context.Context, uuid.UUID) (bool, error)

// checkUnreferenced runs every probe in order and returns the first 409.
//
// This is the D-19 policy: a soft delete is refused while anything still points at
// the row, and the message directs the caller to PATCH {"is_active": false}
// instead, which always succeeds and is the correct response to "a medicine still
// uses this". Deactivating is not interchangeable with deleting — is_active=false
// leaves the row resolvable and still referenced, while deleted_at releases the
// code for reuse (contract §20).
//
// Every probe runs inside the caller's transaction, alongside the locked read and
// the soft delete, so a row cannot gain a new reference between the check and the
// write (contract §15: "Delete with a reference check -> Yes, check-then-act must
// not race").
func checkUnreferenced(ctx context.Context, resource string, id uuid.UUID, probes ...referenceProbe) error {
	for _, probe := range probes {
		used, err := probe(ctx, id)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if used {
			return errs.Conflict(resource +
				" is still in use; deactivate it with {\"is_active\": false} instead of deleting it")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

// listPage is the shared body of all eleven List methods.
//
// The per-resource method is a one-line delegation so each endpoint keeps its own
// typed signature and its own response element type, while Normalize, the
// ?parent_id= parse, the filter assembly and the meta construction happen exactly
// once.
//
// list is the repository's typed List method bound to its resource, which is what
// keeps this generic without reflection.
func listPage[T any](
	ctx context.Context,
	q *ListMasterDataQuery,
	list func(context.Context, ListFilter, pagination.Offset) ([]T, int64, error),
) ([]T, pagination.Meta, error) {
	q.Offset.Normalize()

	// A malformed ?parent_id= is a client error, so it is rejected here rather
	// than being dropped into a filter that would silently match nothing. The
	// handler may also parse it; doing it in the service means every caller gets
	// the same verdict, not just the ones routed through a handler.
	parentID, err := q.ParentUUID()
	if err != nil {
		return nil, pagination.Meta{}, errs.Validation("parent_id is not a valid UUID",
			errs.FieldError{
				Field:   "parent_id",
				Rule:    ruleInvalidUUID,
				Message: "must be a UUID",
			})
	}

	filter := ListFilter{
		IsActive: q.IsActive,
		Q:        q.NormalizedQ(),
		ParentID: parentID,
	}
	items, total, err := list(ctx, filter, q.Offset)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}
	return items, pagination.BuildOffsetMeta(q.Offset, total), nil
}

// ---------------------------------------------------------------------------
// DTO default resolution
// ---------------------------------------------------------------------------

// intOr returns *p when set and def otherwise — the *int sibling of the boolOr that
// already exists at seed.go:504.
//
// The repository's Create<Resource> writes every non-generated column
// unconditionally, including the ones whose schema default is meaningful
// (contract §28.5). A nil SortOrder therefore arrives as the model's zero value
// rather than as the column default, so the service must resolve it before
// calling Create. boolOr covers the is_active/is_default/is_controlled booleans.
func intOr(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}

// ---------------------------------------------------------------------------
// Numeric and pair rules
// ---------------------------------------------------------------------------

// requireConversionFactor rejects a conversion_factor the column cannot hold.
//
// It applies representability ONLY — finiteness and NUMERIC(15,6) — and
// deliberately NOT a positivity or bounds rule: contract §11.2 and §26.4 keep
// conversion_factor bounds UNRESOLVED, and seed/unit_types.yaml:25 describes the
// column as a per-medicine default rather than an invariant, so a `> 0` rule here
// would invent a business decision nobody has made. The owner confirmed this
// during Phase 05.
func requireConversionFactor(v *float64) error {
	if v == nil || FitsConversionFactor(*v) {
		return nil
	}
	return errs.Validation("conversion_factor is not a valid conversion factor",
		errs.FieldError{
			Field:   "conversion_factor",
			Rule:    ruleInvalidNumber,
			Message: "must be a finite number representable by NUMERIC(15,6)",
		})
}

// requireTemperatureRange rejects a storage band that is not ordered, or whose ends
// do not fit NUMERIC(5,2).
//
// The service re-checks this even though the DTO already registers the same rule,
// because a PATCH naming only one bound cannot be decided from the payload alone:
// whether the merged row is still ordered depends on the STORED value of the other
// end. The caller passes the merged model, so this validates exactly what the
// database will check via ck_storage_conditions_temp_range (000020).
func requireTemperatureRange(min, max *float64) error {
	if err := ValidateTemperatureRange(min, max); err != nil {
		rule := ruleInvalidNumber
		if errors.Is(err, ErrTemperatureRange) {
			rule = ruleTemperatureRange
		}
		return errs.Validation("temperature range is invalid",
			errs.FieldError{
				Field:   "min_temp_celsius",
				Rule:    rule,
				Message: err.Error(),
			})
	}
	return nil
}

// requireRatePercent applies the configured tax bounds.
//
// Representability and the optional business maximum come from Limits; the
// non-negative sign is the DTO's `gte=0` tag and is not duplicated here.
func (s *Service) requireRatePercent(v float64) error {
	if err := s.limits.ValidateRatePercent(v); err != nil {
		rule := ruleInvalidNumber
		if errors.Is(err, ErrRateAboveMaximum) {
			rule = ruleAboveMaximum
		}
		return errs.Validation("rate_percent is invalid",
			errs.FieldError{
				Field:   "rate_percent",
				Rule:    rule,
				Message: err.Error(),
			})
	}
	return nil
}
