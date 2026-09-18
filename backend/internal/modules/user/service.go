package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/common/enums"
	errs "backend/internal/errors"
	"backend/internal/modules/rbac"
	"backend/internal/platform/db"
	"backend/pkg/crypto"
	"backend/pkg/pagination"
)

// RoleService is the slice of the rbac module the user module needs to surface
// the /users/{id}/roles assignment endpoints. It is declared here (the consumer
// side) so the user module depends on a narrow port rather than the whole rbac
// service; *rbac.Service satisfies it. rbac never imports user, so there is no
// cycle. The user Service always verifies the target user exists in the caller's
// tenant before delegating, giving these endpoints the same cross-tenant→NotFound
// behavior as the rest of the module.
type RoleService interface {
	AssignRole(ctx context.Context, userID uuid.UUID, req *rbac.AssignRoleRequest) (*rbac.UserRoleItem, error)
	RevokeRole(ctx context.Context, userID, roleID uuid.UUID) error
	ListRolesOfUser(ctx context.Context, userID uuid.UUID) ([]rbac.UserRoleItem, error)
}

// AuthzBumper advances a user's durable authorization epoch so an
// access-control change (here: a branch assignment grant/revoke) is reflected in
// the auth middleware immediately rather than on the next token refresh (ADR
// §15). It is a consumer-side port implemented by the auth module and injected
// by the composition root; the user module never imports auth.
//
// Contract: implementations MUST honor the transaction on ctx (via db.FromCtx),
// so the bump commits atomically with the assignment write that triggered it — if
// the bump fails, the whole change rolls back and the stale scope is never
// persisted.
type AuthzBumper interface {
	BumpAuthzVersion(ctx context.Context, userID uuid.UUID) error
}

// SessionRevoker terminates all of a user's active sessions and refresh tokens.
// It is a consumer-side port implemented by the auth module and injected by the
// composition root; the user module never imports auth. The service invokes it
// when a user is moved out of a login-capable status or soft-deleted, so the
// change takes effect immediately instead of on the next token expiry.
//
// Contract: implementations MUST honor the transaction on ctx (via db.FromCtx),
// so revocation commits atomically with the status/delete write that triggered
// it — if revocation fails, the whole change rolls back.
type SessionRevoker interface {
	RevokeUserSessions(ctx context.Context, userID uuid.UUID, reason string) error
}

// Service holds the user module's business logic: tenant binding, password
// hashing on create, the user+profile write pair, status lifecycle with session
// revocation side effects, and delegation of role assignment to rbac. It is the
// only layer that maps raw driver errors to domain *errs.AppError values.
type Service struct {
	repo    *Repository
	db      *db.DB
	hasher  crypto.PasswordHash
	roles   RoleService
	revoker SessionRevoker // may be nil until the auth module is wired in
	bumper  AuthzBumper    // may be nil until the auth module is wired in
	log     *zap.Logger
}

// NewService assembles the service. hasher and roles are required; revoker and
// bumper may be nil during the phased build before auth exists (a status change
// then logs a warning instead of revoking, and a branch assignment logs a warning
// instead of bumping) and are injected once auth is available.
func NewService(repo *Repository, database *db.DB, hasher crypto.PasswordHash, roles RoleService, revoker SessionRevoker, bumper AuthzBumper, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, db: database, hasher: hasher, roles: roles, revoker: revoker, bumper: bumper, log: log}
}

