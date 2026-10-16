package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

var (
	ErrInvalidAuditReadScope = errors.New("audit: invalid read scope")
	ErrInvalidAuditCursor    = errors.New("audit: invalid cursor")
	ErrInvalidAuditRequest   = errors.New("audit: invalid repository request")
)

// AuditReadScope is constructed by the service from authenticated identity and
// authorization policy. Never populate it from request query/body fields.
//
// OrganizationID scopes tenant reads. PlatformWide is a privileged bypass
// across all organizations, including platform-level events without an
// organization. Branch-restricted organization reads must explicitly enumerate
// permitted branches and separately opt into unassigned-branch events.
type AuditReadScope struct {
	OrganizationID          *uuid.UUID
	PlatformWide            bool
	PlatformEventsOnly      bool
	AllBranches             bool
	BranchIDs               []uuid.UUID
	IncludeUnassignedBranch bool
}

// Repository performs bounded, read-only access to the current audit_logs
// parent table. PostgreSQL partition pruning comes from mandatory created_at
// bounds on list/search operations. Immutable audit records are deliberately
// not cached in process: every read uses current authorization scope, and
// replica routing is delegated to the shared DB wrapper.
type Repository struct {
	db          *db.DB
	cursorCodec *pagination.CursorCodec
}

// NewRepository wires the shared database handle and the HMAC cursor codec.
// The signing key must be a stable secret of at least 32 bytes shared by all
// application instances; callers must fail startup if codec creation fails.
func NewRepository(database *db.DB, cursorCodec *pagination.CursorCodec) *Repository {
	return &Repository{db: database, cursorCodec: cursorCodec}
}

// List returns one bounded, newest-first page. The query's time range is
// mandatory to guarantee partition pruning; no unbounded COUNT is performed.
func (r *Repository) List(ctx context.Context, scope AuditReadScope, query *ListAuditLogsQuery) (AuditLogListResponse, error) {
	if r == nil || r.db == nil {
		return AuditLogListResponse{}, fmt.Errorf("%w: repository database is not configured", ErrInvalidAuditRequest)
	}
	if query == nil {
		return AuditLogListResponse{}, fmt.Errorf("%w: query is required", ErrInvalidAuditRequest)
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return AuditLogListResponse{}, errors.Join(validation...)
	}
	if err := validateAuditReadScope(scope); err != nil {
		return AuditLogListResponse{}, err
	}
	if r.cursorCodec == nil {
		return AuditLogListResponse{}, fmt.Errorf("%w: cursor signing is not configured", ErrInvalidAuditRequest)
	}

	from, to := query.TimeRange()
	cursorKey, err := auditCursorScopeKey(scope, query.AuditSearchFilters)
	if err != nil {
		return AuditLogListResponse{}, err
	}
	var cursor *pagination.Cursor
	if query.Cursor != "" {
		decoded, decodeErr := r.cursorCodec.Decode(query.Cursor)
		if decodeErr != nil ||
			decoded.ValidateSortKey(cursorKey) != nil ||
			decoded.ID == uuid.Nil ||
			decoded.CreatedAt.IsZero() ||
			decoded.CreatedAt.Before(from) ||
			!decoded.CreatedAt.Before(to) {
			return AuditLogListResponse{}, ErrInvalidAuditCursor
		}
		cursor = &decoded
	}

	const listSQL = `SELECT
			a.id, a.created_at, a.organization_id, a.branch_id, a.user_id,
			a.role_name, a.session_id, a.request_id, a.module, a.action,
			a.entity_type, a.entity_id, a.outcome, a.error_code, a.duration_ms
		FROM audit_logs AS a`
	where, args, err := buildAuditWhere(scope, query.AuditSearchFilters, from, to, cursor)
	if err != nil {
		return AuditLogListResponse{}, err
	}
	limitParam := len(args) + 1
	args = append(args, query.Limit+1)
	sql := listSQL + where + fmt.Sprintf(
		` ORDER BY a.created_at DESC, a.id DESC LIMIT $%d`,
		limitParam,
	)

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, sql, args...)
	if err != nil {
		return AuditLogListResponse{}, fmt.Errorf("audit list query: %w", err)
	}
	defer rows.Close()

	items := make([]AuditLogListItem, 0, query.Limit)
	var last AuditLogListItem
	for rows.Next() {
		var item AuditLogListItem
		if err := scanAuditListItem(rows, &item); err != nil {
			return AuditLogListResponse{}, fmt.Errorf("scan audit list item: %w", err)
		}
		if len(items) == query.Limit {
			return AuditLogListResponse{
				Items: items, HasMore: true,
				NextCursor: r.cursorCodec.Encode(pagination.Cursor{
					CreatedAt: last.OccurredAt,
					ID:        last.ID,
					SortKey:   cursorKey,
				}),
			}, rows.Err()
		}
		items = append(items, item)
		last = item
	}
	if err := rows.Err(); err != nil {
		return AuditLogListResponse{}, fmt.Errorf("iterate audit list: %w", err)
	}
	return AuditLogListResponse{Items: items}, nil
}

