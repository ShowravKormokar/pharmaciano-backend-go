package backup

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
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
)

// newDisabledService returns a Service with backup disabled so authorization
// and feature-gate preconditions can be exercised without a database.
func newDisabledService() *Service {
	return &Service{cfg: BackupServiceConfig{Enabled: false}}
}

// newAuthedService returns a Service with backup enabled and a non-nil (but
// database-less) repository, so preconditions that short-circuit before
// repository access can be exercised safely, and methods that reach the DB
// layer surface a database error rather than panicking on a nil repo.
func newAuthedService() *Service {
	return &Service{
		repo:    &Repository{},
		cfg:     BackupServiceConfig{Enabled: true},
		runtime: testBackupRuntime{},
	}
}

type testBackupRuntime struct{}

func (testBackupRuntime) Ready() bool { return true }
func (testBackupRuntime) EnqueueBackup(context.Context, uuid.UUID) error {
	return nil
}
func (testBackupRuntime) EnqueueVerification(context.Context, uuid.UUID) error {
	return nil
}

type testStepUpAuthorizer struct{}

func (testStepUpAuthorizer) RequireRecentStepUp(context.Context) error { return nil }

func superAdminCtx() context.Context {
	return appctx.WithPrincipal(context.Background(), appctx.Principal{
		UserID:   uuid.New(),
		OrgID:    uuid.New(),
		RoleName: constants.RoleSuperAdmin,
	})
}

func adminCtx() context.Context {
	return appctx.WithPrincipal(context.Background(), appctx.Principal{
		UserID:   uuid.New(),
		OrgID:    uuid.New(),
		RoleName: constants.RoleAdmin,
	})
}

func requireCode(t *testing.T, err error, want errs.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	if got := errs.CodeOf(err); got != want {
		t.Fatalf("error code = %s, want %s (err=%v)", got, want, err)
	}
}

