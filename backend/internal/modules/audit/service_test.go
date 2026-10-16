package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	"backend/internal/platform/db"
)

type auditReaderStub struct {
	listScope   AuditReadScope
	getScope    AuditReadScope
	getID       uuid.UUID
	getOccurred time.Time
	moduleScope AuditReadScope
	actionScope AuditReadScope
	event       AuditLog
	listResult  AuditLogListResponse
	values      []string
	listErr     error
	getErr      error
	valuesErr   error
}

func (s *auditReaderStub) List(_ context.Context, scope AuditReadScope, _ *ListAuditLogsQuery) (AuditLogListResponse, error) {
	s.listScope = scope
	return s.listResult, s.listErr
}

func (s *auditReaderStub) GetByID(_ context.Context, scope AuditReadScope, id uuid.UUID, occurredAt time.Time) (AuditLog, error) {
	s.getScope = scope
	s.getID = id
	s.getOccurred = occurredAt
	return s.event, s.getErr
}

func (s *auditReaderStub) ListModules(_ context.Context, scope AuditReadScope, _, _ time.Time, _ int) ([]string, error) {
	s.moduleScope = scope
	return s.values, s.valuesErr
}

func (s *auditReaderStub) ListActions(_ context.Context, scope AuditReadScope, _, _ time.Time, _ int) ([]string, error) {
	s.actionScope = scope
	return s.values, s.valuesErr
}

func TestServiceListRequiresAuthenticationAndPermission(t *testing.T) {
	repo := &auditReaderStub{}
	service := NewService(repo)
	query := &ListAuditLogsQuery{AuditSearchFilters: validAuditFilters()}

	if _, err := service.List(context.Background(), query); err == nil {
		t.Fatal("unauthenticated list succeeded")
	}
	ctx := auditPrincipalContext(uuid.New(), uuid.New(), constants.RoleAdmin, nil, nil)
	if _, err := service.List(ctx, query); err == nil {
		t.Fatal("list without audit:view succeeded")
	}
	if repo.listScope.OrganizationID != nil {
		t.Fatal("repository was called without permission")
	}
}

func TestServiceListBindsTenantAndBranchScopeFromPrincipal(t *testing.T) {
	orgID := uuid.New()
	branchA := uuid.New()
	branchB := uuid.New()
	repo := &auditReaderStub{}
	service := NewService(repo)
	query := &ListAuditLogsQuery{AuditSearchFilters: validAuditFilters()}
	ctx := auditPrincipalContext(orgID, uuid.New(), constants.RoleBranchManager,
		[]uuid.UUID{branchA, branchB}, []string{"audit:view"})

	if _, err := service.List(ctx, query); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repo.listScope.OrganizationID == nil || *repo.listScope.OrganizationID != orgID {
		t.Fatalf("scope organization = %v, want %s", repo.listScope.OrganizationID, orgID)
	}
	if repo.listScope.AllBranches || repo.listScope.IncludeUnassignedBranch ||
		len(repo.listScope.BranchIDs) != 2 {
		t.Fatalf("branch scope widened unexpectedly: %+v", repo.listScope)
	}
}

func TestServiceOrganizationWideScopeRequiresApprovedRole(t *testing.T) {
	tests := []struct {
		role  string
		allow bool
	}{
		{role: constants.RoleAdmin, allow: true},
		{role: constants.RoleSuperAdmin, allow: true},
		{role: constants.RoleAccountant, allow: true},
		{role: constants.RoleAuditor, allow: true},
		{role: constants.RoleBranchManager, allow: false},
	}
	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			orgID := uuid.New()
			scope, err := organizationAuditScope(auditPrincipalContext(
				orgID, uuid.New(), test.role, nil, []string{"audit:view"},
			))
			if (err == nil) != test.allow {
				t.Fatalf("organizationAuditScope() = %+v, %v; allow=%t", scope, err, test.allow)
			}
			if err == nil && (!scope.AllBranches || !scope.IncludeUnassignedBranch) {
				t.Fatalf("organization-wide scope omits unassigned events: %+v", scope)
			}
		})
	}
}

func TestServicePlatformPathIsSuperAdminOnlyAndPlatformEventsOnly(t *testing.T) {
	repo := &auditReaderStub{}
	service := NewService(repo)
	query := &ListAuditLogsQuery{AuditSearchFilters: validAuditFilters()}
	orgID := uuid.New()

	nonAdmin := auditPrincipalContext(orgID, uuid.New(), constants.RoleAdmin, nil, []string{"audit:view"})
	if _, err := service.ListPlatformEvents(nonAdmin, query); err == nil {
		t.Fatal("non-super-admin read platform events")
	}
	if repo.listScope.PlatformEventsOnly {
		t.Fatal("repository was called for unauthorized platform request")
	}

	admin := auditPrincipalContext(orgID, uuid.New(), constants.RoleSuperAdmin, nil, []string{"audit:view"})
	if _, err := service.ListPlatformEvents(admin, query); err != nil {
		t.Fatalf("ListPlatformEvents() error = %v", err)
	}
	if !repo.listScope.PlatformEventsOnly || repo.listScope.PlatformWide {
		t.Fatalf("platform path can see tenant rows: %+v", repo.listScope)
	}
}