// Create provisions a new user and their identity profile in the caller's
// organization. The password is Argon2id-hashed before it ever reaches the
// database, and the user row plus profile row are written in one transaction so a
// user never exists without a profile. A duplicate email / username / employee
// code surfaces as a 409 naming the offending field.
func (s *Service) Create(ctx context.Context, req *CreateUserRequest) (*Detail, error) {
	orgID := appctx.OrgID(ctx)

	joiningDate, err := parseDate(req.JoiningDate)
	if err != nil {
		return nil, err
	}
	dob, err := parseDate(req.Profile.DateOfBirth)
	if err != nil {
		return nil, err
	}

	hash, err := s.hasher.Hash(req.Password)
	if err != nil {
		// A hashing failure is an internal fault, never the caller's mistake.
		return nil, errs.Internal(err)
	}

	u := &User{
		OrganizationID:     orgID,
		BranchID:           req.BranchID,
		EmployeeCode:       req.EmployeeCode,
		Email:              req.Email,
		Username:           req.Username,
		Phone:              req.Phone,
		PasswordHash:       hash,
		Status:             derefStatus(req.Status, enums.UserStatusActive),
		Stage:              derefStage(req.Stage, enums.UserStageUnverified),
		MustChangePassword: derefBool(req.MustChangePassword, true),
		JoiningDate:        joiningDate,
		EmploymentType:     req.EmploymentType,
	}

	var out *Detail
	err = s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := s.ensureBranchInOrg(ctx, orgID, req.BranchID); err != nil {
			return err
		}
		created, err := s.repo.InsertUser(ctx, u)
		if err != nil {
			return mapWriteErr(err)
		}
		profile, err := s.repo.InsertProfile(ctx, &UserProfile{
			UserID:      created.ID,
			FirstName:   req.Profile.FirstName,
			LastName:    req.Profile.LastName,
			MiddleName:  req.Profile.MiddleName,
			DisplayName: req.Profile.DisplayName,
			DateOfBirth: dob,
			Gender:      req.Profile.Gender,
			AvatarURL:   req.Profile.AvatarURL,
		})
		if err != nil {
			return mapWriteErr(err)
		}
		out = &Detail{User: *created, Profile: profile}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Get returns one user in the caller's organization with their profile, or
// NotFound. A missing profile row (a data-integrity edge case, since create
// always writes one) degrades to a nil profile rather than failing the read.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Detail, error) {
	u, err := s.repo.FindByID(ctx, appctx.OrgID(ctx), id)
	if err != nil {
		return nil, mapReadErr(err)
	}
	profile, err := s.loadProfile(ctx, id)
	if err != nil {
		return nil, err
	}
	return &Detail{User: *u, Profile: profile}, nil
}

// GetMe returns the authenticated caller's own user record and profile. It reads
// the subject from the request principal, so it needs no id in the path.
func (s *Service) GetMe(ctx context.Context) (*Detail, error) {
	uid := appctx.UserID(ctx)
	if uid == uuid.Nil {
		return nil, errs.Unauthenticated()
	}
	return s.Get(ctx, uid)
}

// List returns a filtered, paginated page of the caller's users plus meta. The
// enum and UUID filters are parsed and validated here (Gin's form binder does not
// run the enums' JSON validation), so a malformed filter is a clean 400 rather
// than a silently-empty page.
func (s *Service) List(ctx context.Context, q *ListUsersQuery) ([]ListItem, pagination.Meta, error) {
	q.Offset.Normalize()

	filter := ListFilter{Q: q.Q}
	if q.Status != "" {
		st, err := enums.ParseUserStatus(q.Status)
		if err != nil {
			return nil, pagination.Meta{}, errs.Validation("invalid status filter")
		}
		filter.Status = st.String()
	}
	if q.Stage != "" {
		sg, err := enums.ParseUserStage(q.Stage)
		if err != nil {
			return nil, pagination.Meta{}, errs.Validation("invalid stage filter")
		}
		filter.Stage = sg.String()
	}
	if q.BranchID != "" {
		bid, err := uuid.Parse(q.BranchID)
		if err != nil {
			return nil, pagination.Meta{}, errs.Validation("branch_id must be a valid UUID")
		}
		filter.BranchID = &bid
	}

	items, total, err := s.repo.List(ctx, appctx.OrgID(ctx), filter, q.Offset)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}
	return items, pagination.BuildOffsetMeta(q.Offset, total), nil
}