// GetByID returns an event visible within scope. Both the ID and partition key
// are required because the partitioned primary key does not guarantee global
// uniqueness for ID by itself.
func (r *Repository) GetByID(ctx context.Context, scope AuditReadScope, id uuid.UUID, occurredAt time.Time) (AuditLog, error) {
	if r == nil || r.db == nil {
		return AuditLog{}, fmt.Errorf("%w: repository database is not configured", ErrInvalidAuditRequest)
	}
	if id == uuid.Nil {
		return AuditLog{}, fmt.Errorf("%w: event ID must be non-zero", ErrInvalidAuditRequest)
	}
	if occurredAt.IsZero() {
		return AuditLog{}, fmt.Errorf("%w: occurrence timestamp is required", ErrInvalidAuditRequest)
	}
	if err := validateAuditReadScope(scope); err != nil {
		return AuditLog{}, err
	}

	const query = `SELECT
			a.id, a.created_at, a.organization_id, a.branch_id, a.user_id,
			a.role_name, a.session_id, a.request_id, a.module, a.action,
			a.entity_type, a.entity_id, host(a.ip), a.browser, a.os, a.device,
			a.location, a.user_agent, a.details, a.before_data, a.after_data,
			a.outcome, a.error_code, a.duration_ms
		FROM audit_logs AS a`
	where, args, err := buildAuditIDWhere(scope, id, occurredAt.UTC())
	if err != nil {
		return AuditLog{}, err
	}
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, query+where+` LIMIT 1`, args...)
	if err != nil {
		return AuditLog{}, fmt.Errorf("audit detail query: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return AuditLog{}, fmt.Errorf("iterate audit detail: %w", err)
		}
		return AuditLog{}, db.ErrNoRows
	}
	event, err := scanAuditLog(rows)
	if err != nil {
		return AuditLog{}, fmt.Errorf("scan audit detail: %w", err)
	}
	if err := rows.Err(); err != nil {
		return AuditLog{}, fmt.Errorf("iterate audit detail: %w", err)
	}
	return event, nil
}

// ListModules returns distinct modules visible within scope and an explicit
// bounded occurrence-time window. It intentionally does not cache values.
func (r *Repository) ListModules(ctx context.Context, scope AuditReadScope, from, to time.Time, limit int) ([]string, error) {
	return r.listDistinctValues(ctx, scope, from, to, limit, "module")
}

// ListActions returns distinct actions visible within scope and an explicit
// bounded occurrence-time window. It intentionally does not cache values.
func (r *Repository) ListActions(ctx context.Context, scope AuditReadScope, from, to time.Time, limit int) ([]string, error) {
	return r.listDistinctValues(ctx, scope, from, to, limit, "action")
}

