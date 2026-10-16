package audit

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	defaultAuditPageSize          = 50
	maxAuditPageSize              = 200
	maxAuditCursorBytes           = 512
	maxAuditCursorTextSize        = 1024
	auditCursorSignatureHexLength = 64
	maxAuditQueryRunes            = 120
	minAuditQueryRunes            = 3
	maxAuditRange                 = 90 * 24 * time.Hour
	maxAuditPayloadBytes          = 16 << 10
)

// AuditSearchFilters contains client-selectable event filters. Tenant and
// branch scope are intentionally absent: the service must derive them from
// authenticated authorization context and apply them to every query.
type AuditSearchFilters struct {
	UserID     string `form:"user_id" json:"user_id,omitempty"`
	Module     string `form:"module" json:"module,omitempty"`
	Action     string `form:"action" json:"action,omitempty"`
	EntityType string `form:"entity_type" json:"entity_type,omitempty"`
	EntityID   string `form:"entity_id" json:"entity_id,omitempty"`
	Outcome    string `form:"outcome" json:"outcome,omitempty"`
	From       string `form:"from" json:"from"`
	To         string `form:"to" json:"to"`
	IP         string `form:"ip" json:"ip,omitempty"`
	Q          string `form:"q" json:"q,omitempty"`

	fromTime time.Time
	toTime   time.Time
	userID   *uuid.UUID
	entityID *uuid.UUID
	ip       *net.IP
}

// NormalizeAndValidate normalizes filters and validates a bounded, explicit
// occurrence-time window. Call it before using the parsed accessors.
func (f *AuditSearchFilters) NormalizeAndValidate() []error {
	if f == nil {
		return []error{auditFieldError("query", "required", "query is required")}
	}
	var errs []error

	f.UserID = strings.TrimSpace(f.UserID)
	f.Module = strings.TrimSpace(f.Module)
	f.Action = strings.TrimSpace(f.Action)
	f.EntityType = strings.TrimSpace(f.EntityType)
	f.EntityID = strings.TrimSpace(f.EntityID)
	f.Outcome = strings.TrimSpace(f.Outcome)
	f.From = strings.TrimSpace(f.From)
	f.To = strings.TrimSpace(f.To)
	f.IP = strings.TrimSpace(f.IP)
	f.Q = strings.TrimSpace(f.Q)

	f.userID = nil
	if f.UserID != "" {
		id, err := uuid.Parse(f.UserID)
		if err != nil || id == uuid.Nil {
			errs = append(errs, auditFieldError("user_id", "uuid", "must be a valid non-zero UUID"))
		} else {
			f.userID = &id
			f.UserID = id.String()
		}
	}
	f.entityID = nil
	if f.EntityID != "" {
		id, err := uuid.Parse(f.EntityID)
		if err != nil || id == uuid.Nil {
			errs = append(errs, auditFieldError("entity_id", "uuid", "must be a valid non-zero UUID"))
		} else {
			f.entityID = &id
			f.EntityID = id.String()
		}
	}

	validateFilterText := func(field, value string, maxBytes int) {
		if len(value) > maxBytes {
			errs = append(errs, auditFieldError(field, "max", "exceeds the maximum length"))
		}
		if !utf8.ValidString(value) {
			errs = append(errs, auditFieldError(field, "utf8", "must contain valid UTF-8"))
		}
		if hasControlCharacter(value) {
			errs = append(errs, auditFieldError(field, "characters", "contains unsupported control characters"))
		}
	}
	validateFilterText("module", f.Module, 60)
	validateFilterText("action", f.Action, 40)
	validateFilterText("entity_type", f.EntityType, 60)

	if f.Outcome != "" && !AuditOutcome(f.Outcome).Valid() {
		errs = append(errs, auditFieldError("outcome", "oneof", "must be success or failure"))
	}

	f.ip = nil
	if f.IP != "" {
		parsed := net.ParseIP(f.IP)
		if parsed == nil {
			errs = append(errs, auditFieldError("ip", "ip", "must be a valid IP address"))
		} else {
			canonical := parsed.String()
			f.IP = canonical
			f.ip = &parsed
		}
	}

	if f.Q != "" {
		if !utf8.ValidString(f.Q) {
			errs = append(errs, auditFieldError("q", "utf8", "must contain valid UTF-8"))
		} else {
			runes := []rune(f.Q)
			if len(runes) < minAuditQueryRunes {
				errs = append(errs, auditFieldError("q", "min", "must contain at least 3 characters"))
			}
			if len(runes) > maxAuditQueryRunes {
				errs = append(errs, auditFieldError("q", "max", "exceeds the maximum search length"))
			}
			if hasControlCharacter(f.Q) {
				errs = append(errs, auditFieldError("q", "characters", "contains unsupported control characters"))
			}
		}
	}

	f.fromTime = time.Time{}
	f.toTime = time.Time{}
	fromTime, fromErr := parseAuditTime("from", f.From)
	toTime, toErr := parseAuditTime("to", f.To)
	if fromErr != nil {
		errs = append(errs, fromErr)
	} else {
		f.fromTime = fromTime
	}
	if toErr != nil {
		errs = append(errs, toErr)
	} else {
		f.toTime = toTime
	}
	if fromErr == nil && toErr == nil {
		if !fromTime.Before(toTime) {
			errs = append(errs, auditFieldError("from", "before", "must be earlier than to"))
		} else if toTime.Sub(fromTime) > maxAuditRange {
			errs = append(errs, auditFieldError("from", "range", "time window must not exceed 90 days"))
		} else {
			f.From = fromTime.Format(time.RFC3339Nano)
			f.To = toTime.Format(time.RFC3339Nano)
		}
	}

	return errs
}

