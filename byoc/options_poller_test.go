package byoc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/livepeer/go-livepeer/core"
	"github.com/stretchr/testify/assert"
)

func TestRefreshCapabilityOptions_SetsWorkerOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"model":"llama-3","vram_gb":24},{"model":"mistral-7b","vram_gb":16}]`))
	}))
	defer srv.Close()

	node := mockJobLivepeerNode()
	if node.ExternalCapabilities == nil {
		node.ExternalCapabilities = core.NewExternalCapabilities()
	}
	cap := &core.ExternalCapability{Name: "test-cap", Url: srv.URL}
	node.ExternalCapabilities.Capabilities["test-cap"] = map[string]*core.ExternalCapability{srv.URL: cap}

	bso := &BYOCOrchestratorServer{
		node:              node,
		sharedBalMtx:      &sync.Mutex{},
		optionsPollMu:     &sync.Mutex{},
		optionsPollCancel: make(map[string]context.CancelFunc),
		optionsHTTPClient: &http.Client{},
	}

	bso.refreshCapabilityOptions(context.Background(), "test-cap", srv.URL, srv.URL)

	options := node.ExternalCapabilities.GetCapabilityWorkerOptions("test-cap")
	assert.Len(t, options, 2)
	assert.Equal(t, "llama-3", options[0]["model"])
	assert.Equal(t, float64(24), options[0]["vram_gb"])
	assert.Equal(t, "mistral-7b", options[1]["model"])
	assert.Equal(t, float64(16), options[1]["vram_gb"])
}

func TestRestartOptionsPolling_StopOnUnregister(t *testing.T) {
	reqCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"model":"llama-3"}]`))
	}))
	defer srv.Close()

	node := mockJobLivepeerNode()
	if node.ExternalCapabilities == nil {
		node.ExternalCapabilities = core.NewExternalCapabilities()
	}
	capability := &core.ExternalCapability{
		Name:                       "test-cap",
		OptionsEndpoint:            srv.URL,
		OptionsPollIntervalSeconds: 1,
	}
	node.ExternalCapabilities.Capabilities["test-cap"] = map[string]*core.ExternalCapability{"": capability}

	bso := &BYOCOrchestratorServer{
		node:              node,
		sharedBalMtx:      &sync.Mutex{},
		optionsPollMu:     &sync.Mutex{},
		optionsPollCancel: make(map[string]context.CancelFunc),
		optionsHTTPClient: &http.Client{},
	}

	bso.restartOptionsPolling(capability)
	time.Sleep(1200 * time.Millisecond)
	bso.stopAllOptionsPollingForCapability("test-cap")
	prevCount := reqCount
	time.Sleep(1200 * time.Millisecond)
	assert.GreaterOrEqual(t, prevCount, 1)
	assert.Equal(t, prevCount, reqCount)
}
