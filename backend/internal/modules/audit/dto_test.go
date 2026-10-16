package audit

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuditSearchFiltersNormalizeAndValidate(t *testing.T) {
	userID := uuid.New()
	entityID := uuid.New()
	filters := AuditSearchFilters{
		UserID:     " " + strings.ToUpper(userID.String()) + " ",
		Module:     " inventory ",
		Action:     " stock.adjust ",
		EntityType: " stock_item ",
		EntityID:   entityID.String(),
		Outcome:    "failure",
		From:       "2026-10-01T00:00:00-04:00",
		To:         "2026-10-02T00:00:00-04:00",
		IP:         "192.0.2.10",
		Q:          "  inventory adjustment ",
	}

	if errs := filters.NormalizeAndValidate(); len(errs) != 0 {
		t.Fatalf("NormalizeAndValidate() errors = %v", errs)
	}
	if filters.UserID != userID.String() || filters.EntityID != entityID.String() {
		t.Fatalf("UUIDs were not normalized: user=%q entity=%q", filters.UserID, filters.EntityID)
	}
	if filters.Module != "inventory" || filters.Action != "stock.adjust" || filters.EntityType != "stock_item" {
		t.Fatalf("text filters were not trimmed: %+v", filters)
	}
	if filters.From != "2026-10-01T04:00:00Z" || filters.To != "2026-10-02T04:00:00Z" {
		t.Fatalf("time filters were not normalized: from=%q to=%q", filters.From, filters.To)
	}
	if got := filters.ParsedUserID(); got == nil || *got != userID {
		t.Fatalf("ParsedUserID() = %v, want %s", got, userID)
	}
	if got := filters.ParsedEntityID(); got == nil || *got != entityID {
		t.Fatalf("ParsedEntityID() = %v, want %s", got, entityID)
	}
	if got := filters.ParsedIP(); !got.Equal(net.ParseIP("192.0.2.10")) {
		t.Fatalf("ParsedIP() = %v", got)
	}
	from, to := filters.TimeRange()
	if from.Location() != time.UTC || to.Location() != time.UTC || !from.Before(to) {
		t.Fatalf("TimeRange() = (%s, %s), want ordered UTC times", from, to)
	}
}

func TestAuditSearchFiltersRejectUnsafeOrUnboundedValues(t *testing.T) {
	tests := []struct {
		name    string
		filters AuditSearchFilters
		field   string
	}{
		{
			name: "missing time range",
			filters: AuditSearchFilters{
				From: "2026-10-01T00:00:00Z",
			},
			field: "to",
		},
		{
			name: "invalid uuid",
			filters: AuditSearchFilters{
				UserID: "not-a-uuid", From: "2026-10-01T00:00:00Z", To: "2026-10-02T00:00:00Z",
			},
			field: "user_id",
		},
		{
			name: "zero uuid",
			filters: AuditSearchFilters{
				EntityID: uuid.Nil.String(), From: "2026-10-01T00:00:00Z", To: "2026-10-02T00:00:00Z",
			},
			field: "entity_id",
		},
		{
			name: "invalid outcome",
			filters: AuditSearchFilters{
				Outcome: "pending", From: "2026-10-01T00:00:00Z", To: "2026-10-02T00:00:00Z",
			},
			field: "outcome",
		},
		{
			name: "invalid ip",
			filters: AuditSearchFilters{
				IP: "192.0.2.1; DROP TABLE audit_logs", From: "2026-10-01T00:00:00Z", To: "2026-10-02T00:00:00Z",
			},
			field: "ip",
		},
		{
			name: "reversed range",
			filters: AuditSearchFilters{
				From: "2026-10-02T00:00:00Z", To: "2026-10-01T00:00:00Z",
			},
			field: "from",
		},
		{
			name: "range exceeds maximum",
			filters: AuditSearchFilters{
				From: "2026-01-01T00:00:00Z", To: "2026-04-02T00:00:00Z",
			},
			field: "from",
		},
		{
			name: "search too short",
			filters: AuditSearchFilters{
				Q: "ab", From: "2026-10-01T00:00:00Z", To: "2026-10-02T00:00:00Z",
			},
			field: "q",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			errs := test.filters.NormalizeAndValidate()
			if len(errs) == 0 {
				t.Fatal("NormalizeAndValidate() returned no errors")
			}
			for _, err := range errs {
				field, _, _, ok := AuditValidationErrorFields(err)
				if ok && field == test.field {
					return
				}
			}
			t.Fatalf("errors %v did not include field %q", errs, test.field)
		})
	}
}

