package user

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/common/enums"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Repository is the user module's data-access layer over the shared users and
// user_profiles tables. Every method is tenant-scoped: the organization id is a
// mandatory argument on all user reads and writes, so a query can never touch
// another tenant's rows. Methods return raw driver errors (db.ErrNoRows,
// unique-violation, …); the service maps them to domain errors.
//
// Profile reads/writes are keyed by user_id (which is unique) and are only ever
// called for a user the caller has already resolved in-tenant, so they do not
// repeat the organization filter.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// userColumns is the canonical column order shared by every users SELECT and the
// INSERT/UPDATE ... RETURNING. scanUser Scans in exactly this order. Secret and
// auth-owned-mutable columns (password_hash, mfa_secret_encrypted,
// salary_encrypted, last_login_ip, failed_attempts, locked_until,
// password_changed_at) are intentionally excluded — this module does not read
// them.
const userColumns = `id, organization_id, branch_id, employee_code, email, username, phone, ` +
	`status, stage, must_change_password, mfa_enabled, last_login_at, ` +
	`joining_date, employment_type, created_at, updated_at, deleted_at`

// scanUser maps one row into a User.
func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(
		&u.ID, &u.OrganizationID, &u.BranchID, &u.EmployeeCode, &u.Email, &u.Username, &u.Phone,
		&u.Status, &u.Stage, &u.MustChangePassword, &u.MFAEnabled, &u.LastLoginAt,
		&u.JoiningDate, &u.EmploymentType, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &u, nil
}

// profileColumns is the canonical column order for user_profiles reads/writes
// within this module's projection. Extended HR fields and encrypted PII columns
// are excluded on purpose.
const profileColumns = `id, user_id, first_name, last_name, middle_name, display_name, ` +
	`date_of_birth, gender, avatar_url, created_at, updated_at`

