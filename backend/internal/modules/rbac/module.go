package rbac

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"backend/internal/middleware"
	"backend/internal/platform/db"
	platformredis "backend/internal/platform/redis"
	"backend/internal/platform/telemetry"
	"backend/internal/platform/validator"
)

// Module is the rbac module's fully assembled surface. Unlike a leaf domain
// module (which hides everything behind New → *Handler), rbac must hand three
// things back to the composition root:
//
//   - Enforcer — the concrete authorization engine. main.go injects it as the
//     middleware.Authorizer for every Protected route, and the auth module uses
//     its ResolveAccess as the AccessResolver port when minting principals.
//   - Seeder — plants the fixed permission/role catalogue; main.go runs it once
//     at startup (inside a transaction with an advisory lock).
//   - Service — the user module's assignment endpoints (/users/{id}/roles) call
//     it to operate on rbac-owned user_roles without importing rbac internals.
//
// Startup contract (composition root): build the module, run Seeder.Seed, then
// Enforcer.Load (initial warm) — or Enforcer.StartAutoReload, which warms
// immediately — before serving traffic. Until the first successful Load the
// enforcer fails closed, so no request is authorized against an empty policy.
type Module struct {
	Handler    *Handler
	Service    *Service
	Enforcer   *Enforcer
	Seeder     *Seeder
	Authorizer *CachedAuthorizer
}

// New wires the rbac dependency graph: repository → enforcer → service →
// (seeder, handler). The enforcer is shared between the service (which reloads
// it after every mutation) and the composition root (which serves it as the
// platform Authorizer), so there is exactly one snapshot in the process.
// metrics may be nil — the authorizer is nil-tolerant and the auth/role-mutation
// paths no-op the counters in that case.
func New(database *db.DB, rdb *platformredis.Client, v *validator.Validator, metrics *telemetry.Metrics, log *zap.Logger) *Module {
	if log == nil {
		log = zap.NewNop()
	}
	repo := NewRepository(database)
	enf := NewEnforcer(repo, log)
	svc := NewService(repo, database, enf, metrics, log)
	seeder := NewSeeder(repo, database, log)
	authorizer := NewCachedAuthorizer(enf, rdb, metrics, log)
	handler := NewHandler(svc, v, log)
	return &Module{
		Handler:    handler,
		Service:    svc,
		Enforcer:   enf,
		Seeder:     seeder,
		Authorizer: authorizer,
	}
}

// RegisterRoutes lets the Module satisfy the router's ModuleRegistrar directly,
// delegating to its Handler. main.go can therefore place the Module in the
// modules slice while still holding references to the enforcer and seeder.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	m.Handler.RegisterRoutes(rg, mw)
}
