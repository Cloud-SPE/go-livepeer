package byoc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/livepeer/go-livepeer/clog"
	"github.com/livepeer/go-livepeer/common"
	"github.com/livepeer/go-livepeer/core"
)

const (
	workerOptionsPollDefaultInterval = 30 * time.Second
	workerOptionsPollRequestTimeout  = 5 * time.Second
	workerOptionsMaxBodyBytes        = 1 << 20 // 1MiB
)

func (bso *BYOCOrchestratorServer) restartOptionsPolling(cap *core.ExternalCapability) {
	if cap == nil {
		return
	}
	bso.ensureOptionsPollerState()

	endpoint := resolveOptionsEndpoint(cap)
	pollerKey := cap.Name + ":" + cap.Url
	if endpoint == "" {
		bso.stopOptionsPolling(pollerKey)
		return
	}

	_, configuredInterval := cap.GetOptionsPollConfig()
	interval := configuredInterval
	if interval <= 0 {
		interval = workerOptionsPollDefaultInterval
	}

	bso.stopOptionsPolling(pollerKey)
	ctx, cancel := context.WithCancel(context.Background())

	bso.optionsPollMu.Lock()
	bso.optionsPollCancel[pollerKey] = cancel
	bso.optionsPollMu.Unlock()

	go bso.pollCapabilityOptions(ctx, cap.Name, cap.Url, endpoint, interval)
}

// stopOptionsPolling stops the poller for a specific name:url key.
func (bso *BYOCOrchestratorServer) stopOptionsPolling(key string) {
	bso.ensureOptionsPollerState()
	bso.optionsPollMu.Lock()
	defer bso.optionsPollMu.Unlock()

	cancel, ok := bso.optionsPollCancel[key]
	if ok {
		cancel()
		delete(bso.optionsPollCancel, key)
	}
}

// stopAllOptionsPollingForCapability stops all pollers whose key starts with name+":".
func (bso *BYOCOrchestratorServer) stopAllOptionsPollingForCapability(name string) {
	bso.ensureOptionsPollerState()
	bso.optionsPollMu.Lock()
	defer bso.optionsPollMu.Unlock()

	prefix := name + ":"
	for key, cancel := range bso.optionsPollCancel {
		if strings.HasPrefix(key, prefix) {
			cancel()
			delete(bso.optionsPollCancel, key)
		}
	}
}

func resolveOptionsEndpoint(cap *core.ExternalCapability) string {
	endpoint, _ := cap.GetOptionsPollConfig()
	if endpoint != "" {
		return endpoint
	}

	if cap.Url == "" {
		return ""
	}

	return strings.TrimRight(cap.Url, "/") + "/" + cap.Name + "/options"
}

func (bso *BYOCOrchestratorServer) pollCapabilityOptions(ctx context.Context, name, url, endpoint string, interval time.Duration) {
	bso.refreshCapabilityOptions(ctx, name, url, endpoint)

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			bso.refreshCapabilityOptions(ctx, name, url, endpoint)
		}
	}
}

func (bso *BYOCOrchestratorServer) refreshCapabilityOptions(ctx context.Context, name, url, endpoint string) {
	bso.ensureOptionsPollerState()
	if bso == nil || bso.node == nil || bso.node.ExternalCapabilities == nil {
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, workerOptionsPollRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		clog.Errorf(ctx, "Unable to build BYOC worker options request capability=%v endpoint=%v err=%v", name, endpoint, err)
		return
	}

	resp, err := bso.optionsHTTPClient.Do(req)
	if err != nil {
		clog.V(common.DEBUG).Infof(ctx, "BYOC worker options poll failed capability=%v endpoint=%v err=%v", name, endpoint, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		clog.V(common.DEBUG).Infof(ctx, "BYOC worker options poll non-200 capability=%v endpoint=%v status=%v", name, endpoint, resp.StatusCode)
		return
	}

	limited := io.LimitReader(resp.Body, workerOptionsMaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		clog.Errorf(ctx, "Unable to read BYOC worker options capability=%v endpoint=%v err=%v", name, endpoint, err)
		return
	}
	if len(body) > workerOptionsMaxBodyBytes {
		clog.Errorf(ctx, "BYOC worker options response too large capability=%v endpoint=%v max=%d", name, endpoint, workerOptionsMaxBodyBytes)
		return
	}

	options, err := parseWorkerOptions(body)
	if err != nil {
		clog.Errorf(ctx, "Unable to decode BYOC worker options capability=%v endpoint=%v err=%v", name, endpoint, err)
		return
	}

	cap, ok := bso.node.ExternalCapabilities.GetCapabilityRunner(name, url)
	if !ok || cap == nil {
		return
	}
	cap.SetWorkerOptions(options)
	clog.V(common.DEBUG).Infof(ctx, "Refreshed BYOC worker options capability=%v url=%v endpoint=%v", name, url, endpoint)
}

// parseWorkerOptions handles three response shapes from runner /options endpoints:
//  1. Array of objects:        [{"model":"llama-3",...}, ...]
//  2. Single object:           {"model":"llama-3",...}
//  3. Models-list object:      {"models":["llama-3","mistral",...]}  → each string becomes {"model":"<name>"}
func parseWorkerOptions(body []byte) ([]map[string]interface{}, error) {
	// Try array of objects first (preferred format)
	var list []map[string]interface{}
	if err := json.Unmarshal(body, &list); err == nil {
		return list, nil
	}

	// Try single object
	var single map[string]interface{}
	if err := json.Unmarshal(body, &single); err != nil {
		return nil, err
	}

	// Check for {"models": [...string...]} shape
	if raw, ok := single["models"]; ok {
		if models, ok := raw.([]interface{}); ok {
			result := make([]map[string]interface{}, 0, len(models))
			for _, m := range models {
				if name, ok := m.(string); ok {
					result = append(result, map[string]interface{}{"model": name})
				}
			}
			return result, nil
		}
	}

	// Plain single object
	return []map[string]interface{}{single}, nil
}

func (bso *BYOCOrchestratorServer) ensureOptionsPollerState() {
	if bso == nil {
		return
	}
	if bso.optionsPollMu == nil {
		bso.optionsPollMu = &sync.Mutex{}
	}
	if bso.optionsPollCancel == nil {
		bso.optionsPollCancel = make(map[string]context.CancelFunc)
	}
	if bso.optionsHTTPClient == nil {
		bso.optionsHTTPClient = &http.Client{}
	}
}