// Update applies a partial change to a user and/or their profile. An empty
// request degrades to a read. It runs under a row lock (the user row) so a
// concurrent edit cannot cause a lost update; because profile edits also take the
// user lock, that single lock serializes the whole user+profile aggregate.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req *UpdateUserRequest) (*Detail, error) {
	orgID := appctx.OrgID(ctx)
	if req.IsEmpty() {
		return s.Get(ctx, id)
	}

	var out *Detail
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		u, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadErr(err)
		}

		if req.touchesUser() {
			if err := s.ensureBranchInOrg(ctx, orgID, req.BranchID); err != nil {
				return err
			}
			prevBranch := u.BranchID
			if err := applyUserPatch(u, req); err != nil {
				return err
			}
			saved, err := s.repo.UpdateUser(ctx, u)
			if err != nil {
				return mapWriteErr(err)
			}
			u = saved
			// The home branch is part of the effective branch subset carried into
			// authorizations (ADR §29). If it changed, advance the user's authz epoch
			// in the same transaction so an already-issued token whose embedded
			// branch scope is now stale cannot keep authorizing the old branch.
			if req.BranchID != nil && (prevBranch == nil || *prevBranch != *req.BranchID) {
				if err := s.bumpAuthz(ctx, id); err != nil {
					return err
				}
			}
		}

		profile, err := s.loadProfile(ctx, id)
		if err != nil {
			return err
		}
		if req.touchesProfile() {
			if profile == nil {
				// Invariant: a live user always has a profile. If it is missing we
				// cannot apply an identity patch — treat as not found.
				return errs.NotFound("user profile")
			}
			if err := applyProfilePatch(profile, req); err != nil {
				return err
			}
			saved, err := s.repo.UpdateProfile(ctx, profile)
			if err != nil {
				return mapWriteErr(err)
			}
			profile = saved
		}

		out = &Detail{User: *u, Profile: profile}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ChangeStatus transitions a user's lifecycle status. Setting the status a user
// already holds is an idempotent no-op. Moving a user into any non-login status
// (deactivated, suspended, inactive, resigned, terminated) revokes their active
// sessions in the same transaction, so access is cut immediately; if the write
// or the revocation fails, neither is applied.
func (s *Service) ChangeStatus(ctx context.Context, id uuid.UUID, req *ChangeStatusRequest) (*Detail, error) {
	orgID := appctx.OrgID(ctx)

	var out *Detail
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadErr(err)
		}

		if current.Status == req.Status {
			profile, err := s.loadProfile(ctx, id)
			if err != nil {
				return err
			}
			out = &Detail{User: *current, Profile: profile}
			return nil
		}

		saved, err := s.repo.UpdateStatus(ctx, orgID, id, req.Status)
		if err != nil {
			return mapWriteErr(err)
		}
		if !saved.Status.CanLogin() {
			if err := s.revokeSessions(ctx, id, statusReason(req)); err != nil {
				return err
			}
		}

		profile, err := s.loadProfile(ctx, id)
		if err != nil {
			return err
		}
		out = &Detail{User: *saved, Profile: profile}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete soft-deletes a user and revokes any active sessions in the same
// transaction (a removed user must not retain access). The partial unique indexes
// exclude deleted rows, so the user's email/username/employee code become
// reusable immediately.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	orgID := appctx.OrgID(ctx)

	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.FindByIDForUpdate(ctx, orgID, id); err != nil {
			return mapReadErr(err)
		}
		ok, err := s.repo.SoftDelete(ctx, orgID, id)
		if err != nil {
			return mapWriteErr(err)
		}
		if !ok {
			return errs.NotFound("user")
		}
		return s.revokeSessions(ctx, id, "user_deleted")
	})
}

