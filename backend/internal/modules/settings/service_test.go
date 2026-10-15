package settings

import (
	"context"
	"encoding/json"
	"testing"

	appctx "backend/internal/common/context"
	"github.com/google/uuid"
)

func TestSystemReadsRequirePlatformAdministrator(t *testing.T) {
	service := NewService(nil, nil)
	ctx := appctx.WithPrincipal(context.Background(), appctx.Principal{
		UserID: uuid.New(),
		OrgID:  uuid.New(),
	})

	if _, _, err := service.List(ctx, &ListSettingsQuery{Scope: SettingReadModeSystem}); err == nil {
		t.Fatal("expected direct system list to be forbidden")
	}
	if _, err := service.Get(ctx, "app.name", GetSettingQuery{Scope: SettingReadModeSystem}); err == nil {
		t.Fatal("expected direct system read to be forbidden")
	}
}

func TestSensitiveReadsRequirePlatformAdministrator(t *testing.T) {
	service := NewService(nil, nil)
	ctx := appctx.WithPrincipal(context.Background(), appctx.Principal{
		UserID: uuid.New(),
		OrgID:  uuid.New(),
	})
	includeSensitive := true

	if _, _, err := service.List(ctx, &ListSettingsQuery{IncludeSensitive: &includeSensitive}); err == nil {
		t.Fatal("expected sensitive list disclosure to be forbidden")
	}
	if _, err := service.Get(ctx, "app.name", GetSettingQuery{IncludeSensitive: &includeSensitive}); err == nil {
		t.Fatal("expected sensitive read disclosure to be forbidden")
	}
}

func TestValidateDefinitionValueEnforcesDeclaredTypeAndOneJSONValue(t *testing.T) {
	tests := []struct {
		name      string
		valueType SettingValueType
		value     json.RawMessage
		wantError bool
	}{
		{name: "string", valueType: SettingValueTypeString, value: json.RawMessage(`"value"`)},
		{name: "integer", valueType: SettingValueTypeInteger, value: json.RawMessage(`42`)},
		{name: "fraction is not integer", valueType: SettingValueTypeInteger, value: json.RawMessage(`4.2`), wantError: true},
		{name: "boolean", valueType: SettingValueTypeBoolean, value: json.RawMessage(`false`)},
		{name: "json null", valueType: SettingValueTypeJSON, value: json.RawMessage(`null`)},
		{name: "trailing json", valueType: SettingValueTypeJSON, value: json.RawMessage(`{} {}`), wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDefinitionValue(SettingDefinition{
				Key:       "test.setting",
				ValueType: test.valueType,
			}, test.value)
			if (err != nil) != test.wantError {
				t.Fatalf("validateDefinitionValue() error = %v, wantError %t", err, test.wantError)
			}
		})
	}
}
