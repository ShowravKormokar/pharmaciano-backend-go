package rbac

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Repository is the rbac data-access layer. Role and permission mutations are
// tenant-scoped by organization id (system rows carry a NULL org and are
// read-only through the API); the join tables (role_permissions, user_roles)
// and the enforcer-snapshot loaders round it out. Methods return raw driver
// errors for the service to translate.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// Column lists are the canonical order shared by every SELECT and the
// INSERT/UPDATE ... RETURNING for a table; the matching scan* helper Scans in
// exactly this order.
const (
	roleColumns       = `id, organization_id, name, description, is_active, is_system, priority, created_at, updated_at, deleted_at`
	permissionColumns = `id, module, action, description, is_system, created_at, updated_at, deleted_at`
)

func scanRole(row pgx.Row) (*Role, error) {
	var r Role
	if err := row.Scan(
		&r.ID, &r.OrganizationID, &r.Name, &r.Description, &r.IsActive, &r.IsSystem,
		&r.Priority, &r.CreatedAt, &r.UpdatedAt, &r.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanPermission(row pgx.Row) (*Permission, error) {
	var p Permission
	if err := row.Scan(
		&p.ID, &p.Module, &p.Action, &p.Description, &p.IsSystem,
		&p.CreatedAt, &p.UpdatedAt, &p.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

// --- Roles -------------------------------------------------------------------

// InsertRole creates a role and returns the stored row. organization_id,
// is_system and the rest must already be set on r by the service. A duplicate
// name within the org (or among system roles) surfaces as a unique violation.
func (r *Repository) InsertRole(ctx context.Context, role *Role) (*Role, error) {
	const q = `INSERT INTO roles (organization_id, name, description, is_active, is_system, priority)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING ` + roleColumns
	return scanRole(r.db.FromCtx(ctx).QueryRow(ctx, q,
		role.OrganizationID, role.Name, role.Description, role.IsActive, role.IsSystem, role.Priority,
	))
}

// FindRoleForView returns a role the caller's org may see: either one of its own
// tenant roles or a shared system role (organization_id NULL). A role belonging
// to another org is indistinguishable from missing (NotFound), never leaked.
func (r *Repository) FindRoleForView(ctx context.Context, orgID, id uuid.UUID) (*Role, error) {
	const q = `SELECT ` + roleColumns + `
		FROM roles
		WHERE id = $1 AND (organization_id = $2 OR organization_id IS NULL) AND deleted_at IS NULL`
	return scanRole(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// FindManagedRoleForUpdate locks and returns a role the caller's org may
// *modify*: only its own tenant roles. System roles (NULL org) and other tenants'
// roles are excluded, so they read as NotFound and can never be edited or
// deleted through the API. Always targets the primary within a tx.
func (r *Repository) FindManagedRoleForUpdate(ctx context.Context, orgID, id uuid.UUID) (*Role, error) {
	const q = `SELECT ` + roleColumns + `
		FROM roles
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		FOR UPDATE`
	return scanRole(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// UpdateRole writes the mutable columns of a tenant role and returns the
// refreshed row. The organization_id guard means a system role can never be
// updated here (its org is NULL). Returns db.ErrNoRows if the row is
// missing/deleted/foreign.
func (r *Repository) UpdateRole(ctx context.Context, orgID uuid.UUID, role *Role) (*Role, error) {
	const q = `UPDATE roles SET
			name = $3, description = $4, is_active = $5, priority = $6
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING ` + roleColumns
	return scanRole(r.db.FromCtx(ctx).QueryRow(ctx, q,
		role.ID, orgID, role.Name, role.Description, role.IsActive, role.Priority,
	))
}

// SoftDeleteRole marks a tenant role deleted within orgID and reports whether a
// row was affected. System roles are excluded by the org filter. Assignments and
// grants to a soft-deleted role become inert automatically: every snapshot
// loader filters roles.deleted_at IS NULL.
func (r *Repository) SoftDeleteRole(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	const q = `UPDATE roles SET deleted_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// roleSortColumns is the allow-list of columns GET /roles may sort by.
var roleSortColumns = map[string]struct{}{
	"name":       {},
	"priority":   {},
	"created_at": {},
	"is_active":  {},
}

// RoleListFilter carries the optional filters for ListRoles.
type RoleListFilter struct {
	IsActive *bool
}

// ListRoles returns a filtered, paginated page of roles visible to orgID — its
// own tenant roles plus all system roles — and the matching total. All values
// are bound parameters. is_global (organization_id IS NULL) is projected so the
// client can mark system roles read-only.
func (r *Repository) ListRoles(ctx context.Context, orgID uuid.UUID, f RoleListFilter, o pagination.Offset) ([]RoleListItem, int64, error) {
	where := []string{"(organization_id = $1 OR organization_id IS NULL)", "deleted_at IS NULL"}
	args := []any{orgID}

	if f.IsActive != nil {
		args = append(args, *f.IsActive)
		where = append(where, "is_active = $"+strconv.Itoa(len(args)))
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, `SELECT count(*) FROM roles WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []RoleListItem{}, 0, nil
	}

	orderSQL := buildOrderBy(o.Sort, roleSortColumns, "ORDER BY priority DESC, name ASC, id ASC")
	args = append(args, o.Limit, o.OffsetSQL())
	listSQL := `SELECT id, name, description, is_active, is_system, priority, (organization_id IS NULL) AS is_global
		FROM roles
		WHERE ` + whereSQL + `
		` + orderSQL + `
		LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]RoleListItem, 0, o.Limit)
	for rows.Next() {
		var it RoleListItem
		if err := rows.Scan(&it.ID, &it.Name, &it.Description, &it.IsActive, &it.IsSystem, &it.Priority, &it.IsGlobal); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// --- Permissions -------------------------------------------------------------

// InsertPermission registers a capability and returns the stored row. A
// duplicate (module, action) surfaces as a unique violation.
func (r *Repository) InsertPermission(ctx context.Context, p *Permission) (*Permission, error) {
	const q = `INSERT INTO permissions (module, action, description, is_system)
		VALUES ($1,$2,$3,$4)
		RETURNING ` + permissionColumns
	return scanPermission(r.db.FromCtx(ctx).QueryRow(ctx, q, p.Module, p.Action, p.Description, p.IsSystem))
}

// FindPermissionByID returns the non-deleted permission with the given id.
// Permissions are a global catalogue, so this is not org-scoped.
func (r *Repository) FindPermissionByID(ctx context.Context, id uuid.UUID) (*Permission, error) {
	const q = `SELECT ` + permissionColumns + ` FROM permissions WHERE id = $1 AND deleted_at IS NULL`
	return scanPermission(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id))
}

// FindPermissionsByIDs returns the non-deleted permissions matching ids. The
// service compares the returned count against the requested (de-duplicated) set
// to reject a grant that references an unknown permission. Uses an explicit
// placeholder IN-list rather than a uuid[] bind to stay portable across the pgx
// array codec; the set is bounded by the DTO's max=500.
func (r *Repository) FindPermissionsByIDs(ctx context.Context, ids []uuid.UUID) ([]Permission, error) {
	if len(ids) == 0 {
		return []Permission{}, nil
	}
	args := make([]any, len(ids))
	ph := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		ph[i] = "$" + strconv.Itoa(i+1)
	}
	q := `SELECT ` + permissionColumns + ` FROM permissions
		WHERE id IN (` + strings.Join(ph, ",") + `) AND deleted_at IS NULL`

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Permission, 0, len(ids))
	for rows.Next() {
		p, err := scanPermission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// permissionSortColumns is the allow-list of columns GET /permissions may sort by.
var permissionSortColumns = map[string]struct{}{
	"module":     {},
	"action":     {},
	"created_at": {},
}

// PermissionListFilter carries the optional filters for ListPermissions.
type PermissionListFilter struct {
	Module *string
}

// ListPermissions returns a filtered, paginated page of the capability catalogue
// plus the matching total.
func (r *Repository) ListPermissions(ctx context.Context, f PermissionListFilter, o pagination.Offset) ([]PermissionListItem, int64, error) {
	where := []string{"deleted_at IS NULL"}
	args := []any{}

	if f.Module != nil {
		args = append(args, *f.Module)
		where = append(where, "module = $"+strconv.Itoa(len(args)))
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, `SELECT count(*) FROM permissions WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []PermissionListItem{}, 0, nil
	}

	orderSQL := buildOrderBy(o.Sort, permissionSortColumns, "ORDER BY module ASC, action ASC, id ASC")
	args = append(args, o.Limit, o.OffsetSQL())
	listSQL := `SELECT id, module, action, description, is_system
		FROM permissions
		WHERE ` + whereSQL + `
		` + orderSQL + `
		LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]PermissionListItem, 0, o.Limit)
	for rows.Next() {
		var it PermissionListItem
		if err := rows.Scan(&it.ID, &it.Module, &it.Action, &it.Description, &it.IsSystem); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// --- Role ↔ permission grants ------------------------------------------------

// ListRolePermissions returns the permissions currently granted to roleID (only
// non-deleted permissions), ordered for stable display.
func (r *Repository) ListRolePermissions(ctx context.Context, roleID uuid.UUID) ([]Permission, error) {
	const q = `SELECT p.id, p.module, p.action, p.description, p.is_system, p.created_at, p.updated_at, p.deleted_at
		FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = $1 AND p.deleted_at IS NULL
		ORDER BY p.module, p.action`
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Permission, 0, 16)
	for rows.Next() {
		p, err := scanPermission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ReplaceRolePermissions sets roleID's grant set to exactly permIDs, atomically:
// it clears the existing grants and re-inserts the given ones. It MUST run inside
// the caller's transaction (the service wraps it) so a reader never observes the
// role with an empty grant set mid-swap. Passing an empty permIDs revokes all.
// Uses a placeholder VALUES list (bounded by the DTO's max=500) rather than a
// uuid[] bind for pgx portability; ON CONFLICT DO NOTHING de-duplicates.
func (r *Repository) ReplaceRolePermissions(ctx context.Context, roleID uuid.UUID, permIDs []uuid.UUID, grantedBy *uuid.UUID) error {
	q := r.db.FromCtx(ctx)
	if _, err := q.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return err
	}
	if len(permIDs) == 0 {
		return nil
	}

	// args[0]=roleID, args[1]=grantedBy, args[2..]=permission ids.
	args := make([]any, 0, len(permIDs)+2)
	args = append(args, roleID, grantedBy)
	rowsSQL := make([]string, 0, len(permIDs))
	for i, pid := range permIDs {
		args = append(args, pid)
		rowsSQL = append(rowsSQL, "($1, $"+strconv.Itoa(i+3)+", $2)")
	}
	insert := `INSERT INTO role_permissions (role_id, permission_id, granted_by) VALUES ` +
		strings.Join(rowsSQL, ",") +
		` ON CONFLICT (role_id, permission_id) DO NOTHING`
	_, err := q.Exec(ctx, insert, args...)
	return err
}

// --- User ↔ role assignments -------------------------------------------------

// AssignableRoleExists reports whether roleID is a role orgID may assign: an
// active, non-deleted role that is either owned by orgID or a shared system
// role. The service calls it before assigning so a user can never be granted a
// foreign, inactive or missing role. Runs on the primary to avoid a replica-lag
// TOCTOU against a role that was just created/activated.
func (r *Repository) AssignableRoleExists(ctx context.Context, orgID, roleID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (
			SELECT 1 FROM roles
			WHERE id = $1 AND (organization_id = $2 OR organization_id IS NULL)
			  AND deleted_at IS NULL AND is_active
		)`
	var ok bool
	if err := r.db.FromCtx(ctx).QueryRow(ctx, q, roleID, orgID).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// AssignUserRole grants roleID to userID (upsert on the (user_id, role_id) key):
// re-assigning an existing role refreshes its branch scope, actor and expiry
// rather than erroring. The service validates the role first via
// AssignableRoleExists.
func (r *Repository) AssignUserRole(ctx context.Context, ur *UserRole) error {
	const q = `INSERT INTO user_roles (user_id, role_id, branch_id, assigned_by, expires_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (user_id, role_id) DO UPDATE SET
			branch_id   = EXCLUDED.branch_id,
			assigned_by = EXCLUDED.assigned_by,
			expires_at  = EXCLUDED.expires_at,
			assigned_at = now()`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, ur.UserID, ur.RoleID, ur.BranchID, ur.AssignedBy, ur.ExpiresAt)
	return err
}

// RevokeUserRole removes roleID from userID and reports whether a row was
// removed (false = the assignment did not exist).
func (r *Repository) RevokeUserRole(ctx context.Context, userID, roleID uuid.UUID) (bool, error) {
	const q = `DELETE FROM user_roles WHERE user_id = $1 AND role_id = $2`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, userID, roleID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// BumpUserAuthzVersion and BumpRoleMembersAuthzVersion advance durable epochs
// in the same transaction as RBAC mutations. Tokens minted before that commit
// are rejected by auth.Authenticate on their next request.
func (r *Repository) BumpUserAuthzVersion(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.FromCtx(ctx).Exec(ctx, `UPDATE users SET authz_version = authz_version + 1 WHERE id = $1 AND deleted_at IS NULL`, userID)
	return err
}

func (r *Repository) BumpRoleMembersAuthzVersion(ctx context.Context, roleID uuid.UUID) error {
	_, err := r.db.FromCtx(ctx).Exec(ctx, `UPDATE users u SET authz_version = u.authz_version + 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_id = $1 AND u.deleted_at IS NULL`, roleID)
	return err
}

// BumpOrgRBACGeneration advances the organization-level RBAC counter (ADR §30).
// It is the O(1) companion to per-user authz_version bumps: a role definition
// change or grant set swap can affect every user in the org, but instead of
// touching every users row the org-level counter advances and the next
// Authorizer.Enforce call rebuilds the snapshot under the new generation,
// leaving the old (now-orphaned) cached entries to expire by TTL.
func (r *Repository) BumpOrgRBACGeneration(ctx context.Context, orgID uuid.UUID) error {
	_, err := r.db.FromCtx(ctx).Exec(ctx, `SELECT bump_organization_rbac_generation($1)`, orgID)
	return err
}

// ListUserRoles returns userID's role assignments joined to the role, highest
// privilege first. Assignments to a soft-deleted role are omitted.
func (r *Repository) ListUserRoles(ctx context.Context, userID uuid.UUID) ([]UserRoleItem, error) {
	const q = `SELECT r.id, r.name, r.priority, r.is_system, ur.branch_id, ur.assigned_at, ur.expires_at
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1 AND r.deleted_at IS NULL
		ORDER BY r.priority DESC, r.name ASC`
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]UserRoleItem, 0, 8)
	for rows.Next() {
		var it UserRoleItem
		if err := rows.Scan(&it.RoleID, &it.Name, &it.Priority, &it.IsSystem, &it.BranchID, &it.AssignedAt, &it.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// FindUserRoleItemByPrimary returns a single user→role assignment (joined to the
// role) reading from the primary. It exists for read-after-write flows: a write
// and its immediate re-fetch must observe each other, which the replica path
// cannot guarantee under replication lag. A missing row yields db.ErrNoRows.
func (r *Repository) FindUserRoleItemByPrimary(ctx context.Context, userID, roleID uuid.UUID) (*UserRoleItem, error) {
	const q = `SELECT r.id, r.name, r.priority, r.is_system, ur.branch_id, ur.assigned_at, ur.expires_at
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1 AND ur.role_id = $2 AND r.deleted_at IS NULL`
	var it UserRoleItem
	if err := r.db.FromCtx(ctx).QueryRow(ctx, q, userID, roleID).Scan(
		&it.RoleID, &it.Name, &it.Priority, &it.IsSystem, &it.BranchID, &it.AssignedAt, &it.ExpiresAt); err != nil {
		return nil, err
	}
	return &it, nil
}

// --- Enforcer snapshot loaders -----------------------------------------------
//
// These two feed the in-memory enforcer (casbin.go). Both read from the primary
// (FromCtx with no tx → pool), not a replica: the snapshot is loaded
// infrequently (periodic refresh + after each mutation) and must reflect a
// just-committed grant without waiting on replication lag.

// PolicyRow is one (role → permission) grant flattened into the casbin policy
// tuple p = (sub = role name, dom, obj = module, act = action). dom is the
// owning org id as text, or "*" for a system role (organization_id NULL) whose
// permissions apply in every domain — exactly config/casbin_model.conf.
type PolicyRow struct {
	Role   string
	Dom    string
	Module string
	Action string
}

// GroupingRow is one (user → role) assignment flattened into the casbin grouping
// tuple g = (user id, role name, dom). dom is the user's org id as text; a user
// only ever operates within their own org, so this is always the request domain.
// Priority rides along so the enforcer can pick a user's canonical (highest)
// role for the Principal without a second query. Branch is the role grant's
// branch restriction (nil = org-wide); the enforcer folds it into the effective
// branch-scope filter (ADR §19/§22).
type GroupingRow struct {
	UserID   string
	Role     string
	Dom      string
	Priority int
	Branch   *uuid.UUID // nil = applies org-wide; else the role only applies in that branch
}

// LoadPolicies returns every active grant as a policy tuple. Only active,
// non-deleted roles and non-deleted permissions contribute, so disabling a role
// or deleting a permission removes its authority on the next snapshot.
func (r *Repository) LoadPolicies(ctx context.Context) ([]PolicyRow, error) {
	const q = `SELECT r.name, COALESCE(r.organization_id::text, '*'), p.module, p.action
		FROM role_permissions rp
		JOIN roles r       ON r.id = rp.role_id
		JOIN permissions p ON p.id = rp.permission_id
		WHERE r.deleted_at IS NULL AND r.is_active AND p.deleted_at IS NULL`
	rows, err := r.db.FromCtx(ctx).Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PolicyRow, 0, 256)
	for rows.Next() {
		var p PolicyRow
		if err := rows.Scan(&p.Role, &p.Dom, &p.Module, &p.Action); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LoadOrgGenerations returns every live organization's RBAC generation keyed by
// org id. The enforcer folds this into its snapshot so the CachedAuthorizer can
// build a versioned cache key without a per-request database read: a
// rbac_generation bump (a role/permission definition change, ADR §30) invalidates
// every cached snapshot for that org at O(1) on the next snapshot. Runs as part
// of the snapshot load, not on the request path.
func (r *Repository) LoadOrgGenerations(ctx context.Context) (map[uuid.UUID]int64, error) {
	const q = `SELECT id, rbac_generation FROM organizations WHERE deleted_at IS NULL`
	rows, err := r.db.FromCtx(ctx).Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]int64, 8)
	for rows.Next() {
		var id uuid.UUID
		var gen int64
		if err := rows.Scan(&id, &gen); err != nil {
			return nil, err
		}
		out[id] = gen
	}
	return out, rows.Err()
}

// LoadGroupings returns every effective user→role assignment as a grouping
// tuple. Expired assignments (expires_at in the past), inactive/deleted roles and
// deleted users are excluded, so they stop granting access on the next snapshot.
func (r *Repository) LoadGroupings(ctx context.Context) ([]GroupingRow, error) {
	const q = `SELECT ur.user_id::text, r.name, u.organization_id::text, r.priority, ur.branch_id
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		JOIN users u ON u.id = ur.user_id
		WHERE r.deleted_at IS NULL AND r.is_active
		  AND u.deleted_at IS NULL
		  AND (ur.expires_at IS NULL OR ur.expires_at > now())`
	rows, err := r.db.FromCtx(ctx).Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]GroupingRow, 0, 256)
	for rows.Next() {
		var g GroupingRow
		if err := rows.Scan(&g.UserID, &g.Role, &g.Dom, &g.Priority, &g.Branch); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// --- Cross-entity guards -----------------------------------------------------

// UserBelongsToOrg reports whether userID is a live user of orgID. The rbac
// service calls it before assigning/revoking a role so a role can never be
// attached to a user in another tenant (defence in depth on top of the user
// module's own scoping). rbac never *writes* users; this is a read-only join,
// consistent with the enforcer's grouping snapshot which also reads users.
func (r *Repository) UserBelongsToOrg(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (
			SELECT 1 FROM users WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		)`
	var ok bool
	if err := r.db.FromCtx(ctx).QueryRow(ctx, q, userID, orgID).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// BranchBelongsToOrg reports whether branchID is a live branch of orgID. Used to
// validate a branch-scoped assignment; a foreign or unknown branch reads as
// missing so a caller cannot probe other tenants' branch ids.
func (r *Repository) BranchBelongsToOrg(ctx context.Context, orgID, branchID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (
			SELECT 1 FROM branches WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		)`
	var ok bool
	if err := r.db.FromCtx(ctx).QueryRow(ctx, q, branchID, orgID).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// --- Seeding (system catalogue) ----------------------------------------------

// SeedAdvisoryLock is the fixed key for the transaction-scoped advisory lock the
// seeder takes, so two app instances starting at once serialise their seed
// rather than racing on the same rows. Any stable constant works; this one is
// arbitrary but must not collide with other advisory-lock users.
const SeedAdvisoryLock int64 = 0x5262_4143 // "RbAC"

// AcquireSeedLock takes a transaction-scoped advisory lock (auto-released at tx
// end). Must be called inside the seeding transaction.
func (r *Repository) AcquireSeedLock(ctx context.Context) error {
	_, err := r.db.FromCtx(ctx).Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, SeedAdvisoryLock)
	return err
}

// UpsertSystemPermission inserts or refreshes a system permission and returns its
// id. Idempotent on the real UNIQUE(module, action) constraint, so re-running the
// seed keeps descriptions in sync without creating duplicates.
func (r *Repository) UpsertSystemPermission(ctx context.Context, module, action string, description *string) (uuid.UUID, error) {
	const q = `INSERT INTO permissions (module, action, description, is_system)
		VALUES ($1,$2,$3,TRUE)
		ON CONFLICT (module, action) DO UPDATE SET description = EXCLUDED.description
		RETURNING id`
	var id uuid.UUID
	err := r.db.FromCtx(ctx).QueryRow(ctx, q, module, action, description).Scan(&id)
	return id, err
}

// UpsertSystemRole inserts or refreshes a shared system role (organization_id
// NULL, is_system true) and returns its id. It is a three-step upsert:
// insert-or-ignore against the *partial* unique index (which ON CONFLICT ... DO
// UPDATE cannot target cleanly), then sync the mutable metadata, then read the id
// back — all within the caller's seeding transaction so it is atomic and
// idempotent.
func (r *Repository) UpsertSystemRole(ctx context.Context, name string, description *string, priority int) (uuid.UUID, error) {
	q := r.db.FromCtx(ctx)
	const ins = `INSERT INTO roles (organization_id, name, description, is_active, is_system, priority)
		VALUES (NULL, $1, $2, TRUE, TRUE, $3)
		ON CONFLICT DO NOTHING`
	if _, err := q.Exec(ctx, ins, name, description, priority); err != nil {
		return uuid.Nil, err
	}
	const upd = `UPDATE roles SET description = $2, priority = $3, is_active = TRUE
		WHERE organization_id IS NULL AND name = $1 AND is_system = TRUE AND deleted_at IS NULL`
	if _, err := q.Exec(ctx, upd, name, description, priority); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	const sel = `SELECT id FROM roles WHERE organization_id IS NULL AND name = $1 AND deleted_at IS NULL`
	if err := q.QueryRow(ctx, sel, name).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// buildOrderBy turns a validated "sort=" string into an ORDER BY clause using
// only allow-listed columns, falling back to def (which must itself be a safe,
// literal ORDER BY) and always appending an id tie-breaker for determinism.
func buildOrderBy(sort string, allowed map[string]struct{}, def string) string {
	fields, err := pagination.ParseSort(sort, allowed)
	if err != nil || len(fields) == 0 {
		return def
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
