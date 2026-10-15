package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNormalizeSeedSetting(t *testing.T) {
	tests := []struct {
		name      string
		seed      SettingSeed
		wantType  string
		wantValue string
		wantError bool
	}{
		{
			name:      "string",
			seed:      SettingSeed{Value: "hello", ValueType: "string"},
			wantType:  "string",
			wantValue: `"hello"`,
		},
		{
			name:      "duration alias becomes string",
			seed:      SettingSeed{Value: "15m", ValueType: "duration"},
			wantType:  "string",
			wantValue: `"15m"`,
		},
		{
			name:      "integer alias",
			seed:      SettingSeed{Value: "42", ValueType: "int"},
			wantType:  "integer",
			wantValue: `42`,
		},
		{
			name:      "boolean alias",
			seed:      SettingSeed{Value: "true", ValueType: "bool"},
			wantType:  "boolean",
			wantValue: `true`,
		},
		{
			name:      "float",
			seed:      SettingSeed{Value: "1.25", ValueType: "float"},
			wantType:  "float",
			wantValue: `1.25`,
		},
		{
			name:      "json",
			seed:      SettingSeed{Value: `{"enabled":true}`, ValueType: "json"},
			wantType:  "json",
			wantValue: `{"enabled":true}`,
		},
		{
			name:      "invalid integer",
			seed:      SettingSeed{Value: "4.2", ValueType: "int"},
			wantError: true,
		},
		{
			name:      "non-finite float",
			seed:      SettingSeed{Value: "NaN", ValueType: "float"},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			valueType, value, err := normalizeSeedSetting(test.seed)
			if (err != nil) != test.wantError {
				t.Fatalf("normalizeSeedSetting() error = %v, wantError %t", err, test.wantError)
			}
			if test.wantError {
				return
			}
			if valueType != test.wantType {
				t.Errorf("value type = %q, want %q", valueType, test.wantType)
			}
			if !json.Valid(value) || !bytes.Equal(value, []byte(test.wantValue)) {
				t.Errorf("value = %s, want valid JSON %s", value, test.wantValue)
			}
		})
	}
}