// ParsedUserID returns the UUID parsed by NormalizeAndValidate.
func (f AuditSearchFilters) ParsedUserID() *uuid.UUID { return cloneUUID(f.userID) }

// ParsedEntityID returns the UUID parsed by NormalizeAndValidate.
func (f AuditSearchFilters) ParsedEntityID() *uuid.UUID { return cloneUUID(f.entityID) }

// ParsedIP returns the parsed IP address, if an IP filter was supplied.
func (f AuditSearchFilters) ParsedIP() net.IP {
	if f.ip == nil {
		return nil
	}
	return append(net.IP(nil), (*f.ip)...)
}

// TimeRange returns the normalized inclusive-from/exclusive-to time window.
// The returned values are valid only after NormalizeAndValidate succeeds.
func (f AuditSearchFilters) TimeRange() (time.Time, time.Time) {
	return f.fromTime, f.toTime
}

// ListAuditLogsQuery binds search filters and cursor pagination for the audit
// list endpoint. Ordering is fixed by the repository (created_at DESC, id DESC).
type ListAuditLogsQuery struct {
	AuditSearchFilters
	Cursor string `form:"cursor" json:"cursor,omitempty"`
	Limit  int    `form:"limit" json:"limit,omitempty"`
}

// NormalizeAndValidate validates all filters and applies the default page size.
func (q *ListAuditLogsQuery) NormalizeAndValidate() []error {
	if q == nil {
		return []error{auditFieldError("query", "required", "query is required")}
	}
	errs := q.AuditSearchFilters.NormalizeAndValidate()
	if q.Limit == 0 {
		q.Limit = defaultAuditPageSize
	} else if q.Limit < 1 || q.Limit > maxAuditPageSize {
		errs = append(errs, auditFieldError("limit", "range", "must be between 1 and 200"))
	}
	q.Cursor = strings.TrimSpace(q.Cursor)
	if q.Cursor != "" {
		if len(q.Cursor) > maxAuditCursorTextSize {
			errs = append(errs, auditFieldError("cursor", "max", "cursor exceeds the maximum length"))
		} else if !validCursorEncoding(q.Cursor) {
			errs = append(errs, auditFieldError("cursor", "format", "cursor format is invalid"))
		}
	}
	return errs
}

// CreateAuditExportRequest is the validated body for creating an asynchronous
// CSV export. Scope is never accepted here; it must be derived from the caller.
type CreateAuditExportRequest struct {
	AuditSearchFilters
	Format string `json:"format"`
}

// Validate validates the export format and bounded search filters.
func (r *CreateAuditExportRequest) Validate() []error {
	if r == nil {
		return []error{auditFieldError("request", "required", "request is required")}
	}
	errs := r.AuditSearchFilters.NormalizeAndValidate()
	r.Format = strings.ToLower(strings.TrimSpace(r.Format))
	if r.Format != "csv" {
		errs = append(errs, auditFieldError("format", "oneof", "only csv exports are supported"))
	}
	return errs
}

// AuditLogListItem is the deliberately limited projection returned in search
// results. JSON details and before/after snapshots are available only through
// the separately authorized detail path.
type AuditLogListItem struct {
	ID             uuid.UUID    `json:"id"`
	OccurredAt     time.Time    `json:"occurred_at"`
	OrganizationID *uuid.UUID   `json:"organization_id,omitempty"`
	BranchID       *uuid.UUID   `json:"branch_id,omitempty"`
	UserID         *uuid.UUID   `json:"user_id,omitempty"`
	RoleName       *string      `json:"role_name,omitempty"`
	SessionID      *uuid.UUID   `json:"session_id,omitempty"`
	RequestID      *string      `json:"request_id,omitempty"`
	Module         string       `json:"module"`
	Action         string       `json:"action"`
	EntityType     *string      `json:"entity_type,omitempty"`
	EntityID       *uuid.UUID   `json:"entity_id,omitempty"`
	Outcome        AuditOutcome `json:"outcome"`
	ErrorCode      *string      `json:"error_code,omitempty"`
	DurationMS     *int32       `json:"duration_ms,omitempty"`
}

