package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

type Repository struct {
	db *db.DB
}

func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

type listRow struct {
	Definition SettingDefinition
	Value      json.RawMessage
	Source     SettingValueSource
	Version    int64
}

const definitionListColumns = `d.key, d.value_type, d.schema_version, d.allowed_scopes,
	d.description, d.is_sensitive, d.secret_reference_only, d.validator_key, d.status`

func scanListRow(row interface{ Scan(...any) error }) (listRow, error) {
	var result listRow
	if err := row.Scan(
		&result.Definition.Key, &result.Definition.ValueType,
		&result.Definition.SchemaVersion, &result.Definition.AllowedScopes,
		&result.Definition.Description, &result.Definition.IsSensitive,
		&result.Definition.SecretReferenceOnly, &result.Definition.ValidatorKey,
		&result.Definition.Status, &result.Value, &result.Source, &result.Version,
	); err != nil {
		return listRow{}, err
	}
	return result, nil
}

func (r *Repository) listQuery(mode SettingReadMode) string {
	var valueExpr, sourceExpr, versionExpr string
	switch mode {
	case SettingReadModeSystem:
		valueExpr = `sv.value`
		sourceExpr = `CASE WHEN sv.key IS NULL THEN NULL ELSE 'system' END`
		versionExpr = `COALESCE(sv.version, 0)`
	case SettingReadModeOrganization:
		valueExpr = `ov.value`
		sourceExpr = `CASE WHEN ov.key IS NULL THEN NULL ELSE 'organization' END`
		versionExpr = `COALESCE(ov.version, 0)`
	default:
		valueExpr = `COALESCE(ov.value, sv.value, d.default_value)`
		sourceExpr = `CASE WHEN ov.key IS NOT NULL THEN 'organization'
			WHEN sv.key IS NOT NULL THEN 'system'
			WHEN d.default_value IS NOT NULL THEN 'default' END`
		versionExpr = `CASE WHEN ov.key IS NOT NULL THEN ov.version
			WHEN sv.key IS NOT NULL THEN sv.version ELSE 0 END`
	}
	return `SELECT ` + definitionListColumns + `,
			` + valueExpr + ` AS resolved_value,
			` + sourceExpr + ` AS resolved_source,
			` + versionExpr + ` AS resolved_version
		FROM setting_definitions d
		LEFT JOIN system_setting_values sv ON sv.key = d.key AND sv.deleted_at IS NULL
		LEFT JOIN organization_setting_values ov
			ON ov.key = d.key AND ov.organization_id = $1 AND ov.deleted_at IS NULL
		WHERE d.status = 'active' AND d.deleted_at IS NULL`
}

