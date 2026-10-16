package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"backend/internal/common/constants"
)

type auditHandlerReaderStub struct {
	listCalls int
	list      AuditLogListResponse
}

func (s *auditHandlerReaderStub) List(context.Context, AuditReadScope, *ListAuditLogsQuery) (AuditLogListResponse, error) {
	s.listCalls++
	return s.list, nil
}

func (*auditHandlerReaderStub) GetByID(context.Context, AuditReadScope, uuid.UUID, time.Time) (AuditLog, error) {
	return AuditLog{}, nil
}

func (*auditHandlerReaderStub) ListModules(context.Context, AuditReadScope, time.Time, time.Time, int) ([]string, error) {
	return []string{"users"}, nil
}

func (*auditHandlerReaderStub) ListActions(context.Context, AuditReadScope, time.Time, time.Time, int) ([]string, error) {
	return []string{"update"}, nil
}

func TestAuditListHandlerRejectsUnknownRepeatedAndMalformedParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditHandlerReaderStub{}
	handler := &Handler{svc: NewService(repo)}
	router := gin.New()
	router.GET("/audit-logs", handler.List)

	tests := []string{
		"?from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z&unexpected=x",
		"?from=2026-10-01T00:00:00Z&from=2026-10-02T00:00:00Z&to=2026-10-03T00:00:00Z",
		"?from=%zz&to=2026-10-02T00:00:00Z",
		"?from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z&limit=not-a-number",
	}
	for _, target := range tests {
		request := httptest.NewRequest(http.MethodGet, "/audit-logs"+target, nil)
		request = request.WithContext(auditPrincipalContext(
			uuid.New(), uuid.New(), constants.RoleAdmin, nil, []string{"audit:view"},
		))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want %d; body=%s", target, recorder.Code, http.StatusBadRequest, recorder.Body)
		}
	}
	if repo.listCalls != 0 {
		t.Fatalf("repository called %d times for invalid requests", repo.listCalls)
	}
}

func TestAuditDetailHandlerRequiresPartitionTimestamp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditHandlerReaderStub{}
	handler := &Handler{svc: NewService(repo)}
	router := gin.New()
	router.GET("/audit-logs/:id", handler.Get)

	request := httptest.NewRequest(http.MethodGet, "/audit-logs/"+uuid.NewString(), nil)
	request = request.WithContext(auditPrincipalContext(
		uuid.New(), uuid.New(), constants.RoleAdmin, nil, []string{"audit:view"},
	))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("detail without occurred_at status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body)
	}
}

func TestAuditListHandlerReturnsCursorPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	event := AuditLogListItem{ID: uuid.New(), OccurredAt: time.Now().UTC(), Module: "users", Action: "view"}
	repo := &auditHandlerReaderStub{list: AuditLogListResponse{
		Items: []AuditLogListItem{event}, HasMore: true, NextCursor: "signed-cursor",
	}}
	handler := &Handler{svc: NewService(repo)}
	router := gin.New()
	router.GET("/audit-logs", handler.List)

	request := httptest.NewRequest(http.MethodGet,
		"/audit-logs?from=2026-10-01T00:00:00Z&to=2026-10-17T00:00:00Z&limit=1", nil)
	request = request.WithContext(auditPrincipalContext(
		uuid.New(), uuid.New(), constants.RoleAdmin, nil, []string{"audit:view"},
	))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body)
	}
	if repo.listCalls != 1 {
		t.Fatalf("repository calls = %d, want 1", repo.listCalls)
	}
	for _, expected := range []string{`"has_more":true`, `"next_cursor":"signed-cursor"`, `"items"`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("response missing %s: %s", expected, recorder.Body.String())
		}
	}
}
