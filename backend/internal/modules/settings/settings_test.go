package settings

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestUpdateSettingRequestValidateRequiresVersionAndScope(t *testing.T) {
	request := UpdateSettingRequest{Value: json.RawMessage(`false`)}

	errs := request.Validate()
	if !hasFieldError(errs, "scope", "oneof") {
		t.Fatal("expected invalid scope error")
	}
	if !hasFieldError(errs, "expected_version", "required") {
		t.Fatal("expected required expected_version error")
	}

	version := int64(0)
	request.Scope = SettingScopeOrganization
	request.ExpectedVersion = &version
	if errs := request.Validate(); len(errs) != 0 {
		t.Fatalf("version 0 and JSON false should be valid: %v", errs)
	}
}

func TestUpdateSettingRequestValidateJSONMustContainOneBoundedValue(t *testing.T) {
	version := int64(1)
	tests := []struct {
		name     string
		value    json.RawMessage
		wantRule string
	}{
		{name: "multiple values", value: json.RawMessage(`{} {}`), wantRule: "json"},
		{name: "oversized value", value: json.RawMessage(`"` + strings.Repeat("a", maxSettingValueBytes) + `"`), wantRule: "max_size"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := UpdateSettingRequest{
				Value:           test.value,
				ExpectedVersion: &version,
				Scope:           SettingScopeSystem,
			}
			if errs := request.Validate(); !hasFieldError(errs, "value", test.wantRule) {
				t.Fatalf("expected %q error for value, got %v", test.wantRule, errs)
			}
		})
	}
}

func TestBulkUpdateSettingsRequestNormalizesKeysAndReportsFullIndexes(t *testing.T) {
	version := int64(0)
	request := BulkUpdateSettingsRequest{
		Scope: SettingScopeOrganization,
		Items: make([]BulkUpdateEntry, 11),
	}
	for i := range request.Items {
		request.Items[i] = BulkUpdateEntry{
			Key:             "setting." + strings.Repeat("x", i+1),
			Value:           json.RawMessage(`null`),
			ExpectedVersion: &version,
		}
	}
	request.Items[10].Value = json.RawMessage(`{} {}`)
	request.Items[0].Key = " setting.first "

	errs := request.Validate()
	if request.Items[0].Key != "setting.first" {
		t.Fatalf("key was not normalized in the request: %q", request.Items[0].Key)
	}
	if !hasFieldError(errs, "items[10].value", "json") {
		t.Fatalf("expected error path to preserve index 10, got %v", errs)
	}
}

func TestBulkUpdateSettingsRequestRejectsDuplicateNormalizedKeys(t *testing.T) {
	version := int64(0)
	request := BulkUpdateSettingsRequest{
		Scope: SettingScopeSystem,
		Items: []BulkUpdateEntry{
			{Key: " setting.one ", Value: json.RawMessage(`0`), ExpectedVersion: &version},
			{Key: "setting.one", Value: json.RawMessage(`1`), ExpectedVersion: &version},
		},
	}

	errs := request.Validate()
	if !hasFieldError(errs, "items", "unique") {
		t.Fatalf("expected duplicate-key error, got %v", errs)
	}
	if request.Items[0].Key != "setting.one" {
		t.Fatalf("key was not normalized in the request: %q", request.Items[0].Key)
	}
}

func TestBulkUpdateSettingsRequestRejectsInvalidKeyPattern(t *testing.T) {
	version := int64(0)
	request := BulkUpdateSettingsRequest{
		Scope: SettingScopeOrganization,
		Items: []BulkUpdateEntry{
			{Key: "Setting.Invalid", Value: json.RawMessage(`true`), ExpectedVersion: &version},
		},
	}

	if errs := request.Validate(); !hasFieldError(errs, "items[0].key", "pattern") {
		t.Fatalf("expected invalid setting key pattern error, got %v", errs)
	}
}

func TestSensitiveRedactionIsValidJSON(t *testing.T) {
	value := RedactSensitiveValue()
	if !json.Valid(value) || string(value) != `"<redacted>"` {
		t.Fatalf("unexpected redacted value: %s", value)
	}
}

func TestListSettingsQueryRejectsUnsupportedReadControls(t *testing.T) {
	query := ListSettingsQuery{
		Scope: SettingReadMode("branch"),
		Q:     strings.Repeat("x", maxSettingSearchRunes+1),
	}
	errs := query.NormalizeAndValidate()
	if !hasFieldError(errs, "scope", "oneof") {
		t.Fatalf("expected invalid scope error, got %v", errs)
	}
	if !hasFieldError(errs, "q", "max_size") {
		t.Fatalf("expected query length error, got %v", errs)
	}

	query = ListSettingsQuery{}
	query.Sort = "id"
	if errs := query.NormalizeAndValidate(); !hasFieldError(errs, "sort", "oneof") {
		t.Fatalf("expected unsupported sort error, got %v", errs)
	}
}

func hasFieldError(errs []error, field, rule string) bool {
	for _, err := range errs {
		var fieldErr *FieldError
		if errors.As(err, &fieldErr) && fieldErr.Field == field && fieldErr.Rule == rule {
			return true
		}
	}
	return false
}
