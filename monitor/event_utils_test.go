package monitor

import "testing"

func TestFirstNonEmpty(t *testing.T) {
	got := FirstNonEmpty("", "", "x", "y")
	if got != "x" {
		t.Fatalf("unexpected first non-empty: %q", got)
	}
}

func TestMapStringValue(t *testing.T) {
	m := map[string]interface{}{
		"s": "value",
		"n": 10,
		"z": nil,
	}

	if got := MapStringValue(m, "s"); got != "value" {
		t.Fatalf("unexpected string value: %q", got)
	}
	if got := MapStringValue(m, "n"); got != "" {
		t.Fatalf("expected empty for non-string, got: %q", got)
	}
	if got := MapStringValue(m, "z"); got != "" {
		t.Fatalf("expected empty for nil, got: %q", got)
	}
	if got := MapStringValue(m, "missing"); got != "" {
		t.Fatalf("expected empty for missing key, got: %q", got)
	}
}
