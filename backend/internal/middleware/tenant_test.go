package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
)

func TestTenantAllowsOrganizationScopedRolesWithoutBranchAssignments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{
		constants.RoleSuperAdmin,
		constants.RoleAdmin,
		constants.RoleAccountant,
		constants.RoleAuditor,
	} {
		t.Run(role, func(t *testing.T) {
			orgID := uuid.New()
			ctx := appctx.WithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil).Context(), appctx.Principal{
				UserID: uuid.New(), OrgID: orgID, RoleName: role,
			})
			request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
			recorder := httptest.NewRecorder()
			nextCalled := false

			router := gin.New()
			router.Use((&Middleware{log: zap.NewNop()}).Tenant())
			router.GET("/", func(c *gin.Context) {
				nextCalled = true
				principal, ok := appctx.CurrentPrincipal(c.Request.Context())
				if !ok {
					t.Fatal("principal was lost")
				}
				if len(principal.BranchIDs) != 0 {
					t.Fatalf("default branch scope = %v, want organization-wide", principal.BranchIDs)
				}
			})
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK || !nextCalled {
				t.Fatalf("Tenant() response = %d, next called = %t", recorder.Code, nextCalled)
			}
		})
	}
}

func TestTenantKeepsBranchBoundRoleRestricted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	ctx := appctx.WithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil).Context(), appctx.Principal{
		UserID: uuid.New(), OrgID: orgID, RoleName: constants.RoleBranchManager,
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	nextCalled := false

	router := gin.New()
	router.Use((&Middleware{log: zap.NewNop()}).Tenant())
	router.GET("/", func(c *gin.Context) { nextCalled = true })
	router.ServeHTTP(recorder, request)

	if nextCalled || recorder.Code == http.StatusOK {
		t.Fatalf("branch-bound role without assignments was allowed: status=%d", recorder.Code)
	}
}