func (r *Repository) ListResolved(ctx context.Context, orgID uuid.UUID, mode SettingReadMode, q string, page pagination.Offset) ([]listRow, int64, error) {
	base := r.listQuery(mode)
	args := []any{orgID}
	where := ""
	if q != "" {
		args = append(args, likePattern(q))
		where = ` AND (d.key ILIKE $2 ESCAPE '\' OR COALESCE(d.description, '') ILIKE $2 ESCAPE '\')`
	}

	var total int64
	countSQL := `SELECT count(*) FROM (` + base + where + `) AS resolved
		WHERE resolved_value IS NOT NULL`
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []listRow{}, 0, nil
	}

	sortSQL := `key ASC`
	if _, desc := pagination.SortDirection(page.Sort); desc && strings.TrimPrefix(page.Sort, "-") == "key" {
		sortSQL = `key DESC`
	}
	query := `SELECT * FROM (` + base + where + `) AS resolved
		WHERE resolved_value IS NOT NULL ORDER BY ` + sortSQL
	args = append(args, page.Limit, page.OffsetSQL())
	query += ` LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]listRow, 0, page.Limit)
	for rows.Next() {
		item, err := scanListRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func valueProjection(mode SettingReadMode) string {
	switch mode {
	case SettingReadModeSystem:
		return `sv.value`
	case SettingReadModeOrganization:
		return `ov.value`
	default:
		return `COALESCE(ov.value, sv.value, d.default_value)`
	}
}

func likePattern(q string) string {
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return "%" + q + "%"
}

func (r *Repository) FindResolved(ctx context.Context, orgID uuid.UUID, key string, mode SettingReadMode) (listRow, error) {
	query := `SELECT * FROM (` + r.listQuery(mode) + `) AS resolved
		WHERE key = $2 AND resolved_value IS NOT NULL`
	return scanListRow(r.db.ReadFromCtx(ctx).QueryRow(ctx, query, orgID, key))
}

func (r *Repository) FindDefinition(ctx context.Context, key string) (*SettingDefinition, error) {
	const query = `SELECT id, key, value_type, schema_version, default_value,
		allowed_scopes, description, is_sensitive, secret_reference_only,
		validator_key, status, created_at, updated_at, deleted_at
		FROM setting_definitions WHERE key = $1 AND deleted_at IS NULL`
	var d SettingDefinition
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, query, key).Scan(
		&d.ID, &d.Key, &d.ValueType, &d.SchemaVersion, &d.DefaultValue,
		&d.AllowedScopes, &d.Description, &d.IsSensitive, &d.SecretReferenceOnly,
		&d.ValidatorKey, &d.Status, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *Repository) FindValueForUpdate(ctx context.Context, scope SettingScope, orgID uuid.UUID, key string) (json.RawMessage, int64, error) {
	var query string
	var args []any
	if scope == SettingScopeSystem {
		query = `SELECT value, version FROM system_setting_values
			WHERE key = $1 AND deleted_at IS NULL FOR UPDATE`
		args = []any{key}
	} else {
		query = `SELECT value, version FROM organization_setting_values
			WHERE organization_id = $1 AND key = $2 AND deleted_at IS NULL FOR UPDATE`
		args = []any{orgID, key}
	}
	var value []byte
	var version int64
	err := r.db.FromCtx(ctx).QueryRow(ctx, query, args...).Scan(&value, &version)
	return json.RawMessage(value), version, err
}

func (r *Repository) InsertValue(ctx context.Context, scope SettingScope, orgID, actorID uuid.UUID, key string, value json.RawMessage) (int64, error) {
	const systemQuery = `INSERT INTO system_setting_values (key, value, version, updated_by)
		VALUES ($1, $2, 1, $3) RETURNING version`
	const orgQuery = `INSERT INTO organization_setting_values
		(organization_id, key, value, version, updated_by)
		VALUES ($1, $2, $3, 1, $4) RETURNING version`
	var version int64
	var err error
	if scope == SettingScopeSystem {
		err = r.db.FromCtx(ctx).QueryRow(ctx, systemQuery, key, value, actorID).Scan(&version)
	} else {
		err = r.db.FromCtx(ctx).QueryRow(ctx, orgQuery, orgID, key, value, actorID).Scan(&version)
	}
	return version, err
}

func (r *Repository) UpdateValue(ctx context.Context, scope SettingScope, orgID, actorID uuid.UUID, key string, value json.RawMessage, currentVersion int64) (int64, error) {
	const systemQuery = `UPDATE system_setting_values SET value = $2, version = version + 1, updated_by = $3
		WHERE key = $1 AND version = $4 AND deleted_at IS NULL RETURNING version`
	const orgQuery = `UPDATE organization_setting_values SET value = $3, version = version + 1, updated_by = $4
		WHERE organization_id = $1 AND key = $2 AND version = $5 AND deleted_at IS NULL RETURNING version`
	var version int64
	var err error
	if scope == SettingScopeSystem {
		err = r.db.FromCtx(ctx).QueryRow(ctx, systemQuery, key, value, actorID, currentVersion).Scan(&version)
	} else {
		err = r.db.FromCtx(ctx).QueryRow(ctx, orgQuery, orgID, key, value, actorID, currentVersion).Scan(&version)
	}
	return version, err
}

func (r *Repository) InsertRevision(ctx context.Context, revision SettingRevision) error {
	const query = `INSERT INTO setting_revisions
		(key, scope, organization_id, version, schema_version, actor_id, change_reason,
		 correlation_id, previous_value, new_value, values_redacted)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	_, err := r.db.FromCtx(ctx).Exec(ctx, query, revision.Key, revision.Scope,
		revision.OrganizationID, revision.Version, revision.SchemaVersion,
		revision.ActorID, revision.ChangeReason, revision.CorrelationID,
		revision.PreviousValue, revision.NewValue, revision.ValuesRedacted)
	return err
}

func IsNoRows(err error) bool {
	return errors.Is(err, db.ErrNoRows)
}