// scanProfile maps one row into a UserProfile.
func scanProfile(row pgx.Row) (*UserProfile, error) {
	var p UserProfile
	if err := row.Scan(
		&p.ID, &p.UserID, &p.FirstName, &p.LastName, &p.MiddleName, &p.DisplayName,
		&p.DateOfBirth, &p.Gender, &p.AvatarURL, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

// InsertUser creates a user and returns the stored row (server defaults and
// trigger-managed timestamps included). organization_id and PasswordHash must
// already be set on u by the service. A duplicate email / username /
// (organization_id, employee_code) surfaces as a unique violation for the
// service to translate to AlreadyExists. Intended to run inside the create
// transaction alongside InsertProfile.
func (r *Repository) InsertUser(ctx context.Context, u *User) (*User, error) {
	const q = `INSERT INTO users (
			organization_id, branch_id, employee_code, email, username, phone,
			password_hash, status, stage, must_change_password,
			joining_date, employment_type
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING ` + userColumns
	return scanUser(r.db.FromCtx(ctx).QueryRow(ctx, q,
		u.OrganizationID, u.BranchID, u.EmployeeCode, u.Email, u.Username, u.Phone,
		u.PasswordHash, u.Status, u.Stage, u.MustChangePassword,
		u.JoiningDate, u.EmploymentType,
	))
}

// InsertProfile creates the identity profile for a freshly inserted user and
// returns the stored row. p.UserID must reference the user created in the same
// transaction. A duplicate user_id (a second profile for one user) surfaces as a
// unique violation.
func (r *Repository) InsertProfile(ctx context.Context, p *UserProfile) (*UserProfile, error) {
	const q = `INSERT INTO user_profiles (
			user_id, first_name, last_name, middle_name, display_name,
			date_of_birth, gender, avatar_url
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING ` + profileColumns
	return scanProfile(r.db.FromCtx(ctx).QueryRow(ctx, q,
		p.UserID, p.FirstName, p.LastName, p.MiddleName, p.DisplayName,
		p.DateOfBirth, p.Gender, p.AvatarURL,
	))
}

// FindByID returns the non-deleted user with the given id within orgID. Returns
// db.ErrNoRows if it does not exist, is soft-deleted, or belongs to a different
// organization (the org filter makes cross-tenant reads look missing).
func (r *Repository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*User, error) {
	const q = `SELECT ` + userColumns + `
		FROM users
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanUser(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// FindByIDForUpdate is FindByID with a row lock, for the read-modify-write of a
// PATCH / status change / soft-delete inside a transaction. Always targets the
// primary.
func (r *Repository) FindByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*User, error) {
	const q = `SELECT ` + userColumns + `
		FROM users
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		FOR UPDATE`
	return scanUser(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// FindProfileByUserID returns the identity profile for a user. Callers resolve
// the user in-tenant first, so this is not org-filtered. Returns db.ErrNoRows if
// no live profile exists.
func (r *Repository) FindProfileByUserID(ctx context.Context, userID uuid.UUID) (*UserProfile, error) {
	const q = `SELECT ` + profileColumns + `
		FROM user_profiles
		WHERE user_id = $1 AND deleted_at IS NULL`
	return scanProfile(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, userID))
}

// UpdateUser writes the editable users columns of u and returns the refreshed
// row. Identity/tenant columns (email, organization_id), credentials
// (password_hash), lifecycle (status, stage) and all auth-owned state are never
// changed here — status has its own method, and the rest have dedicated flows.
// Returns db.ErrNoRows if the row is missing/deleted.
func (r *Repository) UpdateUser(ctx context.Context, u *User) (*User, error) {
	const q = `UPDATE users SET
			branch_id = $3, employee_code = $4, username = $5, phone = $6,
			joining_date = $7, employment_type = $8
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING ` + userColumns
	return scanUser(r.db.FromCtx(ctx).QueryRow(ctx, q,
		u.ID, u.OrganizationID, u.BranchID, u.EmployeeCode, u.Username, u.Phone,
		u.JoiningDate, u.EmploymentType,
	))
}

// UpdateProfile writes the editable identity columns of p (keyed by user_id) and
// returns the refreshed row. Returns db.ErrNoRows if no live profile exists.
func (r *Repository) UpdateProfile(ctx context.Context, p *UserProfile) (*UserProfile, error) {
	const q = `UPDATE user_profiles SET
			first_name = $2, last_name = $3, middle_name = $4, display_name = $5,
			date_of_birth = $6, gender = $7, avatar_url = $8
		WHERE user_id = $1 AND deleted_at IS NULL
		RETURNING ` + profileColumns
	return scanProfile(r.db.FromCtx(ctx).QueryRow(ctx, q,
		p.UserID, p.FirstName, p.LastName, p.MiddleName, p.DisplayName,
		p.DateOfBirth, p.Gender, p.AvatarURL,
	))
}

// UpdateStatus sets only the lifecycle status of a user within orgID and returns
// the refreshed row. Kept separate from UpdateUser so a status change touches
// exactly one column and can be composed with its side effects (e.g. session
// revocation) inside the service's transaction. Returns db.ErrNoRows if missing.
func (r *Repository) UpdateStatus(ctx context.Context, orgID, id uuid.UUID, status enums.UserStatus) (*User, error) {
	const q = `UPDATE users SET status = $3
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING ` + userColumns
	return scanUser(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID, status))
}

// SoftDelete marks the user deleted (deleted_at = now()) within orgID and reports
// whether a row was affected (false → not found / already deleted). The partial
// unique indexes on email / username / (organization_id, employee_code) all
// exclude deleted rows, so those identifiers become reusable immediately after
// deletion. The profile row is left intact (it is only reachable via a live user).
func (r *Repository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	const q = `UPDATE users SET deleted_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// BranchBelongsToOrg reports whether branchID is a live branch of orgID. The
// service calls it before accepting a client-supplied branch_id: the foreign key
// alone only proves the branch exists somewhere, not that it belongs to the
// caller's tenant, so without this check a user could be pinned to another
// organization's branch. Runs on the primary (it guards a write).
func (r *Repository) BranchBelongsToOrg(ctx context.Context, orgID, branchID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (
		SELECT 1 FROM branches
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL)`
	var ok bool
	err := r.db.FromCtx(ctx).QueryRow(ctx, q, branchID, orgID).Scan(&ok)
	return ok, err
}

// UpsertBranchAssignment grants branchID to userID within orgID, refreshing the
// optional expiry and granting actor. The partial unique index
// (user_id, branch_id) WHERE deleted_at IS NULL makes the write idempotent: a
// re-grant of the same live branch updates expires_at / granted_by / granted_at
// instead of duplicating the row, and resurrects a soft-deleted row. grantedBy
// may be nil for a system-driven grant. Intended to run inside the service's
// transaction (r.db.FromCtx), so it commits atomically with the authz bump.
func (r *Repository) UpsertBranchAssignment(ctx context.Context, orgID, userID, branchID uuid.UUID, expiresAt *time.Time, grantedBy *uuid.UUID, now time.Time) error {
	const q = `INSERT INTO user_branch_assignments
			(user_id, organization_id, branch_id, expires_at, granted_by, granted_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, branch_id) WHERE deleted_at IS NULL
		DO UPDATE SET
			expires_at = EXCLUDED.expires_at,
			granted_by = EXCLUDED.granted_by,
			granted_at = EXCLUDED.granted_at,
			deleted_at = NULL`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, orgID, branchID, expiresAt, grantedBy, now)
	return err
}

// RemoveBranchAssignment soft-deletes the assignment of branchID to userID within
// orgID and reports whether a live row was removed (false → not assigned, so the
// service can 404). Because the partial unique index excludes deleted rows, the
// branch becomes immediately re-grantable. Intended to run inside the service's
// transaction, committing atomically with the authz bump.
func (r *Repository) RemoveBranchAssignment(ctx context.Context, orgID, userID, branchID uuid.UUID) (bool, error) {
	const q = `UPDATE user_branch_assignments SET deleted_at = now()
		WHERE user_id = $1 AND organization_id = $2 AND branch_id = $3 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, orgID, branchID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListBranchAssignments returns the user's live, non-expired branch assignments
// within orgID, newest grant first. It deliberately excludes the user's home
// branch (users.branch_id) — callers that want the full effective subset use the
// auth module's ListUserBranchIDs, which unions the two. Runs on the replica via
// ReadFromCtx (it is a read-only admin view).
func (r *Repository) ListBranchAssignments(ctx context.Context, orgID, userID uuid.UUID, now time.Time) ([]BranchAssignmentItem, error) {
	const q = `SELECT branch_id, granted_by, granted_at, expires_at
		FROM user_branch_assignments
		WHERE user_id = $1 AND organization_id = $2 AND deleted_at IS NULL
		  AND (expires_at IS NULL OR expires_at > $3)
		ORDER BY granted_at DESC`
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, userID, orgID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BranchAssignmentItem, 0, 4)
	for rows.Next() {
		var it BranchAssignmentItem
		if err := rows.Scan(&it.BranchID, &it.GrantedBy, &it.GrantedAt, &it.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListFilter carries the optional filters for List (decoupled from the HTTP DTO
// so the repository does not import the transport layer's query struct shape).
// Status/Stage are pre-validated enum strings ("" = any); BranchID is nil for
// "any branch"; Q is a free-text needle.
type ListFilter struct {
	Status   string
	Stage    string
	BranchID *uuid.UUID
	Q        string
}

// listSortColumns is the allow-list of columns GET /users may sort by. Any other
// "sort=" value is rejected, so the ORDER BY can never be built from raw client
// input (SQL-injection safe by construction). All are on the users table and are
// qualified with the "u." alias in buildOrderBy.
var listSortColumns = map[string]struct{}{
	"created_at":    {},
	"email":         {},
	"status":        {},
	"last_login_at": {},
	"employee_code": {},
}

// List returns a filtered, paginated page of users for orgID plus the total count
// matching the same filters (for pagination meta). It LEFT JOINs user_profiles
// (a 1:1 relation, so the join never multiplies rows) to compute a display name
// and to let the free-text search span profile names as well as the user's own
// identifiers. All values are passed as bind parameters.
func (r *Repository) List(ctx context.Context, orgID uuid.UUID, f ListFilter, o pagination.Offset) ([]ListItem, int64, error) {
	where := []string{"u.organization_id = $1", "u.deleted_at IS NULL"}
	args := []any{orgID}

	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, "u.status = $"+strconv.Itoa(len(args)))
	}
	if f.Stage != "" {
		args = append(args, f.Stage)
		where = append(where, "u.stage = $"+strconv.Itoa(len(args)))
	}
	if f.BranchID != nil {
		args = append(args, *f.BranchID)
		where = append(where, "u.branch_id = $"+strconv.Itoa(len(args)))
	}
	if f.Q != "" {
		args = append(args, "%"+f.Q+"%")
		p := "$" + strconv.Itoa(len(args))
		where = append(where, "(u.email ILIKE "+p+" OR u.username ILIKE "+p+
			" OR u.employee_code ILIKE "+p+" OR up.first_name ILIKE "+p+
			" OR up.last_name ILIKE "+p+" OR up.display_name ILIKE "+p+")")
	}
	whereSQL := strings.Join(where, " AND ")

	const joinSQL = ` FROM users u
		LEFT JOIN user_profiles up ON up.user_id = u.id AND up.deleted_at IS NULL
		WHERE `

	// Count first (same filters, no paging) for the pagination meta.
	var total int64
	countSQL := `SELECT count(*)` + joinSQL + whereSQL
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []ListItem{}, 0, nil
	}

	orderSQL := buildOrderBy(o.Sort)

	// Page args: LIMIT and OFFSET occupy the next two placeholders.
	args = append(args, o.Limit, o.OffsetSQL())
	listSQL := `SELECT u.id, u.email, u.username,
			COALESCE(up.display_name, up.first_name || ' ' || up.last_name, u.email) AS full_name,
			u.status, u.stage, u.branch_id, u.employee_code, u.last_login_at, u.created_at` +
		joinSQL + whereSQL + `
		` + orderSQL + `
		LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]ListItem, 0, o.Limit)
	for rows.Next() {
		var it ListItem
		if err := rows.Scan(
			&it.ID, &it.Email, &it.Username, &it.FullName,
			&it.Status, &it.Stage, &it.BranchID, &it.EmployeeCode, &it.LastLoginAt, &it.CreatedAt,
		); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// buildOrderBy turns a validated "sort=" string into an ORDER BY clause using
// only allow-listed columns, each qualified with the "u." alias to avoid
// ambiguity with the joined user_profiles table. It falls back to a deterministic
// default (newest first) so paging is stable, and always appends id as a
// tie-breaker for a total order.
func buildOrderBy(sort string) string {
	fields, err := pagination.ParseSort(sort, listSortColumns)
	if err != nil || len(fields) == 0 {
		return "ORDER BY u.created_at DESC, u.id ASC"
	}
	parts := make([]string, 0, len(fields)+1)
	for _, f := range fields {
		dir := "ASC"
		if f.Desc {
			dir = "DESC"
		}
		parts = append(parts, "u."+f.Field+" "+dir)
	}
	parts = append(parts, "u.id ASC")
	return "ORDER BY " + strings.Join(parts, ", ")
}