// AuditLogListResponse is a bounded cursor-paginated result.
type AuditLogListResponse struct {
	Items      []AuditLogListItem `json:"items"`
	HasMore    bool               `json:"has_more"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

// AuditLogDetailResponse exposes only an explicit metadata projection plus
// payloads that have already passed service-layer redaction and authorization.
type AuditLogDetailResponse struct {
	AuditLogListItem
	IP         *string         `json:"ip,omitempty"`
	Browser    *string         `json:"browser,omitempty"`
	OS         *string         `json:"os,omitempty"`
	Device     *string         `json:"device,omitempty"`
	Location   *string         `json:"location,omitempty"`
	UserAgent  *string         `json:"user_agent,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
	BeforeData json.RawMessage `json:"before_data,omitempty"`
	AfterData  json.RawMessage `json:"after_data,omitempty"`
}

// RedactedAuditPayload contains only JSON values already allowlisted/redacted
// by the service. Do not populate it directly from a database row.
type RedactedAuditPayload struct {
	Details    json.RawMessage
	BeforeData json.RawMessage
	AfterData  json.RawMessage
}

// ToAuditLogListItem maps persistence data into the safe list projection.
func ToAuditLogListItem(log AuditLog) AuditLogListItem {
	return AuditLogListItem{
		ID: log.ID, OccurredAt: log.OccurredAt,
		OrganizationID: cloneUUID(log.OrganizationID), BranchID: cloneUUID(log.BranchID),
		UserID: cloneUUID(log.UserID), RoleName: cloneString(log.RoleName),
		SessionID: cloneUUID(log.SessionID), RequestID: cloneString(log.RequestID),
		Module: log.Module, Action: log.Action, EntityType: cloneString(log.EntityType),
		EntityID: cloneUUID(log.EntityID), Outcome: log.Outcome,
		ErrorCode: cloneString(log.ErrorCode), DurationMS: cloneInt32(log.DurationMS),
	}
}

// ToAuditLogDetailResponse builds the detail response projection. Payload
// validation bounds bytes and rejects malformed or non-object JSON, but the
// handler/service must authorize the read and redact data before calling it.
func ToAuditLogDetailResponse(log AuditLog, payload RedactedAuditPayload) (AuditLogDetailResponse, error) {
	details, err := validateAuditPayload("details", payload.Details)
	if err != nil {
		return AuditLogDetailResponse{}, err
	}
	before, err := validateAuditPayload("before_data", payload.BeforeData)
	if err != nil {
		return AuditLogDetailResponse{}, err
	}
	after, err := validateAuditPayload("after_data", payload.AfterData)
	if err != nil {
		return AuditLogDetailResponse{}, err
	}
	return AuditLogDetailResponse{
		AuditLogListItem: ToAuditLogListItem(log),
		IP:               cloneString(log.IP), Browser: cloneString(log.Browser), OS: cloneString(log.OS),
		Device: cloneString(log.Device), Location: cloneString(log.Location),
		UserAgent: cloneString(log.UserAgent),
		Details:   details, BeforeData: before, AfterData: after,
	}, nil
}

// AuditValuesResponse returns authorized module/action values without exposing
// an unbounded historical event list.
type AuditValuesResponse struct {
	Items []string `json:"items"`
}

// AuditValuesQuery bounds metadata-value lookups to an explicit occurrence
// window and limits the distinct values returned.
type AuditValuesQuery struct {
	From  string `form:"from" json:"from"`
	To    string `form:"to" json:"to"`
	Limit int    `form:"limit" json:"limit,omitempty"`

	fromTime time.Time
	toTime   time.Time
}

// AuditEventLookupQuery supplies the partition key required for efficient and
// unambiguous lookups in the time-partitioned audit_logs table.
type AuditEventLookupQuery struct {
	OccurredAt string `form:"occurred_at" json:"occurred_at"`

	occurredAt time.Time
}

// NormalizeAndValidate requires an RFC3339 occurrence timestamp.
func (q *AuditEventLookupQuery) NormalizeAndValidate() []error {
	if q == nil {
		return []error{auditFieldError("query", "required", "query is required")}
	}
	q.OccurredAt = strings.TrimSpace(q.OccurredAt)
	occurredAt, err := parseAuditTime("occurred_at", q.OccurredAt)
	if err != nil {
		return []error{err}
	}
	q.occurredAt = occurredAt
	q.OccurredAt = occurredAt.Format(time.RFC3339Nano)
	return nil
}

