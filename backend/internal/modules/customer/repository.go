package customer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

var (
	ErrNotFound      = errors.New("customer not found")
	ErrDuplicateCode = errors.New("customer code already exists")
	ErrConflict      = errors.New("customer version conflict")
	ErrForbidden     = errors.New("customer access forbidden")
)

// Repository is the customer module's data-access layer.
// Every method is organization-scoped and enforces tenant isolation.
// Visibility is enforced separately by the service layer using customer_branch_visibility.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

const customerColumns = `id, organization_id, code, name, phone, email, address, city, 
	date_of_birth, gender, blood_group, loyalty_points, credit_limit, current_due, 
	notes, is_active, created_at, updated_at, deleted_at`

func scanCustomer(row pgx.Row) (*Customer, error) {
	var c Customer
	if err := row.Scan(
		&c.ID, &c.OrganizationID, &c.Code, &c.Name, &c.Phone, &c.Email,
		&c.Address, &c.City, &c.DateOfBirth, &c.Gender, &c.BloodGroup,
		&c.LoyaltyPoints, &c.CreditLimit, &c.CurrentDue, &c.Notes,
		&c.IsActive, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

// Insert creates a customer record. The service must ensure organization_id,
// code (if generated), and defaults are set before calling this method.
// Must be called within a transaction when creating with initial visibility.
func (r *Repository) Insert(ctx context.Context, c *Customer) (*Customer, error) {
	const q = `INSERT INTO customers (
			organization_id, code, name, phone, email, address, city,
			date_of_birth, gender, blood_group, loyalty_points, 
			credit_limit, current_due, notes, is_active
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING ` + customerColumns

	row := r.db.FromCtx(ctx).QueryRow(ctx, q,
		c.OrganizationID, c.Code, c.Name, c.Phone, c.Email, c.Address, c.City,
		c.DateOfBirth, c.Gender, c.BloodGroup, c.LoyaltyPoints,
		c.CreditLimit, c.CurrentDue, c.Notes, c.IsActive,
	)

	res, err := scanCustomer(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "code") {
				return nil, ErrDuplicateCode
			}
		}
		return nil, err
	}
	return res, nil
}

