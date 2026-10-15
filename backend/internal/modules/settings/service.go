package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

var settingKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,119}$`)

type Service struct {
	repo *Repository
	db   *db.DB
}

func NewService(repo *Repository, database *db.DB) *Service {
	return &Service{repo: repo, db: database}
}

func (s *Service) List(ctx context.Context, query *ListSettingsQuery) ([]SettingResponse, pagination.Meta, error) {
	if query == nil {
		return nil, pagination.Meta{}, errs.Validation("invalid settings query")
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return nil, pagination.Meta{}, validationError(validation)
	}
	if query.Scope == SettingReadModeSystem && !appctx.IsSuperAdmin(ctx) {
		return nil, pagination.Meta{}, errs.Forbidden("system settings require platform administrator access")
	}
	if query.IncludeSensitive != nil && *query.IncludeSensitive && !appctx.IsSuperAdmin(ctx) {
		return nil, pagination.Meta{}, errs.Forbidden("sensitive settings require platform administrator access")
	}

	orgID, err := organizationID(ctx)
	if err != nil {
		return nil, pagination.Meta{}, err
	}
	rows, total, err := s.repo.ListResolved(ctx, orgID, query.Scope, query.Q, query.Offset)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}

	includeSensitive := query.IncludeSensitive != nil && *query.IncludeSensitive && appctx.IsSuperAdmin(ctx)
	items := make([]SettingResponse, 0, len(rows))
	for _, row := range rows {
		value := discloseValue(row.Value, row.Definition.IsSensitive, includeSensitive)
		effectiveScope := scopeForReadMode(query.Scope)
		items = append(items, ToSettingResponse(
			row.Definition.Key, value, row.Definition.ValueType,
			effectiveScope, row.Source, row.Version, row.Definition.SchemaVersion,
			row.Definition.IsSensitive, row.Definition.Description,
			row.Definition.SecretReferenceOnly,
		))
	}
	return items, pagination.BuildOffsetMeta(query.Offset, total), nil
}

func (s *Service) Get(ctx context.Context, key string, query GetSettingQuery) (SettingResponse, error) {
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return SettingResponse{}, validationError(validation)
	}
	if !validSettingKey(key) {
		return SettingResponse{}, errs.NotFound("setting")
	}
	if query.Scope == SettingReadModeSystem && !appctx.IsSuperAdmin(ctx) {
		return SettingResponse{}, errs.Forbidden("system settings require platform administrator access")
	}
	if query.IncludeSensitive != nil && *query.IncludeSensitive && !appctx.IsSuperAdmin(ctx) {
		return SettingResponse{}, errs.Forbidden("sensitive settings require platform administrator access")
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return SettingResponse{}, err
	}
	row, err := s.repo.FindResolved(ctx, orgID, key, query.Scope)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return SettingResponse{}, errs.NotFound("setting")
		}
		return SettingResponse{}, errs.DatabaseError(err)
	}
	includeSensitive := query.IncludeSensitive != nil && *query.IncludeSensitive && appctx.IsSuperAdmin(ctx)
	return ToSettingResponse(
		row.Definition.Key,
		discloseValue(row.Value, row.Definition.IsSensitive, includeSensitive),
		row.Definition.ValueType,
		scopeForReadMode(query.Scope), row.Source, row.Version,
		row.Definition.SchemaVersion, row.Definition.IsSensitive,
		row.Definition.Description, row.Definition.SecretReferenceOnly,
	), nil
}

func (s *Service) Update(ctx context.Context, key string, request *UpdateSettingRequest) (BulkUpdateResult, error) {
	if !validSettingKey(key) {
		return BulkUpdateResult{}, errs.Validation("setting key is invalid",
			errs.FieldError{Field: "key", Rule: "pattern", Message: "must be a lowercase dotted identifier"})
	}
	if request == nil {
		return BulkUpdateResult{}, errs.Validation("invalid setting update")
	}
	if validation := request.Validate(); len(validation) > 0 {
		return BulkUpdateResult{}, validationError(validation)
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return BulkUpdateResult{}, err
	}
	var result BulkUpdateResult
	err = s.db.WithTx(ctx, func(txCtx context.Context) error {
		var mutateErr error
		result, mutateErr = s.mutate(
			txCtx, orgID, request.Scope, key, request.Value,
			request.ExpectedVersion, request.Reason,
		)
		return mutateErr
	})
	if err != nil {
		return BulkUpdateResult{}, err
	}
	return result, nil
}

func (s *Service) BulkUpdate(ctx context.Context, request *BulkUpdateSettingsRequest) (BulkUpdateSettingsResponse, error) {
	if request == nil {
		return BulkUpdateSettingsResponse{}, errs.Validation("invalid bulk settings update")
	}
	if validation := request.Validate(); len(validation) > 0 {
		return BulkUpdateSettingsResponse{}, validationError(validation)
	}
	orgID, err := organizationID(ctx)
	if err != nil {
		return BulkUpdateSettingsResponse{}, err
	}

	order := make([]int, len(request.Items))
	for i := range request.Items {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return request.Items[order[i]].Key < request.Items[order[j]].Key
	})
	results := make([]BulkUpdateResult, len(request.Items))
	err = s.db.WithTx(ctx, func(txCtx context.Context) error {
		for _, index := range order {
			item := request.Items[index]
			result, err := s.mutate(txCtx, orgID, request.Scope, item.Key, item.Value, item.ExpectedVersion, item.Reason)
			if err != nil {
				return err
			}
			results[index] = result
		}
		return nil
	})
	if err != nil {
		return BulkUpdateSettingsResponse{}, err
	}
	return BulkUpdateSettingsResponse{Updated: results}, nil
}

func (s *Service) mutate(ctx context.Context, orgID uuid.UUID, scope SettingScope, key string, value json.RawMessage, expected *int64, reason *string) (BulkUpdateResult, error) {
	if scope != SettingScopeSystem && scope != SettingScopeOrganization {
		return BulkUpdateResult{}, errs.Validation("invalid setting scope",
			errs.FieldError{Field: "scope", Rule: "oneof", Message: "must be system or organization"})
	}
	if scope == SettingScopeSystem && !appctx.IsSuperAdmin(ctx) {
		return BulkUpdateResult{}, errs.Forbidden("system settings require platform administrator access")
	}
	if scope == SettingScopeOrganization && orgID == uuid.Nil {
		return BulkUpdateResult{}, errs.Forbidden("an active organization is required")
	}
	if expected == nil || *expected < 0 {
		return BulkUpdateResult{}, errs.Validation("invalid expected version",
			errs.FieldError{Field: "expected_version", Rule: "required", Message: "a nonnegative expected_version is required"})
	}

	definition, err := s.repo.FindDefinition(ctx, key)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return BulkUpdateResult{}, errs.NotFound("setting")
		}
		return BulkUpdateResult{}, errs.DatabaseError(err)
	}
	if definition.Status != DefinitionStatusActive {
		return BulkUpdateResult{}, errs.Conflict("setting definition is deprecated")
	}
	if !containsScope(definition.AllowedScopes, scope) {
		return BulkUpdateResult{}, errs.Forbidden("setting definition does not allow the requested scope")
	}
	if err := validateDefinitionValue(*definition, value); err != nil {
		return BulkUpdateResult{}, err
	}
	if definition.IsSensitive && (reason == nil || strings.TrimSpace(*reason) == "") {
		return BulkUpdateResult{}, errs.Validation("a change reason is required for sensitive settings",
			errs.FieldError{Field: "reason", Rule: "required", Message: "this field is required"})
	}

	previous, currentVersion, lookupErr := s.repo.FindValueForUpdate(ctx, scope, orgID, key)
	exists := lookupErr == nil
	if lookupErr != nil && !errors.Is(lookupErr, db.ErrNoRows) {
		return BulkUpdateResult{}, errs.DatabaseError(lookupErr)
	}
	if (!exists && *expected != 0) || (exists && currentVersion != *expected) {
		return BulkUpdateResult{}, errs.Conflict("setting version is stale")
	}

	actorID := appctx.UserID(ctx)
	if actorID == uuid.Nil {
		return BulkUpdateResult{}, errs.Unauthenticated()
	}
	var version int64
	if exists {
		version, err = s.repo.UpdateValue(ctx, scope, orgID, actorID, key, value, currentVersion)
	} else {
		version, err = s.repo.InsertValue(ctx, scope, orgID, actorID, key, value)
	}
	if err != nil {
		if db.IsUniqueVilation(err) {
			return BulkUpdateResult{}, errs.Conflict("setting was concurrently created or updated")
		}
		return BulkUpdateResult{}, errs.DatabaseError(err)
	}

	var revisionOrgID *uuid.UUID
	if scope == SettingScopeOrganization {
		revisionOrgID = &orgID
	}
	correlation := appctx.RequestID(ctx)
	var correlationID *string
	if correlation != "" {
		correlationID = &correlation
	}
	revision := SettingRevision{
		Key:            key,
		Scope:          scope,
		OrganizationID: revisionOrgID,
		Version:        version,
		SchemaVersion:  definition.SchemaVersion,
		ActorID:        &actorID,
		ChangeReason:   reason,
		CorrelationID:  correlationID,
		ValuesRedacted: definition.IsSensitive,
	}
	if !definition.IsSensitive {
		if exists {
			revision.PreviousValue = previous
		}
		revision.NewValue = value
	}
	if err := s.repo.InsertRevision(ctx, revision); err != nil {
		return BulkUpdateResult{}, errs.DatabaseError(err)
	}
	return ToBulkUpdateResult(key, version, scope), nil
}

func validateDefinitionValue(definition SettingDefinition, raw json.RawMessage) error {
	if len(raw) > maxSettingValueBytes {
		return errs.Validation("setting value exceeds the maximum size",
			errs.FieldError{Field: "value", Rule: "max_size", Message: "exceeds maximum allowed size"})
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return errs.Validation("setting value is invalid JSON",
			errs.FieldError{Field: "value", Rule: "json", Message: "must be one JSON value"})
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errs.Validation("setting value is invalid JSON",
			errs.FieldError{Field: "value", Rule: "json", Message: "must be one JSON value"})
	}

	valid := false
	switch definition.ValueType {
	case SettingValueTypeString:
		_, valid = value.(string)
	case SettingValueTypeBoolean:
		_, valid = value.(bool)
	case SettingValueTypeInteger:
		number, ok := value.(json.Number)
		if ok {
			_, err := strconv.ParseInt(string(number), 10, 64)
			valid = err == nil
		}
	case SettingValueTypeFloat:
		number, ok := value.(json.Number)
		if ok {
			parsed, err := strconv.ParseFloat(string(number), 64)
			valid = err == nil && !math.IsInf(parsed, 0) && !math.IsNaN(parsed)
		}
	case SettingValueTypeJSON:
		valid = true
	default:
		return errs.DatabaseError(fmt.Errorf("setting %q has unsupported value type %q", definition.Key, definition.ValueType))
	}
	if !valid {
		return errs.Validation("setting value does not match its definition",
			errs.FieldError{Field: "value", Rule: string(definition.ValueType), Message: "does not match the registered setting type"})
	}

	if definition.SecretReferenceOnly {
		reference, ok := value.(string)
		if !ok || !validSecretReference(reference) {
			return errs.Validation("setting value must be an approved secret reference",
				errs.FieldError{Field: "value", Rule: "secret_reference", Message: "must use the secret:// reference format"})
		}
	}
	if definition.ValidatorKey != nil && strings.TrimSpace(*definition.ValidatorKey) != "" {
		return errs.DatabaseError(fmt.Errorf("setting %q references unregistered validator %q", definition.Key, *definition.ValidatorKey))
	}
	return nil
}

func validSecretReference(reference string) bool {
	parsed, err := url.Parse(reference)
	return err == nil &&
		parsed.Scheme == "secret" &&
		parsed.Host != "" &&
		parsed.User == nil &&
		parsed.RawQuery == "" &&
		parsed.Fragment == "" &&
		!strings.Contains(reference, " ")
}

func discloseValue(value json.RawMessage, sensitive, includeSensitive bool) json.RawMessage {
	if sensitive && !includeSensitive {
		return RedactSensitiveValue()
	}
	return append(json.RawMessage(nil), value...)
}

func scopeForReadMode(mode SettingReadMode) SettingScope {
	if mode == SettingReadModeSystem {
		return SettingScopeSystem
	}
	if mode == SettingReadModeOrganization {
		return SettingScopeOrganization
	}
	return SettingScopeOrganization
}

func organizationID(ctx context.Context) (uuid.UUID, error) {
	id := appctx.OrgID(ctx)
	if id == uuid.Nil {
		return uuid.Nil, errs.Forbidden("an active organization is required")
	}
	return id, nil
}

func validSettingKey(key string) bool {
	return settingKeyPattern.MatchString(key)
}

func containsScope(scopes []string, scope SettingScope) bool {
	for _, allowed := range scopes {
		if allowed == string(scope) {
			return true
		}
	}
	return false
}

func validationError(validation []error) error {
	details := make([]errs.FieldError, 0, len(validation))
	for _, err := range validation {
		var fieldErr *FieldError
		if errors.As(err, &fieldErr) {
			details = append(details, errs.FieldError{
				Field: fieldErr.Field, Rule: fieldErr.Rule,
				Message: fieldErr.Message, Value: fieldErr.Value,
			})
			continue
		}
		details = append(details, errs.FieldError{Field: "request", Rule: "invalid", Message: err.Error()})
	}
	return errs.Validation("request validation failed", details...)
}