func TestListAuditLogsQueryPagination(t *testing.T) {
	query := ListAuditLogsQuery{
		AuditSearchFilters: validAuditFilters(),
		Cursor:             base64RawURL([]byte("opaque-cursor")) + "." + strings.Repeat("a", auditCursorSignatureHexLength),
	}
	if errs := query.NormalizeAndValidate(); len(errs) != 0 {
		t.Fatalf("NormalizeAndValidate() errors = %v", errs)
	}
	if query.Limit != defaultAuditPageSize {
		t.Fatalf("default limit = %d, want %d", query.Limit, defaultAuditPageSize)
	}

	for _, limit := range []int{-1, maxAuditPageSize + 1} {
		query := ListAuditLogsQuery{AuditSearchFilters: validAuditFilters(), Limit: limit}
		if errs := query.NormalizeAndValidate(); len(errs) == 0 {
			t.Errorf("limit %d accepted", limit)
		}
	}
	for _, cursor := range []string{"not/base64", strings.Repeat("A", maxAuditCursorTextSize+1)} {
		query := ListAuditLogsQuery{AuditSearchFilters: validAuditFilters(), Cursor: cursor}
		if errs := query.NormalizeAndValidate(); len(errs) == 0 {
			t.Errorf("cursor %q accepted", cursor)
		}
	}
}

func TestAuditEventLookupQueryRequiresAndNormalizesPartitionKey(t *testing.T) {
	query := AuditEventLookupQuery{OccurredAt: "2026-10-01T12:30:00+02:00"}
	if errs := query.NormalizeAndValidate(); len(errs) != 0 {
		t.Fatalf("NormalizeAndValidate() errors = %v", errs)
	}
	if query.OccurredAt != "2026-10-01T10:30:00Z" ||
		query.PartitionKey().Location() != time.UTC {
		t.Fatalf("normalized lookup key = %q (%s)", query.OccurredAt, query.PartitionKey().Location())
	}

	for _, occurredAt := range []string{"", "not-a-timestamp", "2026-10-01"} {
		query := AuditEventLookupQuery{OccurredAt: occurredAt}
		if errs := query.NormalizeAndValidate(); len(errs) == 0 {
			t.Errorf("invalid occurrence timestamp %q accepted", occurredAt)
		}
	}
}

func TestCreateAuditExportRequestRequiresCSVAndBoundedTimeWindow(t *testing.T) {
	request := CreateAuditExportRequest{AuditSearchFilters: validAuditFilters(), Format: " CSV "}
	if errs := request.Validate(); len(errs) != 0 {
		t.Fatalf("Validate() errors = %v", errs)
	}
	if request.Format != "csv" {
		t.Fatalf("Format = %q, want csv", request.Format)
	}

	request.Format = "json"
	if errs := request.Validate(); len(errs) == 0 {
		t.Fatal("unsupported export format was accepted")
	}
}

func TestToAuditLogDetailResponseRequiresBoundedObjectPayloads(t *testing.T) {
	log := AuditLog{ID: uuid.New(), OccurredAt: time.Now().UTC(), Module: "users", Action: "update"}
	payload := RedactedAuditPayload{
		Details:    json.RawMessage(`{"method":"PATCH"}`),
		BeforeData: json.RawMessage(`{"status":"inactive"}`),
		AfterData:  json.RawMessage(`{"status":"active"}`),
	}
	response, err := ToAuditLogDetailResponse(log, payload)
	if err != nil {
		t.Fatalf("ToAuditLogDetailResponse() error = %v", err)
	}
	payload.Details[2] = 'x'
	if string(response.Details) != `{"method":"PATCH"}` {
		t.Fatal("response payload aliases caller-owned bytes")
	}

	for _, invalid := range [][]byte{
		[]byte(`[]`),
		[]byte(`{"secret":`),
		[]byte(`{"large":"` + strings.Repeat("x", maxAuditPayloadBytes) + `"}`),
	} {
		if _, err := ToAuditLogDetailResponse(log, RedactedAuditPayload{Details: invalid}); err == nil {
			t.Errorf("invalid payload of %d bytes accepted", len(invalid))
		}
	}
}

func validAuditFilters() AuditSearchFilters {
	return AuditSearchFilters{
		From: "2026-10-01T00:00:00Z",
		To:   "2026-10-02T00:00:00Z",
	}
}

func base64RawURL(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}
