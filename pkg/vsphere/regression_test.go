package vsphere

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/fault"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/futuretea/vsphere-mcp-server/pkg/core/config"
)

type regressionFixture struct {
	ctx     context.Context
	model   *simulator.Model
	server  *simulator.Server
	client  *govmomi.Client
	service *Service
}

func newRegressionFixture(t *testing.T, datacenters int) *regressionFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	model := simulator.VPX()
	model.Datacenter, model.Cluster, model.ClusterHost = datacenters, 0, 0
	model.Host, model.Machine = 1, 1
	if err := model.Create(); err != nil {
		t.Fatalf("create local simulator: %v", err)
	}
	t.Cleanup(model.Remove)
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	t.Cleanup(server.Close)
	client, err := govmomi.NewClient(ctx, server.URL, true)
	if err != nil {
		t.Fatal("connect local simulator")
	}
	t.Cleanup(func() { _ = client.Logout(context.Background()) })
	service := NewService(config.VSphereConfig{Endpoint: server.URL.String(), Insecure: true})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	return &regressionFixture{ctx, model, server, client, service}
}

func TestRegressionInventoryIncludesBothDatacenters(t *testing.T) {
	f := newRegressionFixture(t, 2)
	for _, test := range []struct{ kind, objectType string }{
		{"vm", "VirtualMachine"}, {"host", "HostSystem"}, {"datastore", "Datastore"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			want := make(map[string]bool)
			for _, object := range f.model.Service.Context.Map.AllReference(test.objectType) {
				want[object.Reference().Value] = true
			}
			if len(want) != 2 {
				t.Fatalf("fixture has %d %s objects, want one per datacenter", len(want), test.kind)
			}
			result, err := f.service.ListInventory(f.ctx, test.kind, 100)
			if err != nil {
				t.Fatalf("list %s across two datacenters: %v", test.kind, err)
			}
			var objects []inventoryObject
			if err := json.Unmarshal([]byte(result), &objects); err != nil {
				t.Fatalf("decode inventory: %v", err)
			}
			for _, object := range objects {
				if object.Type != test.objectType || !want[object.ID] {
					t.Fatalf("unexpected or duplicate inventory object: %+v", object)
				}
				delete(want, object.ID)
			}
			if len(want) != 0 {
				t.Fatalf("missing %d %s objects from the two datacenters", len(want), test.kind)
			}
		})
	}
}

func TestRegressionCapabilitiesReportsTemporaryEventFailure(t *testing.T) {
	f := newRegressionFixture(t, 1)
	var injected atomic.Bool
	frontend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			http.Error(w, "read local request", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte("<QueryEvents")) && injected.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.model.Service.ServeMux.ServeHTTP(w, r)
	}))
	t.Cleanup(frontend.Close)
	endpoint := *f.server.URL
	endpoint.Host = strings.TrimPrefix(frontend.URL, "https://")
	service := NewService(config.VSphereConfig{Endpoint: endpoint.String(), Insecure: true})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	_, probeErr := service.Capabilities(f.ctx)
	if !injected.Load() {
		t.Fatal("fixture did not inject the event-query failure")
	}
	if _, err := service.ListEvents(f.ctx, 1); err != nil {
		t.Fatalf("events should recover after the one injected failure: %v", err)
	}
	if probeErr == nil || !strings.Contains(probeErr.Error(), "503") {
		t.Fatalf("capability discovery error = %v, want the event-query HTTP 503 cause", probeErr)
	}
}

func TestRegressionTasksSelectLatestQueueTime(t *testing.T) {
	f := newRegressionFixture(t, 1)
	vm, err := find.NewFinder(f.client.Client).VirtualMachine(f.ctx, "*")
	if err != nil {
		t.Fatalf("find fixture VM: %v", err)
	}
	simctx := f.model.Service.Context
	for _, task := range simctx.Map.AllReference("Task") {
		simctx.Map.AtomicUpdate(simctx, task, []types.PropertyChange{{Name: "info.queueTime", Val: time.Unix(0, 0).UTC()}})
	}
	base := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	var latest types.ManagedObjectReference
	for i, seconds := range []int{1, 3, 2} {
		task, err := vm.Rename(f.ctx, fmt.Sprintf("task-order-%d", i))
		if err != nil {
			t.Fatalf("rename fixture VM: %v", err)
		}
		if err := task.Wait(f.ctx); err != nil {
			t.Fatalf("wait for rename: %v", err)
		}
		simctx.Map.AtomicUpdate(simctx, simctx.Map.Get(task.Reference()), []types.PropertyChange{{
			Name: "info.queueTime", Val: base.Add(time.Duration(seconds) * time.Second),
		}})
		if seconds == 3 {
			latest = task.Reference()
		}
	}
	result, err := f.service.ListTasks(f.ctx, 1)
	if err != nil {
		t.Fatalf("list latest task: %v", err)
	}
	var tasks []struct {
		Info struct {
			Task      types.ManagedObjectReference `json:"task"`
			QueueTime time.Time                    `json:"queueTime"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(result), &tasks); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
	if tasks[0].Info.Task != latest || !tasks[0].Info.QueueTime.Equal(base.Add(3*time.Second)) {
		t.Fatalf("selected task = %s queued %s, want %s queued %s", tasks[0].Info.Task, tasks[0].Info.QueueTime, latest, base.Add(3*time.Second))
	}
}

func TestRegressionInventoryPreservesExpiredSessionError(t *testing.T) {
	f := newRegressionFixture(t, 1)
	if _, err := f.service.ListInventory(f.ctx, "vm", 1); err != nil {
		t.Fatalf("establish inventory session: %v", err)
	}
	session, err := f.service.client.SessionManager.UserSession(f.ctx)
	if err != nil || session == nil {
		t.Fatal("fixture could not obtain the local service session")
	}
	if err := f.client.SessionManager.TerminateSession(f.ctx, []string{session.Key}); err != nil {
		t.Fatal("fixture could not terminate the local service session")
	}
	_, err = f.service.ListInventory(f.ctx, "vm", 1)
	if !fault.Is(err, &types.NotAuthenticated{}) {
		t.Fatalf("inventory error = %v, want the underlying NotAuthenticated fault", err)
	}
}
