package sale

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSensitivePersistenceFieldsAreNotJSON(t *testing.T) {
	const secret = "sale-model-sensitive-sentinel"
	intent := SalePaymentIntent{
		ProviderAccountID:      secret,
		ProviderIdempotencyKey: secret,
		ExternalReference:      stringPointer(secret),
		FailureMessage:         stringPointer(secret),
	}
	event := SalePaymentEvent{
		ProviderAccountID: secret,
		ProviderEventID:   secret,
		EvidenceSnapshot:  map[string]any{"secret": secret},
	}
	prescription := SalePrescriptionEvidence{
		PrescriptionRef: stringPointer(secret),
		PrescriberRef:   stringPointer(secret),
		EvidenceRefs:    []string{secret},
		DecisionReason:  stringPointer(secret),
	}
	operation := SaleOperation{
		ResponseBody: map[string]any{"secret": secret},
	}
	tender := SaleTender{
		ReferenceNo:   stringPointer(secret),
		ProviderRef:   stringPointer(secret),
		SettlementRef: stringPointer(secret),
	}
	refund := SaleRefund{LedgerPostingRef: stringPointer(secret)}
	returnDocument := SalesReturn{EvidenceRefs: []string{secret}}

	encoded, err := json.Marshal([]any{
		intent, event, prescription, operation, tender, refund, returnDocument,
	})
	if err != nil {
		t.Fatalf("marshal persistence models: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("sensitive persistence fields were serialized: %s", encoded)
	}
}

func stringPointer(value string) *string {
	return &value
}