func TestServiceGetRedactsSensitiveJSONRecursively(t *testing.T) {
	orgID := uuid.New()
	repo := &auditReaderStub{
		event: AuditLog{
			ID: uuid.New(), OccurredAt: time.Now().UTC(), OrganizationID: &orgID,
			Module: "users", Action: "update",
			Details: map[string]any{
				"nested": map[string]any{"password": "secret", "status": "active"},
				"items":  []any{map[string]any{"access_token": "token-value"}},
			},
			BeforeData: map[string]any{"pin": "1234"},
			AfterData:  map[string]any{"name": "Alice"},
		},
	}
	service := NewService(repo)
	ctx := auditPrincipalContext(orgID, uuid.New(), constants.RoleAuditor, nil, []string{"audit:view"})
	query := &AuditEventLookupQuery{OccurredAt: repo.event.OccurredAt.Format(time.RFC3339Nano)}
	response, err := service.Get(ctx, repo.event.ID, query)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	for _, raw := range [][]byte{response.Details, response.BeforeData} {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("response payload is invalid JSON: %v", err)
		}
	}
	if got := response.Details; !jsonContains(got, auditRedactionMarker) {
		t.Fatalf("details were not redacted: %s", got)
	}
	if got := response.BeforeData; !jsonContains(got, auditRedactionMarker) {
		t.Fatalf("before_data was not redacted: %s", got)
	}
	if repo.getScope.OrganizationID == nil || *repo.getScope.OrganizationID != orgID {
		t.Fatalf("Get() scope = %+v", repo.getScope)
	}
	if repo.getID != repo.event.ID || !repo.getOccurred.Equal(repo.event.OccurredAt) {
		t.Fatalf("Get() lookup key = (%s, %s), want (%s, %s)", repo.getID, repo.getOccurred, repo.event.ID, repo.event.OccurredAt)
	}
}

func TestServiceSettingsAuditRedactsAllValueFields(t *testing.T) {
	orgID := uuid.New()
	repo := &auditReaderStub{
		event: AuditLog{
			ID: uuid.New(), OccurredAt: time.Now().UTC(), OrganizationID: &orgID,
			Module: "settings", Action: "update",
			AfterData: map[string]any{
				"key":   "mailer.password",
				"value": map[string]any{"username": "service-user", "password": "secret"},
			},
		},
	}
	service := NewService(repo)
	ctx := auditPrincipalContext(orgID, uuid.New(), constants.RoleAdmin, nil, []string{"audit:view"})
	response, err := service.Get(ctx, repo.event.ID, &AuditEventLookupQuery{
		OccurredAt: repo.event.OccurredAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !jsonContains(response.AfterData, auditRedactionMarker) {
		t.Fatalf("settings value was not redacted: %s", response.AfterData)
	}
	if jsonContains(response.AfterData, "service-user") || jsonContains(response.AfterData, "secret") {
		t.Fatalf("sensitive settings data leaked: %s", response.AfterData)
	}
}

func TestServiceMapsInaccessibleEventsToNotFound(t *testing.T) {
	orgID := uuid.New()
	repo := &auditReaderStub{getErr: db.ErrNoRows}
	service := NewService(repo)
	ctx := auditPrincipalContext(orgID, uuid.New(), constants.RoleAdmin, nil, []string{"audit:view"})
	if _, err := service.Get(ctx, uuid.New(), validAuditLookupQuery()); err == nil {
		t.Fatal("missing event returned no error")
	}
}

func TestServiceGetRequiresValidPartitionKey(t *testing.T) {
	orgID := uuid.New()
	repo := &auditReaderStub{}
	service := NewService(repo)
	ctx := auditPrincipalContext(orgID, uuid.New(), constants.RoleAdmin, nil, []string{"audit:view"})

	if _, err := service.Get(ctx, uuid.New(), nil); err == nil {
		t.Fatal("detail lookup without occurrence timestamp succeeded")
	}
	if repo.getID != uuid.Nil {
		t.Fatal("repository called with invalid partition key")
	}
}

func TestMapAuditReadErrorDoesNotExposeCursorOrScopeInternals(t *testing.T) {
	err := mapAuditReadError(errors.Join(ErrInvalidAuditCursor, errors.New("internal cursor detail")))
	if err == nil || err.Error() == "" {
		t.Fatal("invalid cursor was not mapped")
	}
	if jsonContains([]byte(err.Error()), "internal cursor detail") {
		t.Fatalf("internal cursor details leaked: %v", err)
	}
}

func TestRedactAuditValueHandlesNestedArraysAndPreservesSafeFields(t *testing.T) {
	input := map[string]any{
		"status": "active",
		"nested": []any{
			map[string]any{"Authorization": "Bearer private", "label": "ok"},
		},
	}
	got := redactAuditValue(input, false).(map[string]any)
	if got["status"] != "active" || !jsonContains(mustMarshalJSON(t, got), auditRedactionMarker) {
		t.Fatalf("redaction result = %#v", got)
	}
}

func auditPrincipalContext(orgID, userID uuid.UUID, role string, branchIDs []uuid.UUID, permissions []string) context.Context {
	return appctx.WithPrincipal(context.Background(), appctx.Principal{
		UserID: userID, OrgID: orgID, RoleName: role, BranchIDs: branchIDs, Permissions: permissions,
	})
}

func jsonContains(value []byte, text string) bool {
	return strings.Contains(string(value), text)
}

func validAuditLookupQuery() *AuditEventLookupQuery {
	return &AuditEventLookupQuery{OccurredAt: "2026-10-01T00:00:00Z"}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}
