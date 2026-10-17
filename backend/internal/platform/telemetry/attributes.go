package telemetry

import "go.opentelemetry.io/otel/attribute"

// Standardized attribute keys used across HTTP, DB, Redis, AI, and business
// spans/logs. Keep this list append-only — never repurpose an existing key,
// since dashboards and alerts may already depend on it.
const (
	AttrDBSystem     = attribute.Key("db.system")
	AttrDBOperation  = attribute.Key("db.operation")
	// AttrDBStatement must only ever hold a sanitized, parameter-free
	// string (compactSQL from db/hooks.go) — never raw bind params, which
	// can carry PII (email, phone, NID).
	AttrDBStatement = attribute.Key("db.statement")

	AttrRedisOperation = attribute.Key("redis.operation")

	AttrMailerProvider = attribute.Key("mailer.provider")

	AttrAIProvider = attribute.Key("ai.provider")
	AttrAIEndpoint = attribute.Key("ai.endpoint")

	AttrOrgID     = attribute.Key("app.org_id")
	AttrBranchID  = attribute.Key("app.branch_id")
	AttrUserID    = attribute.Key("app.user_id")
	AttrRequestID = attribute.Key("app.request_id")

	// Backup module attributes.
	AttrBackupOperation = attribute.Key("backup.operation") // pg_dump | encryption | storage | verification | restore_drill
	AttrBackupOutcome   = attribute.Key("backup.outcome")   // success | error | rejected
	AttrBackupTrigger   = attribute.Key("backup.trigger")   // manual | scheduled
	AttrBackupStatus    = attribute.Key("backup.status")    // queued | in_progress | succeeded | failed | cancelled | expired
	AttrBackupType      = attribute.Key("backup.type")      // full
	AttrBackupVerifyType = attribute.Key("backup.verification_type") // integrity | restore_drill
	AttrStorageDriver   = attribute.Key("storage.driver")   // local | s3
	AttrStorageOp       = attribute.Key("storage.op")       // put | open | head | delete | download_auth
)