func (r *Repository) listDistinctValues(
	ctx context.Context,
	scope AuditReadScope,
	from, to time.Time,
	limit int,
	column string,
) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("%w: repository database is not configured", ErrInvalidAuditRequest)
	}
	if err := validateAuditReadScope(scope); err != nil {
		return nil, err
	}
	if err := validateAuditTimeRange(from, to); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = defaultAuditValuesLimit
	} else if limit < 0 {
		return nil, fmt.Errorf("%w: values limit must be positive", ErrInvalidAuditRequest)
	}
	if limit > maxAuditValuesLimit {
		return nil, fmt.Errorf("%w: values limit must not exceed %d", ErrInvalidAuditRequest, maxAuditValuesLimit)
	}
	// column is selected only by the two internal callers above.
	if column != "module" && column != "action" {
		return nil, fmt.Errorf("%w: unsupported distinct-value column", ErrInvalidAuditRequest)
	}

	args := []any{from.UTC(), to.UTC()}
	conditions := []string{"a.created_at >= $1", "a.created_at < $2"}
	scopeConditions, scopeArgs, err := buildScopeConditions(scope, 3)
	if err != nil {
		return nil, err
	}
	conditions = append(conditions, scopeConditions...)
	args = append(args, scopeArgs...)
	args = append(args, limit)

	sql := fmt.Sprintf(`SELECT DISTINCT a.%s
		FROM audit_logs AS a
		WHERE %s
		ORDER BY a.%s
		LIMIT $%d`, column, strings.Join(conditions, " AND "), column, len(args))
	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("audit distinct %s query: %w", column, err)
	}
	defer rows.Close()
	values := make([]string, 0, limit)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan audit %s: %w", column, err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit %s values: %w", column, err)
	}
	return values, nil
}

const (
	defaultAuditValuesLimit = 100
	maxAuditValuesLimit     = 500
)

func buildAuditWhere(
	scope AuditReadScope,
	filters AuditSearchFilters,
	from, to time.Time,
	cursor *pagination.Cursor,
) (string, []any, error) {
	args := []any{from.UTC(), to.UTC()}
	conditions := []string{"a.created_at >= $1", "a.created_at < $2"}

	scopeConditions, scopeArgs, err := buildScopeConditions(scope, len(args)+1)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, scopeConditions...)
	args = append(args, scopeArgs...)

	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if id := filters.ParsedUserID(); id != nil {
		add("a.user_id = $%d", *id)
	}
	if filters.Module != "" {
		add("a.module = $%d", filters.Module)
	}
	if filters.Action != "" {
		add("a.action = $%d", filters.Action)
	}
	if filters.EntityType != "" {
		add("a.entity_type = $%d", filters.EntityType)
	}
	if id := filters.ParsedEntityID(); id != nil {
		add("a.entity_id = $%d", *id)
	}
	if filters.Outcome != "" {
		add("a.outcome = $%d", AuditOutcome(filters.Outcome))
	}
	if ip := filters.ParsedIP(); ip != nil {
		add("a.ip = $%d::inet", ip.String())
	}
	if filters.Q != "" {
		args = append(args, likeAuditLiteral(filters.Q))
		parameter := len(args)
		conditions = append(conditions, fmt.Sprintf(
			`(a.module ILIKE $%d ESCAPE '\' OR
			a.action ILIKE $%d ESCAPE '\' OR
			a.entity_type ILIKE $%d ESCAPE '\' OR
			a.request_id ILIKE $%d ESCAPE '\' OR
			a.error_code ILIKE $%d ESCAPE '\')`,
			parameter, parameter, parameter, parameter, parameter,
		))
	}
	if cursor != nil {
		args = append(args, cursor.CreatedAt.UTC(), cursor.ID)
		conditions = append(conditions, fmt.Sprintf(
			"(a.created_at, a.id) < ($%d, $%d)",
			len(args)-1, len(args),
		))
	}
	return " WHERE " + strings.Join(conditions, " AND "), args, nil
}