// --- Role assignment (delegated to rbac) -------------------------------------
//
// These are thin façades over the rbac service: the user module surfaces the
// /users/{id}/roles routes (to keep one :id wildcard on the /users subtree) but
// the data is rbac-owned. They intentionally do NOT re-check that the user is in
// the caller's tenant first — every rbac assignment method already resolves the
// user with UserBelongsToOrg (org-scoped, excludes soft-deleted) and returns
// NotFound("user") for a missing/foreign/deleted target, so a pre-check here
// would be a second identical round-trip with no added guarantee. Keeping the
// façade lets the handler depend only on *user.Service, not on rbac directly.

// AssignRole grants a role to a user in the caller's org (tenancy enforced by rbac).
func (s *Service) AssignRole(ctx context.Context, userID uuid.UUID, req *rbac.AssignRoleRequest) (*rbac.UserRoleItem, error) {
	return s.roles.AssignRole(ctx, userID, req)
}

// RevokeRole removes a role from a user in the caller's org (tenancy enforced by rbac).
func (s *Service) RevokeRole(ctx context.Context, userID, roleID uuid.UUID) error {
	return s.roles.RevokeRole(ctx, userID, roleID)
}

// ListRoles returns the roles assigned to a user in the caller's org (tenancy enforced by rbac).
func (s *Service) ListRoles(ctx context.Context, userID uuid.UUID) ([]rbac.UserRoleItem, error) {
	return s.roles.ListRolesOfUser(ctx, userID)
}

// --- Branch assignment (multi-branch subset, ADR §19) ------------------------
//
// A user may be granted a hand-picked subset of branches to act on beyond their
// home branch. These endpoints own the write path for user_branch_assignments;
// the auth module reads that table (ListUserBranchIDs) to build the effective
// subset baked into each token. Every grant/revoke bumps the user's authz_version
// (via the bumper port) inside the same transaction, so a stale token loses the
// old scope the instant the write commits.

// AssignBranch grants the user an additional branch in the caller's org. The
// target user is resolved with a row lock (so the check and the write serialize),
// the branch is validated as belonging to the org, and — if the actor is present —
// the grant is recorded against them for the admin view. The authz bump is part
// of the same transaction, so the scope change takes effect immediately.
func (s *Service) AssignBranch(ctx context.Context, userID uuid.UUID, req *BranchAssignmentRequest) error {
	orgID := appctx.OrgID(ctx)
	now := time.Now()
	// The schema constraint requires expires_at > granted_at (= now), so a
	// past-or-equal expiry is rejected up front as bad input rather than as a 500
	// from the CHECK violation.
	if req.ExpiresAt != nil && !req.ExpiresAt.After(now) {
		return errs.Validation("expires_at must be in the future")
	}

	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.FindByIDForUpdate(ctx, orgID, userID); err != nil {
			return mapReadErr(err)
		}
		if err := s.ensureBranchInOrg(ctx, orgID, &req.BranchID); err != nil {
			return err
		}
		if err := s.repo.UpsertBranchAssignment(ctx, orgID, userID, req.BranchID, req.ExpiresAt, s.actorID(ctx), now); err != nil {
			return mapWriteErr(err)
		}
		return s.bumpAuthz(ctx, userID)
	})
}

// RevokeBranch removes the user's assignment to branchID in the caller's org. A
// grant that does not exist is NotFound; the revocation and the authz bump commit
// atomically. The user's home branch (users.branch_id) is not managed here — it is
// edited through PATCH /users/{id}.
func (s *Service) RevokeBranch(ctx context.Context, userID, branchID uuid.UUID) error {
	orgID := appctx.OrgID(ctx)

	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.FindByIDForUpdate(ctx, orgID, userID); err != nil {
			return mapReadErr(err)
		}
		ok, err := s.repo.RemoveBranchAssignment(ctx, orgID, userID, branchID)
		if err != nil {
			return mapWriteErr(err)
		}
		if !ok {
			return errs.NotFound("branch assignment")
		}
		return s.bumpAuthz(ctx, userID)
	})
}

