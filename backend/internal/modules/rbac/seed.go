package rbac

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	"backend/internal/platform/db"
)

// Seeder plants the platform's fixed RBAC catalogue: every permission
// (module:action), the eight system roles, and the default grant matrix that
// gives each role its capabilities. It is idempotent and safe to run on every
// boot — permissions upsert on their unique key, roles upsert on the shared
// system-role key, and each system role's grants are *replaced* with the coded
// matrix so the code stays the single source of truth for system authority.
//
// The seeder deliberately does NOT create users or assign roles to users: rbac
// never writes the users table. Seeding the bootstrap SUPER_ADMIN *user* and
// giving it the SUPER_ADMIN role is the user/auth seed's job (cmd/seed).
//
// Ordering: run Seed before the enforcer's initial Load (or reload the enforcer
// afterwards) so the freshly seeded policy is in the in-memory snapshot.
type Seeder struct {
	repo *Repository
	db   *db.DB
	log  *zap.Logger
}

// NewSeeder builds a seeder over the shared repository and database handle.
func NewSeeder(repo *Repository, database *db.DB, log *zap.Logger) *Seeder {
	if log == nil {
		log = zap.NewNop()
	}
	return &Seeder{repo: repo, db: database, log: log}
}

// grantSpec is one line of the role→permission matrix: a module and the actions
// on it a role receives. An empty actions slice means "every action" (the whole
// module), expanded against constants.AllActions.
type grantSpec struct {
	module  string
	actions []string
}

// Seed runs the whole catalogue seed in a single transaction guarded by an
// advisory lock, so two instances booting at once serialise instead of racing.
// Either the entire catalogue is applied or none of it is.
func (s *Seeder) Seed(ctx context.Context) error {
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.AcquireSeedLock(ctx); err != nil {
			return fmt.Errorf("acquire seed lock: %w", err)
		}

		// 1) Permission catalogue — the full module × action capability space, so
		//    any route's (module, action) and any future grant has a backing row.
		permID := make(map[string]uuid.UUID, len(constants.AllModules)*len(constants.AllActions))
		for _, module := range constants.AllModules {
			for _, action := range constants.AllActions {
				id, err := s.repo.UpsertSystemPermission(ctx, module, action, permissionDescription(module, action))
				if err != nil {
					return fmt.Errorf("upsert permission %s:%s: %w", module, action, err)
				}
				permID[module+":"+action] = id
			}
		}

		// 2) System roles and their (authoritative) grant sets.
		matrix := systemRoleMatrix()
		for _, name := range constants.SystemRoles {
			desc := systemRoleDescriptions[name]
			roleID, err := s.repo.UpsertSystemRole(ctx, name, strPtr(desc), systemRolePriorities[name])
			if err != nil {
				return fmt.Errorf("upsert role %s: %w", name, err)
			}
			wantIDs := resolveGrantIDs(matrix[name], permID)
			if err := s.repo.ReplaceRolePermissions(ctx, roleID, wantIDs, nil); err != nil {
				return fmt.Errorf("set grants for role %s: %w", name, err)
			}
		}

		s.log.Info("rbac catalogue seeded",
			zap.Int("permissions", len(permID)),
			zap.Int("system_roles", len(constants.SystemRoles)),
		)
		return nil
	})
}

// systemRolePriorities ranks the system roles; the highest-priority active role
// a user holds becomes their canonical role (used by the SUPER_ADMIN/org-wide
// fast paths). Custom tenant roles default to 0, so a system role always
// out-ranks a same-named-tier custom one.
var systemRolePriorities = map[string]int{
	constants.RoleSuperAdmin:    1000,
	constants.RoleAdmin:         900,
	constants.RoleBranchManager: 700,
	constants.RoleAccountant:    600,
	constants.RolePharmacist:    500,
	constants.RoleWarehouse:     450,
	constants.RoleCashier:       400,
	constants.RoleAuditor:       300,
}

// systemRoleDescriptions documents each seeded role in the catalogue.
var systemRoleDescriptions = map[string]string{
	constants.RoleSuperAdmin:    "Platform super administrator — unrestricted access (break-glass).",
	constants.RoleAdmin:         "Organization administrator — full control of the tenant except platform-level organization management.",
	constants.RoleBranchManager: "Branch manager — runs a branch's day-to-day sales, inventory and purchasing.",
	constants.RoleAccountant:    "Accountant — ledger, payments and financial reporting.",
	constants.RolePharmacist:    "Pharmacist — dispensing, medicine and stock stewardship.",
	constants.RoleCashier:       "Cashier — point-of-sale operations.",
	constants.RoleWarehouse:     "Warehouse operator — stock receipt, transfers and warehouse upkeep.",
	constants.RoleAuditor:       "Auditor — read-only oversight across the business and the audit log.",
}

