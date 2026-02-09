package monitor

import "testing"

func TestNormalizeAIStreamIssueEvent_Defaults(t *testing.T) {
	ev := normalizeAIStreamIssueEvent(AIStreamIssueEvent{
		Message:             "boom",
		OrchestratorAddress: "0xabc",
		OrchestratorURL:     "https://orch",
	})

	if ev.Type != AIStreamEventTypeError {
		t.Fatalf("unexpected default type: %q", ev.Type)
	}
	if ev.Timestamp == 0 {
		t.Fatal("expected timestamp to be set")
	}
	if ev.Message != "boom" {
		t.Fatalf("unexpected message: %q", ev.Message)
	}
	if ev.OrchestratorInfo == nil {
		t.Fatal("expected orchestrator_info to be set")
	}
	if got := ev.OrchestratorInfo["address"]; got != "0xabc" {
		t.Fatalf("unexpected orchestrator address: %v", got)
	}
	if got := ev.OrchestratorInfo["url"]; got != "https://orch" {
		t.Fatalf("unexpected orchestrator url: %v", got)
	}
}

func TestNormalizeAIStreamIssueEvent_RespectsExistingFields(t *testing.T) {
	ev := normalizeAIStreamIssueEvent(AIStreamIssueEvent{
		Type:      AIStreamEventTypeSwap,
		Timestamp: 123,
		Message:   "keep",
		OrchestratorInfo: map[string]interface{}{
			"address": "preset",
			"url":     "preset-url",
		},
	})

	if ev.Type != AIStreamEventTypeSwap {
		t.Fatalf("unexpected type: %q", ev.Type)
	}
	if ev.Timestamp != 123 {
		t.Fatalf("unexpected timestamp: %d", ev.Timestamp)
	}
	if ev.Message != "keep" {
		t.Fatalf("unexpected message: %q", ev.Message)
	}
	if got := ev.OrchestratorInfo["address"]; got != "preset" {
		t.Fatalf("unexpected orchestrator address: %v", got)
	}
}