// ListBranches returns the user's granted branches in the caller's org, after
// resolving the target user in-tenant (a missing/foreign/deleted user is NotFound).
func (s *Service) ListBranches(ctx context.Context, userID uuid.UUID) ([]BranchAssignmentItem, error) {
	orgID := appctx.OrgID(ctx)
	if _, err := s.repo.FindByID(ctx, orgID, userID); err != nil {
		return nil, mapReadErr(err)
	}
	items, err := s.repo.ListBranchAssignments(ctx, orgID, userID, time.Now())
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	return items, nil
}

// --- helpers -----------------------------------------------------------------

// ensureBranchInOrg rejects a client-supplied branch_id that is not a live branch
// of orgID. A nil branchID (unassigned) is always allowed.
func (s *Service) ensureBranchInOrg(ctx context.Context, orgID uuid.UUID, branchID *uuid.UUID) error {
	if branchID == nil {
		return nil
	}
	ok, err := s.repo.BranchBelongsToOrg(ctx, orgID, *branchID)
	if err != nil {
		return errs.DatabaseError(err)
	}
	if !ok {
		return errs.Validation("branch_id does not reference a branch in this organization")
	}
	return nil
}

// loadProfile fetches a user's profile, mapping a missing row to a nil profile
// (not an error) so reads of the rare profile-less user still succeed.
func (s *Service) loadProfile(ctx context.Context, userID uuid.UUID) (*UserProfile, error) {
	p, err := s.repo.FindProfileByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, nil
		}
		return nil, errs.DatabaseError(err)
	}
	return p, nil
}

// revokeSessions asks the injected revoker to terminate the user's sessions. When
// no revoker is wired yet (phased build before auth), it logs a warning and
// succeeds so status changes are not blocked; in production the revoker is always
// present and its failure rolls back the surrounding transaction.
func (s *Service) revokeSessions(ctx context.Context, userID uuid.UUID, reason string) error {
	if s.revoker == nil {
		s.log.Warn("no session revoker configured; user sessions not revoked",
			zap.String("user_id", userID.String()), zap.String("reason", reason))
		return nil
	}
	if err := s.revoker.RevokeUserSessions(ctx, userID, reason); err != nil {
		if errs.As(err) != nil {
			return err
		}
		return errs.Internal(err)
	}
	return nil
}

// bumpAuthz asks the injected bumper to advance the user's authorization epoch.
// When no bumper is wired yet (phased build before auth), it logs a warning and
// succeeds so branch assignments are not blocked; in production the bumper is
// always present and its failure rolls back the surrounding transaction (a grant
// that cannot invalidate stale tokens must not persist).
func (s *Service) bumpAuthz(ctx context.Context, userID uuid.UUID) error {
	if s.bumper == nil {
		s.log.Warn("no authz bumper configured; assignment persisted without an epoch bump",
			zap.String("user_id", userID.String()))
		return nil
	}
	if err := s.bumper.BumpAuthzVersion(ctx, userID); err != nil {
		if errs.As(err) != nil {
			return err
		}
		if errors.Is(err, db.ErrNoRows) {
			// The user was locked for update moments ago; a missing row here is an
			// unlikely race and surfaces as not-found rather than a 500.
			return errs.NotFound("user")
		}
		return errs.Internal(err)
	}
	return nil
}

// actorID returns the caller's user id (nil when absent) for recording who made a
// grant. appctx.UserID is Nil when no principal is on the context (e.g. a
// system-driven grant), in which case the assignment's granted_by is left NULL
// rather than storing a nil-UUID.
func (s *Service) actorID(ctx context.Context) *uuid.UUID {
	uid := appctx.UserID(ctx)
	if uid == uuid.Nil {
		return nil
	}
	return &uid
}

