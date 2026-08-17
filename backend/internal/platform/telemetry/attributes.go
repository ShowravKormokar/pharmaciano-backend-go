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

	AttrAIProvider = attribute.Key("ai.provider")
	AttrAIEndpoint = attribute.Key("ai.endpoint")

	AttrOrgID     = attribute.Key("app.org_id")
	AttrBranchID  = attribute.Key("app.branch_id")
	AttrUserID    = attribute.Key("app.user_id")
	AttrRequestID = attribute.Key("app.request_id")
)