// PartitionKey returns the validated UTC occurrence timestamp.
func (q AuditEventLookupQuery) PartitionKey() time.Time {
	return q.occurredAt
}

// NormalizeAndValidate requires a bounded time range for partition pruning.
func (q *AuditValuesQuery) NormalizeAndValidate() []error {
	if q == nil {
		return []error{auditFieldError("query", "required", "query is required")}
	}
	var errs []error
	q.From = strings.TrimSpace(q.From)
	q.To = strings.TrimSpace(q.To)
	q.fromTime = time.Time{}
	q.toTime = time.Time{}
	from, fromErr := parseAuditTime("from", q.From)
	to, toErr := parseAuditTime("to", q.To)
	if fromErr != nil {
		errs = append(errs, fromErr)
	} else {
		q.fromTime = from
	}
	if toErr != nil {
		errs = append(errs, toErr)
	} else {
		q.toTime = to
	}
	if fromErr == nil && toErr == nil {
		if !from.Before(to) {
			errs = append(errs, auditFieldError("from", "before", "must be earlier than to"))
		} else if to.Sub(from) > maxAuditRange {
			errs = append(errs, auditFieldError("from", "range", "time window must not exceed 90 days"))
		} else {
			q.From = from.Format(time.RFC3339Nano)
			q.To = to.Format(time.RFC3339Nano)
		}
	}
	if q.Limit == 0 {
		q.Limit = defaultAuditValuesLimit
	} else if q.Limit < 1 || q.Limit > maxAuditValuesLimit {
		errs = append(errs, auditFieldError("limit", "range", "must be between 1 and 500"))
	}
	return errs
}

// TimeRange returns the validated UTC window.
func (q AuditValuesQuery) TimeRange() (time.Time, time.Time) {
	return q.fromTime, q.toTime
}

// AuditExportStatus is the externally visible state of an asynchronous export.
type AuditExportStatus string

const (
	AuditExportStatusQueued    AuditExportStatus = "queued"
	AuditExportStatusRunning   AuditExportStatus = "running"
	AuditExportStatusCompleted AuditExportStatus = "completed"
	AuditExportStatusFailed    AuditExportStatus = "failed"
)

// Valid reports whether the status is part of the export response contract.
func (s AuditExportStatus) Valid() bool {
	switch s {
	case AuditExportStatusQueued, AuditExportStatusRunning,
		AuditExportStatusCompleted, AuditExportStatusFailed:
		return true
	default:
		return false
	}
}

// AuditExportAcceptedResponse acknowledges a queued export without returning a
// download URL or artifact reference.
type AuditExportAcceptedResponse struct {
	ID        uuid.UUID         `json:"id"`
	Status    AuditExportStatus `json:"status"`
	CreatedAt time.Time         `json:"created_at"`
}

type auditDTOFieldError struct {
	Field   string
	Rule    string
	Message string
}

func (e *auditDTOFieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func auditFieldError(field, rule, message string) error {
	return &auditDTOFieldError{Field: field, Rule: rule, Message: message}
}

// AuditValidationErrorFields extracts safe field/rule/message details from DTO
// errors without echoing sensitive query or cursor values.
func AuditValidationErrorFields(err error) (field, rule, message string, ok bool) {
	var target *auditDTOFieldError
	if !errors.As(err, &target) {
		return "", "", "", false
	}
	return target.Field, target.Rule, target.Message, true
}

func parseAuditTime(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, auditFieldError(field, "required", "must be a valid RFC3339 timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, auditFieldError(field, "datetime", "must be a valid RFC3339 timestamp")
	}
	return t.UTC(), nil
}

func validCursorEncoding(cursor string) bool {
	if cursor == "" {
		return true
	}
	encodedPayload, signature, found := strings.Cut(cursor, ".")
	if !found || len(signature) != auditCursorSignatureHexLength {
		return false
	}
	if _, err := hex.DecodeString(signature); err != nil {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	return err == nil && len(decoded) > 0 && len(decoded) <= maxAuditCursorBytes
}

func validateAuditPayload(field string, payload json.RawMessage) (json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	if len(payload) > maxAuditPayloadBytes {
		return nil, auditFieldError(field, "max_size", "redacted payload exceeds the maximum size")
	}
	if !json.Valid(payload) {
		return nil, auditFieldError(field, "json", "redacted payload is not valid JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, auditFieldError(field, "object", "redacted payload must be a JSON object")
	}
	return append(json.RawMessage(nil), payload...), nil
}

func hasControlCharacter(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
