package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
)

// AuditReader is the read-only persistence boundary consumed by Service.
// Repositories must apply the supplied scope in every query.
type AuditReader interface {
	List(context.Context, AuditReadScope, *ListAuditLogsQuery) (AuditLogListResponse, error)
	GetByID(context.Context, AuditReadScope, uuid.UUID, time.Time) (AuditLog, error)
	ListModules(context.Context, AuditReadScope, time.Time, time.Time, int) ([]string, error)
	ListActions(context.Context, AuditReadScope, time.Time, time.Time, int) ([]string, error)
}

// Service applies authentication, permissions, tenant/branch scope, validation,
// privacy redaction and safe error mapping before returning audit evidence.
// No data cache is used: audit visibility must follow the current principal and
// database state, not a stale or accidentally cross-tenant cache entry.
type Service struct {
	repo    AuditReader
	metrics *telemetry.Metrics
}

// NewService assembles the audit read service.
func NewService(repo AuditReader, metrics ...*telemetry.Metrics) *Service {
	service := &Service{repo: repo}
	if len(metrics) > 0 {
		service.metrics = metrics[0]
	}
	return service
}

// List returns a validated, authorized page from the caller's effective
// organization and branch scope.
func (s *Service) List(ctx context.Context, query *ListAuditLogsQuery) (result AuditLogListResponse, retErr error) {
	defer func() { s.observeRead("list", retErr) }()
	if err := requireAuditPermission(ctx, "view"); err != nil {
		return AuditLogListResponse{}, err
	}
	if query == nil {
		return AuditLogListResponse{}, errs.Validation("invalid audit query", errs.FieldError{
			Field: "query", Rule: "required", Message: "query is required",
		})
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return AuditLogListResponse{}, auditValidationError(validation)
	}
	scope, err := organizationAuditScope(ctx)
	if err != nil {
		return AuditLogListResponse{}, err
	}
	if s == nil || s.repo == nil {
		return AuditLogListResponse{}, errs.DatabaseError(fmt.Errorf("audit repository is not configured"))
	}
	response, err := s.repo.List(ctx, scope, query)
	if err != nil {
		return AuditLogListResponse{}, mapAuditReadError(err)
	}
	if response.Items == nil {
		response.Items = []AuditLogListItem{}
	}
	return response, nil
}

// Get returns one event in the caller's effective tenant/branch scope. The
// occurrence timestamp is required to prune the time partition and disambiguate
// IDs. A detail request must never reveal whether an inaccessible event exists.
func (s *Service) Get(ctx context.Context, id uuid.UUID, query *AuditEventLookupQuery) (result AuditLogDetailResponse, retErr error) {
	defer func() { s.observeRead("detail", retErr) }()
	if err := requireAuditPermission(ctx, "view"); err != nil {
		return AuditLogDetailResponse{}, err
	}
	if id == uuid.Nil {
		return AuditLogDetailResponse{}, errs.Validation("invalid audit event ID", errs.FieldError{
			Field: "id", Rule: "uuid", Message: "must be a valid non-zero UUID",
		})
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return AuditLogDetailResponse{}, auditValidationError(validation)
	}
	scope, err := organizationAuditScope(ctx)
	if err != nil {
		return AuditLogDetailResponse{}, err
	}
	if s == nil || s.repo == nil {
		return AuditLogDetailResponse{}, errs.DatabaseError(fmt.Errorf("audit repository is not configured"))
	}
	event, err := s.repo.GetByID(ctx, scope, id, query.PartitionKey())
	if err != nil {
		return AuditLogDetailResponse{}, mapAuditReadError(err)
	}
	return detailResponse(event)
}

// ListModules returns only module values represented by visible events in the
// caller's current scope and validated time range.
func (s *Service) ListModules(ctx context.Context, query *AuditValuesQuery) (AuditValuesResponse, error) {
	return s.listValues(ctx, query, true)
}

// ListActions returns only action values represented by visible events in the
// caller's current scope and validated time range.
func (s *Service) ListActions(ctx context.Context, query *AuditValuesQuery) (AuditValuesResponse, error) {
	return s.listValues(ctx, query, false)
}

