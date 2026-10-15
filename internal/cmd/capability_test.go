package cmd_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"

	"github.com/futuretea/vsphere-mcp-server/internal/cmd"
)

func TestToolCatalogFailureClosesVSphereSession(t *testing.T) {
	for _, command := range []string{"list", "describe"} {
		t.Run(command, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			model := simulator.VPX()
			if err := model.Create(); err != nil {
				t.Fatal(err)
			}
			defer model.Remove()
			model.Service.TLS = &tls.Config{}
			target := model.Service.NewServer()
			defer target.Close()
			admin, err := govmomi.NewClient(ctx, target.URL, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = admin.Logout(ctx) }()
			adminSession, err := admin.SessionManager.UserSession(ctx)
			if err != nil || adminSession == nil {
				t.Fatalf("administrator session available=%t error=%v", adminSession != nil, err)
			}
			upstream := *target.URL
			upstream.User = nil
			upstream.Path = ""
			proxy := httputil.NewSingleHostReverseProxy(&upstream)
			proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
			var injected atomic.Bool
			endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, readErr := io.ReadAll(r.Body)
				_ = r.Body.Close()
				if readErr != nil {
					http.Error(w, "read synthetic SOAP request", http.StatusBadRequest)
					return
				}
				if bytes.Contains(body, []byte("<QueryEvents")) && injected.CompareAndSwap(false, true) {
					http.Error(w, "synthetic event outage", http.StatusServiceUnavailable)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				proxy.ServeHTTP(w, r)
			}))
			defer endpoint.Close()
			password, _ := target.URL.User.Password()
			for key, value := range map[string]string{
				"MCP_VSPHERE_ENDPOINT": endpoint.URL + "/sdk", "MCP_VSPHERE_USERNAME": target.URL.User.Username(),
				"MCP_VSPHERE_PASSWORD": password, "MCP_VSPHERE_INSECURE": "true",
				"MCP_PORT": "0", "MCP_LISTEN": "127.0.0.1", "MCP_LOG_LEVEL": "info",
			} {
				t.Setenv(key, value)
			}
			root := cmd.NewRootCommand(cmd.IOStreams{In: strings.NewReader(""), Out: io.Discard, ErrOut: io.Discard})
			args := []string{"tools", command}
			if command == "describe" {
				args = append(args, "vsphere_list_inventory")
			}
			root.SetArgs(args)
			err = root.ExecuteContext(ctx)
			if !injected.Load() || err == nil || !strings.Contains(err.Error(), "discover event capability") {
				t.Fatalf("event outage injected=%t, command error=%v", injected.Load(), err)
			}
			var sessions mo.SessionManager
			err = property.DefaultCollector(admin.Client).RetrieveOne(ctx, admin.SessionManager.Reference(), []string{"sessionList"}, &sessions)
			if err != nil {
				t.Fatal(err)
			}
			onlyAdmin := len(sessions.SessionList) == 1 && sessions.SessionList[0].Key == adminSession.Key
			if !onlyAdmin {
				t.Fatalf("sessions after failed tools %s = %d, want only the administrator session", command, len(sessions.SessionList))
			}
		})
	}
}
