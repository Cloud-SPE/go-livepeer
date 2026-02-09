package monitor

import "testing"

func TestNormalizeStreamTraceEvent_DefaultTimestamp(t *testing.T) {
	ev := normalizeStreamTraceEvent(StreamTraceEvent{
		Type: StreamTraceGatewayReceiveStreamRequest,
	})

	if ev.Timestamp == 0 {
		t.Fatal("expected timestamp to be set")
	}
}

func TestNormalizeStreamTraceEvent_RespectsTimestamp(t *testing.T) {
	ev := normalizeStreamTraceEvent(StreamTraceEvent{
		Type:      StreamTraceGatewayReceiveStreamRequest,
		Timestamp: 42,
	})

	if ev.Timestamp != 42 {
		t.Fatalf("unexpected timestamp: %d", ev.Timestamp)
	}
}