// systemRoleMatrix returns the default capability grants for every system role.
// These are sensible starting defaults meant to be tuned per deployment; system
// roles are API-immutable, so the way to change them is to edit this matrix and
// re-run the seed (which replaces each system role's grants).
func systemRoleMatrix() map[string][]grantSpec {
	// Reusable action groups.
	view := []string{constants.ActionView}
	readOnly := []string{constants.ActionView, constants.ActionExport}
	inbox := []string{constants.ActionRead, constants.ActionDismiss}

	m := make(map[string][]grantSpec, len(constants.SystemRoles))

	// SUPER_ADMIN — every capability. The RBAC middleware also short-circuits
	// this role by name; the explicit grants are defence in depth in case that
	// bypass is ever removed.
	m[constants.RoleSuperAdmin] = allModuleSpecs()

	// ADMIN — full tenant administration, minus platform-level organization
	// management (creating/deleting organizations is SUPER_ADMIN territory).
	m[constants.RoleAdmin] = allModuleSpecsExcept(constants.ModuleOrganizations)

	// BRANCH_MANAGER — a branch's day-to-day operations.
	m[constants.RoleBranchManager] = []grantSpec{
		{module: constants.ModuleSales},
		{module: constants.ModuleSalesReturns},
		{module: constants.ModuleCustomers},
		{module: constants.ModuleCoupons},
		{module: constants.ModuleInventory, actions: []string{constants.ActionView, constants.ActionAdjust, constants.ActionTransfer, constants.ActionExport}},
		{module: constants.ModulePurchases, actions: []string{constants.ActionCreate, constants.ActionView, constants.ActionUpdate, constants.ActionSubmit, constants.ActionApprove, constants.ActionReceive, constants.ActionCancel}},
		{module: constants.ModulePurchasePayments, actions: []string{constants.ActionCreate, constants.ActionView}},
		{module: constants.ModuleWarehouses, actions: view},
		{module: constants.ModuleMedicines, actions: view},
		{module: constants.ModuleSuppliers, actions: view},
		{module: constants.ModuleBrands, actions: view},
		{module: constants.ModuleManufacturers, actions: view},
		{module: constants.ModuleTargets, actions: view},
		{module: constants.ModuleLedger, actions: view},
		{module: constants.ModuleReports, actions: readOnly},
		{module: constants.ModuleAnalytics, actions: view},
		{module: constants.ModuleUsers, actions: view},
		{module: constants.ModuleNotifications, actions: inbox},
	}

	// ACCOUNTANT — finance and financial reporting.
	m[constants.RoleAccountant] = []grantSpec{
		{module: constants.ModuleLedger, actions: []string{constants.ActionView, constants.ActionPost, constants.ActionExport}},
		{module: constants.ModulePurchasePayments},
		{module: constants.ModulePurchases, actions: readOnly},
		{module: constants.ModuleSales, actions: readOnly},
		{module: constants.ModuleSalesReturns, actions: readOnly},
		{module: constants.ModuleCustomers, actions: view},
		{module: constants.ModuleSuppliers, actions: view},
		{module: constants.ModuleReports, actions: readOnly},
		{module: constants.ModuleAnalytics, actions: view},
		{module: constants.ModuleNotifications, actions: inbox},
	}

	// PHARMACIST — dispensing and medicine/stock stewardship.
	m[constants.RolePharmacist] = []grantSpec{
		{module: constants.ModuleMedicines, actions: []string{constants.ActionView, constants.ActionUpdate}},
		{module: constants.ModuleInventory, actions: []string{constants.ActionView, constants.ActionAdjust}},
		{module: constants.ModuleSales, actions: []string{constants.ActionCreate, constants.ActionView}},
		{module: constants.ModuleSalesReturns, actions: []string{constants.ActionCreate, constants.ActionView}},
		{module: constants.ModuleCustomers, actions: []string{constants.ActionCreate, constants.ActionView, constants.ActionUpdate}},
		{module: constants.ModulePurchases, actions: []string{constants.ActionView, constants.ActionReceive}},
		{module: constants.ModuleReports, actions: view},
		{module: constants.ModuleNotifications, actions: inbox},
	}

	// CASHIER — point of sale.
	m[constants.RoleCashier] = []grantSpec{
		{module: constants.ModuleSales, actions: []string{constants.ActionCreate, constants.ActionView}},
		{module: constants.ModuleSalesReturns, actions: []string{constants.ActionCreate}},
		{module: constants.ModuleCustomers, actions: []string{constants.ActionCreate, constants.ActionView}},
		{module: constants.ModuleCoupons, actions: view},
		{module: constants.ModuleNotifications, actions: inbox},
	}

	// WAREHOUSE — stock receipt, transfers, warehouse upkeep.
	m[constants.RoleWarehouse] = []grantSpec{
		{module: constants.ModuleInventory, actions: []string{constants.ActionView, constants.ActionAdjust, constants.ActionTransfer, constants.ActionImport, constants.ActionExport}},
		{module: constants.ModuleWarehouses, actions: []string{constants.ActionView, constants.ActionUpdate}},
		{module: constants.ModulePurchases, actions: []string{constants.ActionView, constants.ActionReceive}},
		{module: constants.ModuleMedicines, actions: view},
		{module: constants.ModuleSuppliers, actions: view},
		{module: constants.ModuleReports, actions: view},
		{module: constants.ModuleNotifications, actions: inbox},
	}

	// AUDITOR — read-only oversight across the business plus the audit log.
	auditModules := []string{
		constants.ModuleUsers, constants.ModuleRoles, constants.ModuleSales, constants.ModuleSalesReturns,
		constants.ModulePurchases, constants.ModulePurchasePayments, constants.ModuleInventory,
		constants.ModuleLedger, constants.ModuleCustomers, constants.ModuleSuppliers, constants.ModuleMedicines,
		constants.ModuleReports, constants.ModuleAnalytics, constants.ModuleAudit, constants.ModuleSettings,
	}
	auditor := make([]grantSpec, 0, len(auditModules)+1)
	for _, mod := range auditModules {
		auditor = append(auditor, grantSpec{module: mod, actions: readOnly})
	}
	auditor = append(auditor, grantSpec{module: constants.ModuleNotifications, actions: inbox})
	m[constants.RoleAuditor] = auditor

	return m
}