func buildAuditIDWhere(scope AuditReadScope, id uuid.UUID, occurredAt time.Time) (string, []any, error) {
	args := []any{id, occurredAt.UTC()}
	conditions := []string{"a.id = $1", "a.created_at = $2"}
	scopeConditions, scopeArgs, err := buildScopeConditions(scope, 3)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, scopeConditions...)
	args = append(args, scopeArgs...)
	return " WHERE " + strings.Join(conditions, " AND "), args, nil
}

func buildScopeConditions(scope AuditReadScope, firstParam int) ([]string, []any, error) {
	if err := validateAuditReadScope(scope); err != nil {
		return nil, nil, err
	}
	if scope.PlatformEventsOnly {
		return []string{"a.organization_id IS NULL"}, nil, nil
	}
	if scope.PlatformWide {
		return nil, nil, nil
	}

	args := []any{*scope.OrganizationID}
	conditions := []string{fmt.Sprintf("a.organization_id = $%d", firstParam)}
	if scope.AllBranches {
		return conditions, args, nil
	}
	if len(scope.BranchIDs) > 0 {
		branchIDs := append([]uuid.UUID(nil), scope.BranchIDs...)
		sort.Slice(branchIDs, func(i, j int) bool {
			return branchIDs[i].String() < branchIDs[j].String()
		})
		args = append(args, branchIDs)
		branchPredicate := fmt.Sprintf("a.branch_id = ANY($%d::uuid[])", firstParam+1)
		if scope.IncludeUnassignedBranch {
			branchPredicate = "(" + branchPredicate + " OR a.branch_id IS NULL)"
		}
		conditions = append(conditions, branchPredicate)
	} else if scope.IncludeUnassignedBranch {
		conditions = append(conditions, "a.branch_id IS NULL")
	}
	return conditions, args, nil
}

func validateAuditReadScope(scope AuditReadScope) error {
	if scope.PlatformEventsOnly {
		if scope.OrganizationID != nil || scope.PlatformWide || scope.AllBranches ||
			len(scope.BranchIDs) > 0 || scope.IncludeUnassignedBranch {
			return fmt.Errorf("%w: platform-event scope cannot include tenant or branch filters", ErrInvalidAuditReadScope)
		}
		return nil
	}
	if scope.PlatformWide {
		if scope.OrganizationID != nil || scope.AllBranches ||
			len(scope.BranchIDs) > 0 || scope.IncludeUnassignedBranch {
			return fmt.Errorf("%w: platform-wide scope cannot include tenant or branch filters", ErrInvalidAuditReadScope)
		}
		return nil
	}
	if scope.OrganizationID == nil || *scope.OrganizationID == uuid.Nil {
		return fmt.Errorf("%w: non-zero organization ID is required", ErrInvalidAuditReadScope)
	}
	if scope.AllBranches && (len(scope.BranchIDs) != 0 || scope.IncludeUnassignedBranch) {
		return fmt.Errorf("%w: all-branch scope cannot also specify branch filters", ErrInvalidAuditReadScope)
	}
	seen := make(map[uuid.UUID]struct{}, len(scope.BranchIDs))
	for _, id := range scope.BranchIDs {
		if id == uuid.Nil {
			return fmt.Errorf("%w: branch IDs must be non-zero", ErrInvalidAuditReadScope)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%w: duplicate branch ID", ErrInvalidAuditReadScope)
		}
		seen[id] = struct{}{}
	}
	if !scope.AllBranches && len(scope.BranchIDs) == 0 && !scope.IncludeUnassignedBranch {
		return fmt.Errorf("%w: branch-restricted scope must enumerate branches", ErrInvalidAuditReadScope)
	}
	return nil
}

