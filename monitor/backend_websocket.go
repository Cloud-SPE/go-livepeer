package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type websocketBackend struct {
	target  string
	origin  string
	headers http.Header
	timeout time.Duration

	mu   sync.Mutex
	conn *websocket.Conn
}

func init() {
	RegisterBackendFactory("ws", newWebSocketBackend)
	RegisterBackendFactory("wss", newWebSocketBackend)
}

func newWebSocketBackend(u *url.URL, opts BackendOptions) (EventBackend, error) {
	query := u.Query()

	origin := query.Get("origin")
	if origin == "" {
		origin = fmt.Sprintf("http://%s", u.Host)
	}

	timeout := 10 * time.Second
	if rawTimeout := query.Get("timeout"); rawTimeout != "" {
		dur, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout %q for websocket sink %s: %w", rawTimeout, u.String(), err)
		}
		timeout = dur
	}

	query.Del("origin")
	query.Del("timeout")
	cleaned := *u
	cleaned.RawQuery = query.Encode()

	header := make(http.Header, len(opts.Headers))
	for k, v := range opts.Headers {
		header.Set(k, v)
	}

	return &websocketBackend{
		target:  cleaned.String(),
		origin:  origin,
		headers: header,
		timeout: timeout,
	}, nil
}

func (b *websocketBackend) Start(ctx context.Context) error {
	cfg, err := websocket.NewConfig(b.target, b.origin)
	if err != nil {
		return err
	}
	cfg.Header = b.headers.Clone()

	dialer := &net.Dialer{Timeout: b.timeout}
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Deadline = deadline
	}
	cfg.Dialer = dialer

	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		return err
	}

	b.mu.Lock()
	b.conn = conn
	b.mu.Unlock()

	return nil
}

func (b *websocketBackend) Publish(_ context.Context, batch []EventEnvelope) error {
	if len(batch) == 0 {
		return nil
	}

	payload, err := json.Marshal(batch)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.conn == nil {
		return fmt.Errorf("websocket connection not initialized")
	}

	return websocket.Message.Send(b.conn, payload)
}

func (b *websocketBackend) Stop(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.conn != nil {
		err := b.conn.Close()
		b.conn = nil
		return err
	}
	return nil
}