// applyUserPatch copies the non-nil users-table fields of a PATCH onto u. A
// joining_date sent as an empty string clears the column.
func applyUserPatch(u *User, req *UpdateUserRequest) error {
	if req.Username != nil {
		u.Username = req.Username
	}
	if req.Phone != nil {
		u.Phone = req.Phone
	}
	if req.BranchID != nil {
		u.BranchID = req.BranchID
	}
	if req.EmployeeCode != nil {
		u.EmployeeCode = req.EmployeeCode
	}
	if req.EmploymentType != nil {
		u.EmploymentType = req.EmploymentType
	}
	if req.JoiningDate != nil {
		jd, err := parseDate(req.JoiningDate)
		if err != nil {
			return err
		}
		u.JoiningDate = jd
	}
	return nil
}

// applyProfilePatch copies the non-nil profile-identity fields of a PATCH onto p.
func applyProfilePatch(p *UserProfile, req *UpdateUserRequest) error {
	if req.FirstName != nil {
		p.FirstName = *req.FirstName
	}
	if req.LastName != nil {
		p.LastName = *req.LastName
	}
	if req.MiddleName != nil {
		p.MiddleName = req.MiddleName
	}
	if req.DisplayName != nil {
		p.DisplayName = req.DisplayName
	}
	if req.Gender != nil {
		p.Gender = req.Gender
	}
	if req.AvatarURL != nil {
		p.AvatarURL = req.AvatarURL
	}
	if req.DateOfBirth != nil {
		dob, err := parseDate(req.DateOfBirth)
		if err != nil {
			return err
		}
		p.DateOfBirth = dob
	}
	return nil
}

// parseDate parses an optional "YYYY-MM-DD" string into a *time.Time. nil or ""
// yields a nil time (absent / cleared). The DTO already validates the format, so
// a parse error here is defensive.
func parseDate(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, *s)
	if err != nil {
		return nil, errs.Validation("invalid date; expected format YYYY-MM-DD").WithCause(err)
	}
	return &t, nil
}

// statusReason derives an audit reason for a status change, preferring the
// caller-supplied note.
func statusReason(req *ChangeStatusRequest) string {
	if req.Reason != nil && *req.Reason != "" {
		return *req.Reason
	}
	return "status_changed_to_" + req.Status.String()
}

// mapReadErr maps a repository read error: missing row → NotFound, else DB error.
func mapReadErr(err error) error {
	if errors.Is(err, db.ErrNoRows) {
		return errs.NotFound("user")
	}
	return errs.DatabaseError(err)
}

// mapWriteErr maps a repository write error to a domain error, naming the exact
// field for a unique violation via the violated constraint's name so the client
// learns which value collided.
func mapWriteErr(err error) error {
	switch {
	case errors.Is(err, db.ErrNoRows):
		return errs.NotFound("user")
	case db.IsUniqueVilation(err):
		switch db.ConstraintName(err) {
		case "ux_users_email":
			return errs.AlreadyExists("email")
		case "ux_users_username":
			return errs.AlreadyExists("username")
		case "ux_users_emp_code":
			return errs.AlreadyExists("employee code")
		case "user_profiles_user_id_key":
			return errs.AlreadyExists("user profile")
		default:
			return errs.AlreadyExists("user")
		}
	case db.IsForeignKeyViolation(err):
		// A referenced branch/organization does not exist. Branch is pre-checked,
		// so this is a rare race; report it as bad input rather than a 500.
		return errs.Validation("a referenced entity does not exist")
	default:
		return errs.DatabaseError(err)
	}
}

// derefStatus returns *p if non-nil, otherwise def.
func derefStatus(p *enums.UserStatus, def enums.UserStatus) enums.UserStatus {
	if p != nil {
		return *p
	}
	return def
}

// derefStage returns *p if non-nil, otherwise def.
func derefStage(p *enums.UserStage, def enums.UserStage) enums.UserStage {
	if p != nil {
		return *p
	}
	return def
}

// derefBool returns *p if non-nil, otherwise def.
func derefBool(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}