func (s *Service) listValues(ctx context.Context, query *AuditValuesQuery, modules bool) (result AuditValuesResponse, retErr error) {
	operation := "list_actions"
	if modules {
		operation = "list_modules"
	}
	defer func() { s.observeRead(operation, retErr) }()
	if err := requireAuditPermission(ctx, "view"); err != nil {
		return AuditValuesResponse{}, err
	}
	if query == nil {
		return AuditValuesResponse{}, errs.Validation("invalid audit query", errs.FieldError{
			Field: "query", Rule: "required", Message: "query is required",
		})
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return AuditValuesResponse{}, auditValidationError(validation)
	}
	scope, err := organizationAuditScope(ctx)
	if err != nil {
		return AuditValuesResponse{}, err
	}
	if s == nil || s.repo == nil {
		return AuditValuesResponse{}, errs.DatabaseError(fmt.Errorf("audit repository is not configured"))
	}
	from, to := query.TimeRange()
	var values []string
	if modules {
		values, err = s.repo.ListModules(ctx, scope, from, to, query.Limit)
	} else {
		values, err = s.repo.ListActions(ctx, scope, from, to, query.Limit)
	}
	if err != nil {
		return AuditValuesResponse{}, mapAuditReadError(err)
	}
	if values == nil {
		values = []string{}
	}
	return AuditValuesResponse{Items: values}, nil
}

// ListPlatformEvents is a separate, explicit path for platform-level events
// without organization ownership. It is restricted to SUPER_ADMIN plus the
// audit:view permission and cannot be used to read tenant events.
func (s *Service) ListPlatformEvents(ctx context.Context, query *ListAuditLogsQuery) (result AuditLogListResponse, retErr error) {
	defer func() { s.observeRead("list_platform", retErr) }()
	if err := requirePlatformAuditPermission(ctx); err != nil {
		return AuditLogListResponse{}, err
	}
	if query == nil {
		return AuditLogListResponse{}, errs.Validation("invalid audit query", errs.FieldError{
			Field: "query", Rule: "required", Message: "query is required",
		})
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return AuditLogListResponse{}, auditValidationError(validation)
	}
	if s == nil || s.repo == nil {
		return AuditLogListResponse{}, errs.DatabaseError(fmt.Errorf("audit repository is not configured"))
	}
	response, err := s.repo.List(ctx, AuditReadScope{PlatformEventsOnly: true}, query)
	if err != nil {
		return AuditLogListResponse{}, mapAuditReadError(err)
	}
	if response.Items == nil {
		response.Items = []AuditLogListItem{}
	}
	return response, nil
}

// GetPlatformEvent reads only an event whose organization_id is NULL. Tenant
// events are never included in this privileged platform-only path.
func (s *Service) GetPlatformEvent(ctx context.Context, id uuid.UUID, query *AuditEventLookupQuery) (result AuditLogDetailResponse, retErr error) {
	defer func() { s.observeRead("detail_platform", retErr) }()
	if err := requirePlatformAuditPermission(ctx); err != nil {
		return AuditLogDetailResponse{}, err
	}
	if id == uuid.Nil {
		return AuditLogDetailResponse{}, errs.Validation("invalid audit event ID", errs.FieldError{
			Field: "id", Rule: "uuid", Message: "must be a valid non-zero UUID",
		})
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return AuditLogDetailResponse{}, auditValidationError(validation)
	}
	if s == nil || s.repo == nil {
		return AuditLogDetailResponse{}, errs.DatabaseError(fmt.Errorf("audit repository is not configured"))
	}
	event, err := s.repo.GetByID(ctx, AuditReadScope{PlatformEventsOnly: true}, id, query.PartitionKey())
	if err != nil {
		return AuditLogDetailResponse{}, mapAuditReadError(err)
	}
	return detailResponse(event)
}

func (s *Service) observeRead(operation string, err error) {
	if s == nil || s.metrics == nil {
		return
	}
	outcome := "success"
	if err != nil {
		switch status := errs.HTTPStatus(err); status {
		case 400, 422:
			outcome = "invalid"
		case 401, 403:
			outcome = "denied"
		case 404:
			outcome = "not_found"
		default:
			outcome = "error"
		}
	}
	s.metrics.ObserveAuditRead(operation, outcome)
}

func organizationAuditScope(ctx context.Context) (AuditReadScope, error) {
	if !appctx.IsAuthenticated(ctx) {
		return AuditReadScope{}, errs.Unauthenticated()
	}
	orgID := appctx.OrgID(ctx)
	if orgID == uuid.Nil {
		return AuditReadScope{}, errs.Forbidden("an active organization is required")
	}
	branchIDs := appctx.BranchIDs(ctx)
	if len(branchIDs) > 0 {
		return AuditReadScope{
			OrganizationID: &orgID,
			BranchIDs:      branchIDs,
		}, nil
	}
	switch appctx.RoleName(ctx) {
	case constants.RoleSuperAdmin, constants.RoleAdmin, constants.RoleAccountant, constants.RoleAuditor:
	default:
		return AuditReadScope{}, errs.Forbidden("a valid branch scope is required")
	}
	return AuditReadScope{
		OrganizationID:          &orgID,
		AllBranches:             true,
		IncludeUnassignedBranch: true,
	}, nil
}