func auditCursorScopeKey(scope AuditReadScope, filters AuditSearchFilters) (string, error) {
	if err := validateAuditReadScope(scope); err != nil {
		return "", err
	}
	from, to := filters.TimeRange()
	if from.IsZero() || to.IsZero() {
		return "", fmt.Errorf("%w: validated time range is required", ErrInvalidAuditRequest)
	}
	type canonicalScope struct {
		OrganizationID          *uuid.UUID  `json:"org,omitempty"`
		PlatformWide            bool        `json:"platform"`
		PlatformEventsOnly      bool        `json:"platform_events_only"`
		AllBranches             bool        `json:"all_branches"`
		BranchIDs               []uuid.UUID `json:"branches,omitempty"`
		IncludeUnassignedBranch bool        `json:"include_unassigned"`
	}
	type canonicalFilters struct {
		UserID     *uuid.UUID   `json:"user,omitempty"`
		Module     string       `json:"module,omitempty"`
		Action     string       `json:"action,omitempty"`
		EntityType string       `json:"entity_type,omitempty"`
		EntityID   *uuid.UUID   `json:"entity,omitempty"`
		Outcome    AuditOutcome `json:"outcome,omitempty"`
		From       time.Time    `json:"from"`
		To         time.Time    `json:"to"`
		IP         string       `json:"ip,omitempty"`
		Q          string       `json:"q,omitempty"`
	}
	branchIDs := append([]uuid.UUID(nil), scope.BranchIDs...)
	sort.Slice(branchIDs, func(i, j int) bool { return branchIDs[i].String() < branchIDs[j].String() })
	body, err := json.Marshal(struct {
		Version int              `json:"v"`
		Scope   canonicalScope   `json:"scope"`
		Filters canonicalFilters `json:"filters"`
	}{
		Version: 1,
		Scope: canonicalScope{
			OrganizationID: cloneUUID(scope.OrganizationID),
			PlatformWide:   scope.PlatformWide, PlatformEventsOnly: scope.PlatformEventsOnly,
			AllBranches: scope.AllBranches,
			BranchIDs:   branchIDs, IncludeUnassignedBranch: scope.IncludeUnassignedBranch,
		},
		Filters: canonicalFilters{
			UserID: filters.ParsedUserID(), Module: filters.Module, Action: filters.Action,
			EntityType: filters.EntityType, EntityID: filters.ParsedEntityID(),
			Outcome: AuditOutcome(filters.Outcome), From: from.UTC(), To: to.UTC(),
			IP: filters.IP, Q: filters.Q,
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal audit cursor binding: %w", err)
	}
	sum := sha256.Sum256(body)
	return "audit:v1:" + hex.EncodeToString(sum[:]), nil
}

func validateAuditTimeRange(from, to time.Time) error {
	if from.IsZero() || to.IsZero() || !from.Before(to) || to.Sub(from) > maxAuditRange {
		return fmt.Errorf("%w: valid time range of at most 90 days is required", ErrInvalidAuditRequest)
	}
	return nil
}

func likeAuditLiteral(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return "%" + value + "%"
}

func scanAuditListItem(row interface{ Scan(...any) error }, item *AuditLogListItem) error {
	return row.Scan(
		&item.ID, &item.OccurredAt, &item.OrganizationID, &item.BranchID, &item.UserID,
		&item.RoleName, &item.SessionID, &item.RequestID, &item.Module, &item.Action,
		&item.EntityType, &item.EntityID, &item.Outcome, &item.ErrorCode, &item.DurationMS,
	)
}

func scanAuditLog(row pgx.Row) (AuditLog, error) {
	var event AuditLog
	err := row.Scan(
		&event.ID, &event.OccurredAt, &event.OrganizationID, &event.BranchID, &event.UserID,
		&event.RoleName, &event.SessionID, &event.RequestID, &event.Module, &event.Action,
		&event.EntityType, &event.EntityID, &event.IP, &event.Browser, &event.OS,
		&event.Device, &event.Location, &event.UserAgent, &event.Details,
		&event.BeforeData, &event.AfterData, &event.Outcome, &event.ErrorCode,
		&event.DurationMS,
	)
	return event, err
}
