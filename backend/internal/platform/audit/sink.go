package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/middleware"
	"backend/internal/platform/db"
)

const insertAudit = `INSERT INTO audit_logs (
	id, created_at, organization_id, branch_id, user_id, role_name, session_id,
	request_id, module, action, ip, user_agent, outcome, error_code, duration_ms, details
) VALUES (
	$1, $2, $3, $4, $5, $6, $7,
	$8, $9, $10, $11, $12, $13, $14, $15, $16
)`

// PostgresAuditSink writes AuditEntry records to the durable audit_logs
// table. It is the production replacement for the nopAuditSink wired into
// the middleware container at startup.
type PostgresAuditSink struct {
	db     *db.DB
	log    *zap.Logger
	clock  func() time.Time
}

// NewPostgresAuditSink builds the sink. database may be nil — in that case
// Record is a no-op (test-friendly).
func NewPostgresAuditSink(database *db.DB, log *zap.Logger) *PostgresAuditSink {
	if log == nil {
		log = zap.NewNop()
	}
	return &PostgresAuditSink{db: database, log: log, clock: time.Now}
}

// Record satisfies the middleware.AuditSink interface. It maps the
// in-memory AuditEntry onto the audit_logs schema and performs a single
// INSERT via the request context (so it participates in any active
// transaction). Errors are logged and swallowed — a transient DB outage
// must never break the request that just succeeded.
func (s *PostgresAuditSink) Record(ctx context.Context, entry middleware.AuditEntry) {
	if s == nil || s.db == nil {
		return
	}
	details, err := json.Marshal(map[string]any{
		"method": entry.Method,
		"route":  entry.Route,
		"path":   entry.Path,
		"module": entry.Module,
		"action": entry.Action,
	})
	if err != nil {
		details = []byte(`{}`)
	}
	_, dbErr := s.db.FromCtx(ctx).Exec(ctx, insertAudit,
		uuid.New(),
		s.clock().UTC(),
		entry.OrgID,
		entry.BranchID,
		entry.UserID,
		entry.RoleName,
		entry.SessionID,
		entry.RequestID,
		entry.Module,
		entry.Action,
		entry.ClientIP,
		entry.UserAgent,
		outcomeFor(entry.Success),
		nullIfEmpty(entry.ErrorCode),
		entry.LatencyMS,
		details,
	)
	if dbErr != nil {
		s.log.Warn("audit write failed", zap.Error(dbErr))
	}
}

func outcomeFor(success bool) string {
	if success {
		return "success"
	}
	return "failure"
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Ensure PostgresAuditSink satisfies the port at compile time.
var _ middleware.AuditSink = (*PostgresAuditSink)(nil)