func requireAuditPermission(ctx context.Context, action string) error {
	if !appctx.IsAuthenticated(ctx) {
		return errs.Unauthenticated()
	}
	if !appctx.HasPermission(ctx, "audit", action) {
		return errs.Forbidden("you do not have permission to view audit records")
	}
	return nil
}

func requirePlatformAuditPermission(ctx context.Context) error {
	if err := requireAuditPermission(ctx, "view"); err != nil {
		return err
	}
	if !appctx.IsSuperAdmin(ctx) {
		return errs.Forbidden("platform audit records require platform administrator access")
	}
	return nil
}

func auditValidationError(validation []error) error {
	details := make([]errs.FieldError, 0, len(validation))
	for _, err := range validation {
		field, rule, message, ok := AuditValidationErrorFields(err)
		if !ok {
			details = append(details, errs.FieldError{
				Field: "query", Rule: "invalid", Message: "contains an invalid value",
			})
			continue
		}
		details = append(details, errs.FieldError{Field: field, Rule: rule, Message: message})
	}
	return errs.Validation("audit query validation failed", details...)
}

func mapAuditReadError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, db.ErrNoRows) {
		return errs.NotFound("audit event")
	}
	if errors.Is(err, ErrInvalidAuditReadScope) {
		return errs.Forbidden("audit read scope is not permitted")
	}
	if errors.Is(err, ErrInvalidAuditCursor) {
		return errs.Validation("invalid audit cursor", errs.FieldError{
			Field: "cursor", Rule: "signature", Message: "cursor is invalid or no longer matches this query",
		})
	}
	if errors.Is(err, ErrInvalidAuditRequest) {
		return errs.Validation("invalid audit query")
	}
	return errs.DatabaseError(err)
}

func detailResponse(event AuditLog) (AuditLogDetailResponse, error) {
	payload, err := redactedAuditPayload(event)
	if err != nil {
		return AuditLogDetailResponse{}, errs.DatabaseError(fmt.Errorf("prepare redacted audit payload: %w", err))
	}
	response, err := ToAuditLogDetailResponse(event, payload)
	if err != nil {
		return AuditLogDetailResponse{}, errs.DatabaseError(fmt.Errorf("validate redacted audit payload: %w", err))
	}
	return response, nil
}

func redactedAuditPayload(event AuditLog) (RedactedAuditPayload, error) {
	details, err := marshalRedactedAuditMap(event.Details, event.Module == "settings")
	if err != nil {
		return RedactedAuditPayload{}, err
	}
	before, err := marshalRedactedAuditMap(event.BeforeData, event.Module == "settings")
	if err != nil {
		return RedactedAuditPayload{}, err
	}
	after, err := marshalRedactedAuditMap(event.AfterData, event.Module == "settings")
	if err != nil {
		return RedactedAuditPayload{}, err
	}
	return RedactedAuditPayload{Details: details, BeforeData: before, AfterData: after}, nil
}

func marshalRedactedAuditMap(value map[string]any, redactSettingValues bool) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	redacted := redactAuditValue(value, redactSettingValues)
	raw, err := json.Marshal(redacted)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxAuditPayloadBytes {
		return json.RawMessage(`{"_truncated":true}`), nil
	}
	return raw, nil
}

const auditRedactionMarker = "***REDACTED***"

var auditSensitiveExactKeys = map[string]struct{}{
	"pin": {}, "cvv": {}, "cvc": {}, "cvv2": {}, "otp": {},
	"ssn": {}, "mfa": {}, "totp": {}, "code": {},
	"cookie": {}, "set-cookie": {},
}

var auditSensitiveKeyFragments = []string{
	"password", "passwd", "secret", "token", "authorization", "api_key",
	"apikey", "private_key", "client_secret", "credential", "card",
	"refresh", "access_token", "session",
}

func redactAuditValue(value any, redactSettingValues bool) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if isSensitiveAuditKey(key) || (redactSettingValues && strings.EqualFold(strings.TrimSpace(key), "value")) {
				out[key] = auditRedactionMarker
				continue
			}
			out[key] = redactAuditValue(nested, redactSettingValues)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, nested := range typed {
			out[i] = redactAuditValue(nested, redactSettingValues)
		}
		return out
	default:
		return value
	}
}

func isSensitiveAuditKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if _, ok := auditSensitiveExactKeys[key]; ok {
		return true
	}
	for _, fragment := range auditSensitiveKeyFragments {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}
