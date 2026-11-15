package sale

import (
	"math"
	"testing"
)

func TestCheckedMultiply(t *testing.T) {
	tests := []struct {
		name        string
		left, right int64
		want        int64
		ok          bool
	}{
		{name: "normal", left: 12, right: 3, want: 36, ok: true},
		{name: "overflow", left: math.MaxInt64, right: 2},
		{name: "negative quantity", left: -1, right: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := checkedMultiply(tt.left, tt.right)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("checkedMultiply(%d, %d) = (%d, %t), want (%d, %t)", tt.left, tt.right, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCheckedAdd(t *testing.T) {
	tests := []struct {
		name        string
		left, right int64
		want        int64
		ok          bool
	}{
		{name: "normal", left: 12, right: 3, want: 15, ok: true},
		{name: "positive overflow", left: math.MaxInt64, right: 1},
		{name: "negative overflow", left: math.MinInt64, right: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := checkedAdd(tt.left, tt.right)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("checkedAdd(%d, %d) = (%d, %t), want (%d, %t)", tt.left, tt.right, got, ok, tt.want, tt.ok)
			}
		})
	}
}
