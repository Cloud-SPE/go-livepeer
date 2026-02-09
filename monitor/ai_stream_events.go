package monitor

import "time"

// AIStreamEventType identifies the kind of normalized live stream issue event.
type AIStreamEventType string

const (
	AIStreamEventTypeError      AIStreamEventType = "error"
	AIStreamEventTypeSwap       AIStreamEventType = "orchestrator_swap"
	AIStreamEventTypeSwapFailed AIStreamEventType = "orchestrator_swap_failed"
	AIStreamEventTypeSuspend    AIStreamEventType = "orchestrator_suspend"
)

// AIStreamErrorType provides a typed reason classification for issue events.
type AIStreamErrorType string

const (
	AIStreamErrorTypeGatewayError                 AIStreamErrorType = "gateway_error"
	AIStreamErrorTypeOrchestratorInferenceFailure AIStreamErrorType = "orchestrator_inference_failure"
	AIStreamErrorTypeNetworkTimeout               AIStreamErrorType = "network_timeout"
	AIStreamErrorTypeSlowOrchestrator             AIStreamErrorType = "slow_orchestrator"
	AIStreamErrorTypeSubscribeError               AIStreamErrorType = "subscribe_error"
	AIStreamErrorTypeOrchestratorSwap             AIStreamErrorType = "orchestrator_swap"
	AIStreamErrorTypeOrchestratorSwapFailed       AIStreamErrorType = "orchestrator_swap_failed"
)

// AIStreamIssueEvent is a consolidated schema for gateway-side live stream
// error/swap/suspension events.
type AIStreamIssueEvent struct {
	Type                AIStreamEventType `json:"type"`
	ErrorType           AIStreamErrorType `json:"error_type,omitempty"`
	OrchestratorAddress string            `json:"orchestrator_address,omitempty"`
	OrchestratorURL     string            `json:"orchestrator_url,omitempty"`
	StreamID            string            `json:"stream_id,omitempty"`
	Pipeline            string            `json:"pipeline,omitempty"`
	Timestamp           int64             `json:"timestamp"`

	OldOrchestratorAddress string `json:"old_orchestrator_address,omitempty"`
	OldOrchestratorURL     string `json:"old_orchestrator_url,omitempty"`
	NewOrchestratorAddress string `json:"new_orchestrator_address,omitempty"`
	NewOrchestratorURL     string `json:"new_orchestrator_url,omitempty"`
	SwapReason             string `json:"swap_reason,omitempty"`

	GPUID      string      `json:"gpu_id,omitempty"`
	ModelID    string      `json:"model_id,omitempty"`
	RequestID  string      `json:"request_id,omitempty"`
	PipelineID string      `json:"pipeline_id,omitempty"`
	Stage      string      `json:"stage,omitempty"`
	Capability interface{} `json:"capability,omitempty"`

	// Legacy compatibility fields used by existing consumers.
	Message          string                 `json:"message,omitempty"`
	OrchestratorInfo map[string]interface{} `json:"orchestrator_info,omitempty"`
}

// EmitAIStreamIssueEvent normalizes and emits an issue event to the
// `ai_stream_events` Kafka topic.
func EmitAIStreamIssueEvent(ev AIStreamIssueEvent) {
	ev = normalizeAIStreamIssueEvent(ev)
	EmitQueueEvent(KafkaTopicAIStreamEvents, ev)
}

func normalizeAIStreamIssueEvent(ev AIStreamIssueEvent) AIStreamIssueEvent {
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}
	if ev.Type == "" {
		ev.Type = AIStreamEventTypeError
	}
	if ev.OrchestratorInfo == nil && ev.OrchestratorAddress != "" {
		ev.OrchestratorInfo = map[string]interface{}{
			"address": ev.OrchestratorAddress,
			"url":     ev.OrchestratorURL,
		}
	}
	return ev
}