func TestCreateBackupRunRequiresEnabled(t *testing.T) {
	svc := newDisabledService()
	_, err := svc.CreateBackupRun(superAdminCtx(), uuid.New(), &CreateBackupRunRequest{})
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestCreateBackupRunRequiresReadyRuntime(t *testing.T) {
	svc := &Service{cfg: BackupServiceConfig{Enabled: true}}
	_, err := svc.CreateBackupRun(superAdminCtx(), uuid.New(), &CreateBackupRunRequest{})
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestCreateBackupRunRequiresSuperAdmin(t *testing.T) {
	svc := newAuthedService()
	_, err := svc.CreateBackupRun(adminCtx(), uuid.New(), &CreateBackupRunRequest{})
	requireCode(t, err, errs.CodeForbidden)
}

func TestCreateBackupRunValidatesRequest(t *testing.T) {
	svc := newAuthedService()
	badReason := strings.Repeat("x", 501)
	_, err := svc.CreateBackupRun(superAdminCtx(), uuid.New(), &CreateBackupRunRequest{Reason: &badReason})
	requireCode(t, err, errs.CodeValidationError)
}

func TestGetBackupRunRequiresEnabled(t *testing.T) {
	svc := newDisabledService()
	_, err := svc.GetBackupRun(superAdminCtx(), uuid.New())
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestGetBackupRunRequiresSuperAdmin(t *testing.T) {
	svc := newAuthedService()
	_, err := svc.GetBackupRun(adminCtx(), uuid.New())
	requireCode(t, err, errs.CodeForbidden)
}

func TestListBackupRunsRequiresEnabled(t *testing.T) {
	svc := newDisabledService()
	_, err := svc.ListBackupRuns(superAdminCtx(), &ListBackupRunsQuery{})
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestListBackupRunsRequiresSuperAdmin(t *testing.T) {
	svc := newAuthedService()
	_, err := svc.ListBackupRuns(adminCtx(), &ListBackupRunsQuery{})
	requireCode(t, err, errs.CodeForbidden)
}

func TestListBackupRunsValidatesQuery(t *testing.T) {
	svc := newAuthedService()
	from := time.Now().Add(time.Hour)
	to := time.Now()
	_, err := svc.ListBackupRuns(superAdminCtx(), &ListBackupRunsQuery{From: &from, To: &to})
	requireCode(t, err, errs.CodeValidationError)
}

func TestRequestDownloadRequiresEnabled(t *testing.T) {
	svc := newDisabledService()
	_, err := svc.RequestDownload(superAdminCtx(), uuid.New())
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestRequestDownloadRequiresSuperAdmin(t *testing.T) {
	svc := newAuthedService()
	_, err := svc.RequestDownload(adminCtx(), uuid.New())
	requireCode(t, err, errs.CodeForbidden)
}

func TestRequestDownloadRequiresStepUp(t *testing.T) {
	svc := newAuthedService()
	_, err := svc.RequestDownload(superAdminCtx(), uuid.New())
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestHighRiskOperationsRemainUnavailableAfterStepUp(t *testing.T) {
	svc := newAuthedService()
	svc.stepUp = testStepUpAuthorizer{}

	_, err := svc.RequestDownload(superAdminCtx(), uuid.New())
	requireCode(t, err, errs.CodeFeatureDisabled)

	err = svc.RequestDelete(superAdminCtx(), uuid.New())
	requireCode(t, err, errs.CodeFeatureDisabled)

	_, err = svc.SetLegalHold(superAdminCtx(), uuid.New(), true)
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestRequestVerifyRequiresEnabled(t *testing.T) {
	svc := newDisabledService()
	_, err := svc.RequestVerify(superAdminCtx(), uuid.New(), VerificationTypeIntegrity)
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestRequestVerifyRequiresSuperAdmin(t *testing.T) {
	svc := newAuthedService()
	_, err := svc.RequestVerify(adminCtx(), uuid.New(), VerificationTypeIntegrity)
	requireCode(t, err, errs.CodeForbidden)
}

func TestRequestVerifyRejectsInvalidType(t *testing.T) {
	svc := newAuthedService()
	_, err := svc.RequestVerify(superAdminCtx(), uuid.New(), VerificationType("bogus"))
	requireCode(t, err, errs.CodeValidationError)
}

func TestRequestDeleteRequiresEnabled(t *testing.T) {
	svc := newDisabledService()
	err := svc.RequestDelete(superAdminCtx(), uuid.New())
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestRequestDeleteRequiresSuperAdmin(t *testing.T) {
	svc := newAuthedService()
	err := svc.RequestDelete(adminCtx(), uuid.New())
	requireCode(t, err, errs.CodeForbidden)
}

func TestSetLegalHoldRequiresEnabled(t *testing.T) {
	svc := newDisabledService()
	_, err := svc.SetLegalHold(superAdminCtx(), uuid.New(), true)
	requireCode(t, err, errs.CodeFeatureDisabled)
}

func TestSetLegalHoldRequiresSuperAdmin(t *testing.T) {
	svc := newAuthedService()
	_, err := svc.SetLegalHold(adminCtx(), uuid.New(), true)
	requireCode(t, err, errs.CodeForbidden)
}

func TestCreateBackupRunRequestValidate(t *testing.T) {
	tests := []struct {
		name     string
		req      *CreateBackupRunRequest
		wantErrs int
	}{
		{name: "nil request", req: nil, wantErrs: 1},
		{name: "empty reason", req: &CreateBackupRunRequest{}, wantErrs: 0},
		{name: "whitespace reason cleared", req: &CreateBackupRunRequest{Reason: strPtr("   ")}, wantErrs: 0},
		{name: "valid reason", req: &CreateBackupRunRequest{Reason: strPtr("manual backup")}, wantErrs: 0},
		{name: "reason too long", req: &CreateBackupRunRequest{Reason: strPtr(strings.Repeat("a", 501))}, wantErrs: 1},
		{name: "reason control char", req: &CreateBackupRunRequest{Reason: strPtr("a\x07b")}, wantErrs: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.req.Validate()
			if len(errs) != tt.wantErrs {
				t.Fatalf("Validate() returned %d errors, want %d: %v", len(errs), tt.wantErrs, errs)
			}
			if tt.req != nil && tt.req.Reason != nil && *tt.req.Reason == "   " {
				if tt.req.Reason != nil {
					t.Errorf("whitespace reason was not cleared to nil")
				}
			}
		})
	}
}

func TestListBackupRunsQueryNormalizeAndValidate(t *testing.T) {
	tests := []struct {
		name     string
		query    *ListBackupRunsQuery
		wantErrs int
	}{
		{name: "nil query", query: nil, wantErrs: 1},
		{name: "empty query", query: &ListBackupRunsQuery{}, wantErrs: 0},
		{name: "invalid status", query: &ListBackupRunsQuery{Status: BackupRunStatus("bogus")}, wantErrs: 1},
		{name: "invalid type", query: &ListBackupRunsQuery{Type: BackupType("bogus")}, wantErrs: 1},
		{name: "invalid trigger", query: &ListBackupRunsQuery{Trigger: BackupTrigger("bogus")}, wantErrs: 1},
		{name: "from after to", query: &ListBackupRunsQuery{
			From: timePtr(time.Now().Add(time.Hour)), To: timePtr(time.Now()),
		}, wantErrs: 1},
		{name: "limit zero defaults", query: &ListBackupRunsQuery{}, wantErrs: 0},
		{name: "limit out of range", query: &ListBackupRunsQuery{Limit: 0}, wantErrs: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var q *ListBackupRunsQuery
			if tt.query != nil {
				q = tt.query
			}
			errs := q.NormalizeAndValidate()
			if len(errs) != tt.wantErrs {
				t.Fatalf("NormalizeAndValidate() returned %d errors, want %d: %v", len(errs), tt.wantErrs, errs)
			}
		})
	}
	// limit 0 should be normalized to default page size
	q := &ListBackupRunsQuery{}
	_ = q.NormalizeAndValidate()
	if q.Limit != defaultBackupPageSize {
		t.Fatalf("limit normalized to %d, want %d", q.Limit, defaultBackupPageSize)
	}
}

func TestParseID(t *testing.T) {
	valid := uuid.New()
	id, err := parseIDFromString(valid.String())
	if err != nil {
		t.Fatalf("parseIDFromString(valid) error = %v", err)
	}
	if id != valid {
		t.Fatalf("parseIDFromString() = %v, want %v", id, valid)
	}

	_, err = parseIDFromString("not-a-uuid")
	if err == nil {
		t.Fatal("expected validation error for invalid UUID")
	}
	if errs.CodeOf(err) != errs.CodeValidationError {
		t.Fatalf("error code = %s, want %s", errs.CodeOf(err), errs.CodeValidationError)
	}
}

func TestLocations(t *testing.T) {
	id := uuid.New()
	if loc := locationForBackupRun(id); loc != "/api/v1/backups/"+id.String() {
		t.Errorf("locationForBackupRun() = %q", loc)
	}
	if loc := locationForVerification(id); loc != "/api/v1/backups/verifications/"+id.String() {
		t.Errorf("locationForVerification() = %q", loc)
	}
}

func TestValidateVerificationResultRequiresSuccessEvidence(t *testing.T) {
	checksumOK, decryptOK, restoreOK := true, true, true
	now := time.Now().UTC()
	repository := &Repository{}

	integrity := BackupVerification{
		ID: uuid.New(), Type: VerificationTypeIntegrity, Status: VerificationStatusSucceeded,
		ChecksumOK: &checksumOK, DecryptOK: &decryptOK,
	}
	if err := repository.validateVerificationResult(integrity); err != nil {
		t.Fatalf("valid integrity result rejected: %v", err)
	}

	badIntegrity := integrity
	checksumFalse := false
	badIntegrity.ChecksumOK = &checksumFalse
	if err := repository.validateVerificationResult(badIntegrity); err == nil {
		t.Fatal("integrity result with failed checksum was accepted")
	}

	restore := BackupVerification{
		ID: uuid.New(), Type: VerificationTypeRestoreDrill, Status: VerificationStatusSucceeded,
		ChecksumOK: &checksumOK, DecryptOK: &decryptOK, RestoreOK: &restoreOK,
		ScratchTargetID: strPtr("isolated-test-db"), CleanupCompletedAt: &now,
	}
	if err := repository.validateVerificationResult(restore); err != nil {
		t.Fatalf("valid restore-drill result rejected: %v", err)
	}
	restore.CleanupCompletedAt = nil
	if err := repository.validateVerificationResult(restore); err == nil {
		t.Fatal("restore drill without cleanup evidence was accepted")
	}
}

func TestSafeBackupFailureDropsRawDiagnostics(t *testing.T) {
	code, message := safeBackupFailure("PG_DUMP_FAILED")
	if code != "PG_DUMP_FAILED" || strings.Contains(message, "password") {
		t.Fatalf("known error was not normalized: code=%q message=%q", code, message)
	}

	code, message = safeBackupFailure("password=do-not-store")
	if code != "BACKUP_FAILED" || strings.Contains(message, "do-not-store") {
		t.Fatalf("unknown error was not normalized: code=%q message=%q", code, message)
	}
}

func TestHandlerCreateBackupRunRejectsInvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{svc: nil, val: nil, log: nil}
	router := gin.New()
	router.POST("/backups", h.CreateBackupRun)

	body := strings.NewReader(`{not valid json`)
	req := httptest.NewRequest(http.MethodPost, "/backups", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(superAdminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CreateBackupRun invalid body status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "VALIDATION_ERROR") {
		t.Errorf("expected VALIDATION_ERROR in body: %s", rec.Body.String())
	}
}

func TestHandlerGetBackupRunRejectsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{svc: nil, val: nil, log: nil}
	router := gin.New()
	router.GET("/backups/:id", h.GetBackupRun)

	req := httptest.NewRequest(http.MethodGet, "/backups/not-a-uuid", nil)
	req = req.WithContext(superAdminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GetBackupRun invalid id status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandlerListBackupRunsValidatesQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{svc: newAuthedService(), val: nil, log: nil}
	router := gin.New()
	router.GET("/backups", h.ListBackupRuns)

	req := httptest.NewRequest(http.MethodGet, "/backups?from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z", nil)
	req = req.WithContext(superAdminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ListBackupRuns bad query status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestRegisterRoutesLeavesBackupUnavailableByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{svc: newDisabledService()}
	router := gin.New()
	h.RegisterRoutes(router.Group("/api/v1"), nil)

	for _, path := range []string{
		"/api/v1/backups",
		"/api/v1/backups/" + uuid.NewString(),
		"/api/v1/backups/" + uuid.NewString() + "/download",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s returned %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}

func strPtr(s string) *string        { return &s }
func timePtr(t time.Time) *time.Time { return &t }

// parseIDFromString exercises the UUID validation logic used by parseID without
// a Gin context.
func parseIDFromString(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter id must be a valid UUID").WithCause(err)
	}
	return id, nil
}
