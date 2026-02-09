package monitor

import "time"

// StreamTraceEventType identifies gateway stream trace event kinds.
type StreamTraceEventType string

const (
	StreamTraceGatewayReceiveStreamRequest     StreamTraceEventType = "gateway_receive_stream_request"
	StreamTraceGatewayIngestStreamClosed       StreamTraceEventType = "gateway_ingest_stream_closed"
	StreamTraceGatewaySendFirstIngestSegment   StreamTraceEventType = "gateway_send_first_ingest_segment"
	StreamTraceGatewayRecvFirstProcSegment     StreamTraceEventType = "gateway_receive_first_processed_segment"
	StreamTraceGatewayRecvFewProcSegments      StreamTraceEventType = "gateway_receive_few_processed_segments"
	StreamTraceGatewayRecvFirstDataSegment     StreamTraceEventType = "gateway_receive_first_data_segment"
	StreamTraceGatewayNoOrchestratorsAvailable StreamTraceEventType = "gateway_no_orchestrators_available"
)

type OrchestratorInfo struct {
	Address string `json:"address"`
	URL     string `json:"url"`
}

// StreamTraceEvent is the typed schema for `stream_trace` events.
type StreamTraceEvent struct {
	Type             StreamTraceEventType `json:"type"`
	Timestamp        int64                `json:"timestamp"`
	StreamID         string               `json:"stream_id,omitempty"`
	PipelineID       string               `json:"pipeline_id,omitempty"`
	RequestID        string               `json:"request_id,omitempty"`
	Pipeline         string               `json:"pipeline,omitempty"`
	Message          string               `json:"message,omitempty"`
	OrchestratorInfo OrchestratorInfo     `json:"orchestrator_info"`
}

// EmitStreamTraceEvent normalizes and emits a stream trace event.
func EmitStreamTraceEvent(ev StreamTraceEvent) {
	ev = normalizeStreamTraceEvent(ev)
	EmitQueueEvent(KafkaTopicStreamTrace, ev)
}

func normalizeStreamTraceEvent(ev StreamTraceEvent) StreamTraceEvent {
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}
	return ev
}
