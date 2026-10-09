package http_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/futuretea/vsphere-mcp-server/pkg/core/config"
	internalhttp "github.com/futuretea/vsphere-mcp-server/pkg/server/http"
	mcpserver "github.com/futuretea/vsphere-mcp-server/pkg/server/mcp"
	"github.com/futuretea/vsphere-mcp-server/pkg/toolset"
	"github.com/futuretea/vsphere-mcp-server/pkg/toolset/vsphere"
)

func TestHealthz(t *testing.T) {
	mcpServer, err := mcpserver.NewServer(mcpserver.Configuration{
		StaticConfig: &config.StaticConfig{LogLevel: "info"},
		Toolsets:     []toolset.Toolset{vsphere.NewToolset(nil, false, false)},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer mcpServer.Close()

	httpServer := &http.Server{}
	handler := internalhttp.NewHandler(mcpServer, httpServer, "")

	req := mustNewRequest(t, http.MethodGet, internalhttp.HealthEndpoint)
	rec := &responseRecorder{header: make(http.Header)}
	handler.ServeHTTP(rec, req)
	if rec.status != http.StatusOK || string(rec.body) != "healthy" {
		t.Fatalf("healthy status=%d body=%q", rec.status, rec.body)
	}

	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	client := testServer.Client()
	client.Timeout = 2 * time.Second
	req, err = http.NewRequestWithContext(t.Context(), http.MethodHead, testServer.URL+internalhttp.HealthEndpoint, nil)
	if err != nil {
		t.Fatalf("NewRequest HEAD: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HEAD healthz: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read HEAD healthz body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || len(body) != 0 || resp.ContentLength != int64(len("healthy")) {
		t.Fatalf("HEAD healthy status=%d content_length=%d body=%q", resp.StatusCode, resp.ContentLength, body)
	}

	req = mustNewRequest(t, http.MethodPost, internalhttp.HealthEndpoint)
	rec = &responseRecorder{header: make(http.Header)}
	handler.ServeHTTP(rec, req)
	if rec.status != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.status)
	}
}

func TestServeListenerShutdown(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "0.0.0.0"} {
		t.Run(host, func(t *testing.T) { testServeListenerShutdown(t, host) })
	}
}

func testServeListenerShutdown(t *testing.T, host string) {
	t.Helper()
	mcpServer, err := mcpserver.NewServer(mcpserver.Configuration{
		StaticConfig: &config.StaticConfig{LogLevel: "info"},
		Toolsets:     []toolset.Toolset{vsphere.NewToolset(nil, false, false)},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer mcpServer.Close()

	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	cfg := &config.StaticConfig{Port: listener.Addr().(*net.TCPAddr).Port, Listen: host}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- internalhttp.ServeListener(ctx, mcpServer, cfg, listener)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	clientAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port))
	resp, err := client.Get("http://" + clientAddress + internalhttp.HealthEndpoint)
	if err != nil {
		select {
		case serveErr := <-done:
			t.Fatalf("ServeListener: %v", serveErr)
		default:
		}
		t.Fatalf("GET healthz: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read healthz body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "healthy" {
		t.Fatalf("healthz status=%d body=%q", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeListener: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeListener did not exit after cancel")
	}
}

func TestServeListenerRejectsRemoteAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	mcpServer, err := mcpserver.NewServer(mcpserver.Configuration{StaticConfig: &config.StaticConfig{LogLevel: "info"}, Toolsets: []toolset.Toolset{vsphere.NewToolset(nil, false, false)}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer mcpServer.Close()
	remoteListener := &listenerWithAddress{Listener: listener, address: &net.TCPAddr{IP: net.ParseIP("192.0.2.1")}}
	err = internalhttp.ServeListener(context.Background(), mcpServer, &config.StaticConfig{LogLevel: "info"}, remoteListener)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("ServeListener error = %v", err)
	}
}

type listenerWithAddress struct {
	net.Listener
	address net.Addr
}

func (l *listenerWithAddress) Addr() net.Addr { return l.address }

type responseRecorder struct {
	header http.Header
	status int
	body   []byte
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.body = append(r.body, b...)
	return len(b), nil
}
func (r *responseRecorder) WriteHeader(statusCode int) { r.status = statusCode }

func mustNewRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return req
}