// allModuleSpecs returns a spec granting every action on every module.
func allModuleSpecs() []grantSpec {
	specs := make([]grantSpec, 0, len(constants.AllModules))
	for _, module := range constants.AllModules {
		specs = append(specs, grantSpec{module: module}) // nil actions ⇒ all
	}
	return specs
}

// allModuleSpecsExcept returns allModuleSpecs minus the excluded modules.
func allModuleSpecsExcept(excluded ...string) []grantSpec {
	skip := make(map[string]struct{}, len(excluded))
	for _, e := range excluded {
		skip[e] = struct{}{}
	}
	specs := make([]grantSpec, 0, len(constants.AllModules))
	for _, module := range constants.AllModules {
		if _, ok := skip[module]; ok {
			continue
		}
		specs = append(specs, grantSpec{module: module})
	}
	return specs
}

// resolveGrantIDs expands a role's grant specs into a de-duplicated list of
// permission ids using the catalogue map keyed "module:action". A spec with no
// actions expands to every action. Unknown pairs are skipped (the catalogue is
// seeded first, so this should not happen).
func resolveGrantIDs(specs []grantSpec, permID map[string]uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, 64)
	seen := make(map[uuid.UUID]struct{}, 64)
	for _, sp := range specs {
		actions := sp.actions
		if len(actions) == 0 {
			actions = constants.AllActions
		}
		for _, action := range actions {
			id, ok := permID[sp.module+":"+action]
			if !ok {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// permissionDescription renders a human label for a catalogue permission, e.g.
// "Create on the sales module".
func permissionDescription(module, action string) *string {
	s := fmt.Sprintf("%s on the %s module", capitalize(action), module)
	return &s
}

// capitalize upper-cases the first byte of an ASCII action word for the label.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-'a'+'A') + s[1:]
	}
	return s
}

// strPtr returns nil for "" and &s otherwise, so an empty description stores as
// SQL NULL rather than an empty string.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