// FindByID returns a non-deleted customer scoped to orgID.
// Returns db.ErrNoRows if not found, deleted, or belongs to different organization.
func (r *Repository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*Customer, error) {
	const q = `SELECT ` + customerColumns + `
		FROM customers
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`

	return scanCustomer(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// FindByIDForUpdate returns a non-deleted customer scoped to orgID with row lock.
// Must be used inside a transaction for updates.
func (r *Repository) FindByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*Customer, error) {
	const q = `SELECT ` + customerColumns + `
		FROM customers
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		FOR UPDATE`

	return scanCustomer(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// FindByCode returns a non-deleted customer by code scoped to orgID.
func (r *Repository) FindByCode(ctx context.Context, orgID uuid.UUID, code string) (*Customer, error) {
	const q = `SELECT ` + customerColumns + `
		FROM customers
		WHERE organization_id = $1 AND code = $2 AND deleted_at IS NULL`

	return scanCustomer(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, code))
}

// Update updates allow-listed fields on a customer within orgID.
// This method only updates safe profile fields - financial fields must not be updated here.
func (r *Repository) Update(ctx context.Context, orgID uuid.UUID, c *Customer) (*Customer, error) {
	const q = `UPDATE customers SET
			name = $3,
			phone = $4,
			email = $5,
			address = $6,
			city = $7,
			date_of_birth = $8,
			gender = $9,
			blood_group = $10,
			notes = $11,
			updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING ` + customerColumns

	row := r.db.FromCtx(ctx).QueryRow(ctx, q,
		c.ID, orgID, c.Name, c.Phone, c.Email, c.Address, c.City,
		c.DateOfBirth, c.Gender, c.BloodGroup, c.Notes,
	)

	res, err := scanCustomer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return res, nil
}

// SoftDelete archives a customer (soft delete) within orgID.
// Returns true if a row was affected. Does not cascade delete posted references.
func (r *Repository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	const q = `UPDATE customers SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`

	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Reactivate reactivates an archived customer within orgID.
func (r *Repository) Reactivate(ctx context.Context, orgID, id uuid.UUID) (*Customer, error) {
	const q = `UPDATE customers SET deleted_at = NULL, updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NOT NULL
		RETURNING ` + customerColumns

	row := r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID)
	res, err := scanCustomer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return res, nil
}

// List returns customers visible to the given branch set within orgID.
// Branch visibility is enforced via customer_branch_visibility join.
// q searches name, phone, email (case-insensitive). Results are paginated.
func (r *Repository) List(ctx context.Context, orgID uuid.UUID, branchIDs []uuid.UUID, q string, isActive *bool, p pagination.Offset) ([]*Customer, int64, error) {
	if len(branchIDs) == 0 {
		return []*Customer{}, 0, nil
	}

	p.Normalize()

	var (
		args     []any
		clauses  []string
		idx      = 1
	)

	clauses = append(clauses, `c.organization_id = $`+fmt.Sprint(idx))
	args = append(args, orgID)
	idx++

	clauses = append(clauses, `c.deleted_at IS NULL`)
	clauses = append(clauses, `EXISTS (
		SELECT 1 FROM customer_branch_visibility cbv 
		WHERE cbv.customer_id = c.id 
		AND cbv.organization_id = c.organization_id 
		AND cbv.branch_id = ANY($`+fmt.Sprint(idx)+`)
	)`)
	args = append(args, branchIDs)
	idx++

	if isActive != nil {
		clauses = append(clauses, `c.is_active = $`+fmt.Sprint(idx))
		args = append(args, *isActive)
		idx++
	}

	if strings.TrimSpace(q) != "" {
		search := "%" + strings.ToLower(strings.TrimSpace(q)) + "%"
		clauses = append(clauses, `(LOWER(c.name) LIKE $`+fmt.Sprint(idx)+` OR LOWER(COALESCE(c.phone,'')) LIKE $`+fmt.Sprint(idx)+` OR LOWER(COALESCE(c.email,'')) LIKE $`+fmt.Sprint(idx)+`)`)
		args = append(args, search)
		idx++
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	// Count
	countQ := `SELECT COUNT(DISTINCT c.id) FROM customers c ` + where
	var total int64
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// List
	listQ := `SELECT DISTINCT ` + "c." + strings.ReplaceAll(customerColumns, ", ", ", c.") + `
		FROM customers c
		` + where + `
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT $` + fmt.Sprint(idx) + ` OFFSET $` + fmt.Sprint(idx+1)

	args = append(args, p.Limit, p.OffsetSQL())
	idx += 2

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var customers []*Customer
	for rows.Next() {
		var c Customer
		if err := rows.Scan(
			&c.ID, &c.OrganizationID, &c.Code, &c.Name, &c.Phone, &c.Email,
			&c.Address, &c.City, &c.DateOfBirth, &c.Gender, &c.BloodGroup,
			&c.LoyaltyPoints, &c.CreditLimit, &c.CurrentDue, &c.Notes,
			&c.IsActive, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
		); err != nil {
			return nil, 0, err
		}
		customers = append(customers, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return customers, total, nil
}

// HasBranchVisibility checks if a customer is visible to any of the given branches.
func (r *Repository) HasBranchVisibility(ctx context.Context, orgID, customerID uuid.UUID, branchIDs []uuid.UUID) (bool, error) {
	if len(branchIDs) == 0 {
		return false, nil
	}

	const q = `SELECT EXISTS (
		SELECT 1 FROM customer_branch_visibility
		WHERE organization_id = $1 AND customer_id = $2 AND branch_id = ANY($3)
	)`

	var exists bool
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, customerID, branchIDs).Scan(&exists)
	return exists, err
}

// AddBranchVisibility grants visibility to branches for a customer.
// Must be called within a transaction. Ignores duplicates (ON CONFLICT DO NOTHING).
func (r *Repository) AddBranchVisibility(ctx context.Context, orgID, customerID uuid.UUID, branchIDs []uuid.UUID, createdBy *uuid.UUID) error {
	if len(branchIDs) == 0 {
		return nil
	}

	const q = `INSERT INTO customer_branch_visibility (organization_id, customer_id, branch_id, created_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (customer_id, branch_id) DO NOTHING`

	batch := &pgx.Batch{}
	for _, bid := range branchIDs {
		batch.Queue(q, orgID, customerID, bid, createdBy)
	}

	br := r.db.FromCtx(ctx).SendBatch(ctx, batch)
	defer br.Close()

	for range branchIDs {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// RemoveBranchVisibility revokes visibility for branches.
func (r *Repository) RemoveBranchVisibility(ctx context.Context, orgID, customerID uuid.UUID, branchIDs []uuid.UUID) error {
	if len(branchIDs) == 0 {
		return nil
	}

	const q = `DELETE FROM customer_branch_visibility
		WHERE organization_id = $1 AND customer_id = $2 AND branch_id = ANY($3)`

	_, err := r.db.FromCtx(ctx).Exec(ctx, q, orgID, customerID, branchIDs)
	return err
}

// GetBranchVisibility returns all branch IDs with visibility to the customer.
func (r *Repository) GetBranchVisibility(ctx context.Context, orgID, customerID uuid.UUID) ([]uuid.UUID, error) {
	const q = `SELECT branch_id FROM customer_branch_visibility
		WHERE organization_id = $1 AND customer_id = $2
		ORDER BY branch_id`

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, orgID, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var branchIDs []uuid.UUID
	for rows.Next() {
		var bid uuid.UUID
		if err := rows.Scan(&bid); err != nil {
			return nil, err
		}
		branchIDs = append(branchIDs, bid)
	}
	return branchIDs, rows.Err()
}

// InsertConsent appends a consent record (append-only). Must be called in transaction.
func (r *Repository) InsertConsent(ctx context.Context, orgID, customerID uuid.UUID, purpose, channel, decision string, actorID *uuid.UUID, evidence *string, expiresAt *time.Time) (*ConsentItem, error) {
	const q = `INSERT INTO customer_consents (organization_id, customer_id, purpose, channel, decision, actor_id, evidence, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, purpose, channel, decision, recorded_at, actor_id, expires_at`

	var item ConsentItem
	err := r.db.FromCtx(ctx).QueryRow(ctx, q, orgID, customerID, purpose, channel, decision, actorID, evidence, expiresAt).
		Scan(&item.ID, &item.Purpose, &item.Channel, &item.Decision, &item.RecordedAt, &item.ActorID, &item.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// GetLatestConsent returns the most recent consent for (customer, purpose, channel).
func (r *Repository) GetLatestConsent(ctx context.Context, orgID, customerID uuid.UUID, purpose, channel string) (*ConsentItem, error) {
	const q = `SELECT id, purpose, channel, decision, recorded_at, actor_id, expires_at
		FROM customer_consents
		WHERE organization_id = $1 AND customer_id = $2 AND purpose = $3 AND channel = $4
		ORDER BY recorded_at DESC
		LIMIT 1`

	var item ConsentItem
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, customerID, purpose, channel).
		Scan(&item.ID, &item.Purpose, &item.Channel, &item.Decision, &item.RecordedAt, &item.ActorID, &item.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

// ListConsents returns consent history for a customer (newest first).
func (r *Repository) ListConsents(ctx context.Context, orgID, customerID uuid.UUID, limit int) ([]*ConsentItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	const q = `SELECT id, purpose, channel, decision, recorded_at, actor_id, expires_at
		FROM customer_consents
		WHERE organization_id = $1 AND customer_id = $2
		ORDER BY recorded_at DESC
		LIMIT $3`

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, orgID, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*ConsentItem
	for rows.Next() {
		var item ConsentItem
		if err := rows.Scan(&item.ID, &item.Purpose, &item.Channel, &item.Decision, &item.RecordedAt, &item.ActorID, &item.ExpiresAt); err != nil {
			return nil, err
		}
		items = append(items, &item)
	}
	return items, rows.Err()
}

// ExistsByCodeInOrg checks if a code already exists in the organization (excluding deleted).
func (r *Repository) ExistsByCodeInOrg(ctx context.Context, orgID uuid.UUID, code string) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM customers WHERE organization_id = $1 AND code = $2 AND deleted_at IS NULL)`

	var exists bool
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, code).Scan(&exists)
	return exists, err
}
