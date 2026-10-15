package middleware

import (
	"encoding/json"
	"testing"
)

func TestSettingsAuditRedactsValuesRecursively(t *testing.T) {
	raw := []byte(`{"scope":"organization","value":{"nested":"secret"},"items":[{"key":"app.name","value":"Pharmaciano"}]}`)
	body := maskBodyForModule(raw, "settings")

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal masked audit body: %v", err)
	}
	if result["value"] != auditRedaction {
		t.Fatalf("top-level setting value was not redacted: %v", result["value"])
	}
	item := result["items"].([]any)[0].(map[string]any)
	if item["value"] != auditRedaction {
		t.Fatalf("bulk setting value was not redacted: %v", item["value"])
	}
}

func TestOtherModuleAuditPreservesNonSensitiveValueFields(t *testing.T) {
	body := maskBodyForModule([]byte(`{"value":"visible"}`), "products")
	if string(body) != `{"value":"visible"}` {
		t.Fatalf("unexpected audit body: %s", body)
	}
}
