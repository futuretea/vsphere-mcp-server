package vsphere_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/alarm"
	"github.com/vmware/govmomi/event"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/performance"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/govmomi/vim25/xml"

	"github.com/futuretea/vsphere-mcp-server/pkg/core/config"
	"github.com/futuretea/vsphere-mcp-server/pkg/toolset"
	vspheretools "github.com/futuretea/vsphere-mcp-server/pkg/toolset/vsphere"
	"github.com/futuretea/vsphere-mcp-server/pkg/vsphere"
)

type queryFixture struct {
	ctx    context.Context
	model  *simulator.Model
	client *govmomi.Client
	tools  []toolset.ServerTool
	perf   chan []byte
	vms    []*object.VirtualMachine
	dcs    []*object.Datacenter
}

func newQueryFixture(t *testing.T, datacenters int) *queryFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	model := simulator.VPX()
	model.Datacenter, model.Cluster, model.ClusterHost = datacenters, 0, 0
	model.Host, model.Machine = 1, 2
	if err := model.Create(); err != nil {
		t.Fatal(err)
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
	f := &queryFixture{ctx: ctx, model: model, client: client, perf: make(chan []byte, 8)}
	frontend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			http.Error(w, "read local request", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		model.Service.ServeMux.ServeHTTP(recorder, r)
		if bytes.Contains(body, []byte("<QueryPerf ")) || bytes.Contains(body, []byte("<QueryPerf>")) {
			f.perf <- append([]byte(nil), recorder.Body.Bytes()...)
		}
		for key, values := range recorder.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	t.Cleanup(frontend.Close)
	endpoint := *server.URL
	endpoint.Host = strings.TrimPrefix(frontend.URL, "https://")
	service := vsphere.NewService(config.VSphereConfig{Endpoint: endpoint.String(), Insecure: true})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	f.tools = vspheretools.NewToolset(service, true, true).GetTools()
	finder := find.NewFinder(client.Client, true)
	f.dcs, err = finder.DatacenterList(ctx, "*")
	if err != nil {
		t.Fatal(err)
	}
	for _, dc := range f.dcs {
		vms, err := finder.SetDatacenter(dc).VirtualMachineList(ctx, "*")
		if err != nil {
			t.Fatal(err)
		}
		f.vms = append(f.vms, vms...)
	}
	return f
}

func (f *queryFixture) tool(t *testing.T, name string) toolset.ServerTool {
	t.Helper()
	for _, tool := range f.tools {
		if tool.Tool.Name == name {
			return tool
		}
	}
	t.Fatalf("MCP tool %q is missing from discovery", name)
	return toolset.ServerTool{}
}

func (f *queryFixture) call(t *testing.T, name string, args map[string]any, result any) string {
	t.Helper()
	text, err := f.tool(t, name).Handler(f.ctx, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := json.Unmarshal([]byte(text), result); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return text
}

func TestQueryFeaturesMetricSamplesPreserveProviderValues(t *testing.T) {
	f := newQueryFixture(t, 1)
	tool := f.tool(t, "vsphere_query_metric_samples")
	ref := f.vms[0].Reference()
	manager := performance.NewManager(f.client.Client)
	counters, err := manager.CounterInfoByName(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := manager.ProviderSummary(f.ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, instance string
		names          []any
		count          int
		explicit       bool
	}{
		{"aggregate", "", []any{"cpu.usage.average", "mem.usage.average"}, 2, true},
		{"cpu_instance", "0", []any{"cpu.usage.average"}, 2, true},
		{"default_sample_count", "", []any{"cpu.usage.average"}, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			end := start.Add(2 * time.Duration(provider.RefreshRate) * time.Second)
			args := map[string]any{"entity_type": ref.Type, "entity_id": ref.Value, "metrics": test.names,
				"start_time": start.Format(time.RFC3339), "end_time": end.Format(time.RFC3339)}
			if test.explicit {
				args["max_samples"] = float64(test.count)
			}
			if test.instance != "" {
				args["instance"] = test.instance
			}
			var result struct {
				Entity     struct{ Type, ID string }
				SampleInfo []types.PerfSampleInfo `json:"sample_info"`
				Metrics    []struct {
					ID                   int32
					Name, Unit, Instance string
					Values               []int64
					Scale                float64
				}
			}
			f.call(t, tool.Tool.Name, args, &result)
			if result.Entity.Type != ref.Type || result.Entity.ID != ref.Value || len(result.SampleInfo) != test.count || len(result.Metrics) != len(test.names) || (len(result.SampleInfo) > 1 && result.SampleInfo[0].Timestamp.Equal(result.SampleInfo[1].Timestamp)) {
				t.Fatalf("wrong entity, sample count, or metric count: %+v", result)
			}
			entity := f.performanceResponse(t)
			want := make(map[string]bool)
			for _, name := range test.names {
				want[name.(string)] = true
			}
			for _, metric := range result.Metrics {
				counter := counters[metric.Name]
				if !want[metric.Name] || counter == nil || metric.ID != counter.Key || metric.Unit != "percent" || metric.Scale != 0.01 || metric.Instance != test.instance || len(metric.Values) != test.count {
					t.Fatalf("metric metadata or value count differs from request/provider: %+v", metric)
				}
				delete(want, metric.Name)
				for i, sample := range result.SampleInfo {
					if sample.Interval != provider.RefreshRate || sample.Timestamp.Before(start) || sample.Timestamp.After(end) {
						t.Fatalf("invalid sample timestamp/interval: %+v", sample)
					}
					value := rawPerformanceValue(t, entity, metric.ID, metric.Instance, sample.Timestamp)
					if metric.Values[i] != value {
						t.Fatalf("%s value at %s = %d, provider raw value = %d", metric.Name, sample.Timestamp, metric.Values[i], value)
					}
				}
			}
		})
	}
	t.Run("unsupported_counter", func(t *testing.T) {
		_, err := tool.Handler(f.ctx, map[string]any{"entity_type": ref.Type, "entity_id": ref.Value, "metrics": []any{"does.not.exist.average"}})
		if err == nil || !strings.Contains(err.Error(), "does.not.exist.average") {
			t.Fatalf("unsupported counter error = %v, want offending counter name", err)
		}
	})
}

func TestQueryFeaturesResourceIncludesUsefulVMSummary(t *testing.T) {
	f := newQueryFixture(t, 1)
	vm := f.vms[0]
	simctx := f.model.Service.Context
	simctx.Map.AtomicUpdate(simctx, simctx.Map.Get(vm.Reference()), []types.PropertyChange{{Name: "summary.guest.ipAddress", Val: "192.0.2.10"}})
	var expected mo.VirtualMachine
	if err := f.client.RetrieveOne(f.ctx, vm.Reference(), []string{"name", "summary"}, &expected); err != nil {
		t.Fatal(err)
	}
	path, err := find.InventoryPath(f.ctx, f.client.Client, vm.Reference())
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Type, ID, Name, Path string
		Properties           map[string]json.RawMessage
	}
	f.call(t, "vsphere_get_resource", map[string]any{"entity_type": "VirtualMachine", "entity_id": vm.Reference().Value}, &result)
	if result.Type != "VirtualMachine" || result.ID != vm.Reference().Value || result.Name != expected.Name || result.Path != path {
		t.Fatalf("resource identity differs from provider: %+v", result)
	}
	if len(result.Properties) != 1 || result.Properties["summary"] == nil {
		t.Fatalf("properties = %v, want summary without full config", result.Properties)
	}
	var summary types.VirtualMachineSummary
	if err := json.Unmarshal(result.Properties["summary"], &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Config.NumCpu != expected.Summary.Config.NumCpu || summary.Config.MemorySizeMB != expected.Summary.Config.MemorySizeMB || summary.Runtime.PowerState != expected.Summary.Runtime.PowerState || summary.Guest == nil || summary.Guest.IpAddress != "192.0.2.10" {
		t.Fatalf("VM summary lost CPU, memory, power state, or provider guest IP: %+v", summary)
	}
}

func TestQueryFeaturesTriggeredAlarmsFollowState(t *testing.T) {
	f := newQueryFixture(t, 1)
	tool := f.tool(t, "vsphere_list_triggered_alarms")
	vm := f.vms[0]
	manager := alarm.NewManager(f.client.Client)
	spec := types.AlarmSpec{Name: "local feature alarm", Description: "local test", Enabled: true,
		Expression: &types.OrAlarmExpression{Expression: []types.BaseAlarmExpression{
			&types.EventAlarmExpression{EventTypeId: "local.red", ObjectType: "VirtualMachine", Status: types.ManagedEntityStatusRed},
			&types.EventAlarmExpression{EventTypeId: "local.green", ObjectType: "VirtualMachine", Status: types.ManagedEntityStatusGreen},
		}}}
	ref, err := manager.CreateAlarm(f.ctx, object.NewRootFolder(f.client.Client), &spec)
	if err != nil {
		t.Fatal(err)
	}
	post := func(eventType string) {
		t.Helper()
		if err := event.NewManager(f.client.Client).PostEvent(f.ctx, &types.EventEx{EventTypeId: eventType, ObjectType: "VirtualMachine", ObjectId: vm.Reference().Value}); err != nil {
			t.Fatal(err)
		}
	}
	type state struct {
		Name          string
		OverallStatus types.ManagedEntityStatus
		Entity, Alarm types.ManagedObjectReference
		Time          time.Time
		Acknowledged  *bool
	}
	query := func(args map[string]any) []state {
		t.Helper()
		var states []state
		text := f.call(t, tool.Tool.Name, args, &states)
		if !strings.HasPrefix(text, "[") {
			t.Fatalf("alarm response must be an array: %s", text)
		}
		return states
	}
	var definitions any
	before := f.call(t, "vsphere_list_alarms", map[string]any{}, &definitions)
	if states := query(map[string]any{}); len(states) != 0 {
		t.Fatalf("untriggered alarm is active: %+v", states)
	}
	post("local.red")
	args := map[string]any{"entity_type": "VirtualMachine", "entity_id": vm.Reference().Value}
	for _, scope := range []map[string]any{map[string]any{}, args} {
		states := query(scope)
		if len(states) != 1 {
			t.Fatalf("active alarm count = %d, want 1", len(states))
		}
		state := states[0]
		if state.Name != spec.Name || state.OverallStatus != types.ManagedEntityStatusRed || state.Entity != vm.Reference() || state.Alarm != *ref || state.Time.IsZero() || state.Acknowledged == nil || *state.Acknowledged {
			t.Fatalf("active alarm missing current state or identity: %+v", state)
		}
	}
	if states := query(map[string]any{"entity_type": "VirtualMachine", "entity_id": f.vms[1].Reference().Value}); len(states) != 0 {
		t.Fatalf("alarm leaked to another VM: %+v", states)
	}
	if err := manager.AcknowledgeAlarm(f.ctx, *ref, vm); err != nil {
		t.Fatal(err)
	}
	if states := query(args); len(states) != 1 || states[0].Acknowledged == nil || !*states[0].Acknowledged {
		t.Fatalf("acknowledgment was not reflected: %+v", states)
	}
	post("local.green")
	if states := query(map[string]any{}); len(states) != 0 {
		t.Fatalf("cleared alarm remains active: %+v", states)
	}
	if after := f.call(t, "vsphere_list_alarms", map[string]any{}, &definitions); before != after {
		t.Fatal("reading active alarm states changed the configured alarm definitions")
	}
}

func TestQueryFeaturesInventoryPagesAndLocatesDuplicateNames(t *testing.T) {
	f := newQueryFixture(t, 2)
	type row struct{ Type, ID, Name, Path string }
	want := make(map[string]row)
	for i, vm := range f.vms {
		name := "shared-name"
		if i%2 != 0 {
			name += "-extra"
		}
		task, err := vm.Rename(f.ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := task.Wait(f.ctx); err != nil {
			t.Fatal(err)
		}
		path, err := find.InventoryPath(f.ctx, f.client.Client, vm.Reference())
		if err != nil {
			t.Fatal(err)
		}
		want[vm.Reference().Value] = row{"VirtualMachine", vm.Reference().Value, name, path}
	}
	seen := make(map[string]bool)
	for offset := 0; offset <= len(f.vms); offset++ {
		var rows []row
		text := f.call(t, "vsphere_list_inventory", map[string]any{"kind": "vm", "limit": float64(1), "offset": float64(offset)}, &rows)
		if !strings.HasPrefix(text, "[") {
			t.Fatalf("inventory response changed array shape: %s", text)
		}
		if offset == len(f.vms) {
			if len(rows) != 0 {
				t.Fatalf("end page contains %d rows", len(rows))
			}
			break
		}
		if len(rows) != 1 || rows[0] != want[rows[0].ID] || seen[rows[0].ID] {
			t.Fatalf("page %d omitted identity or repeated an object: %+v", offset, rows)
		}
		seen[rows[0].ID] = true
	}
	var matches []row
	f.call(t, "vsphere_list_inventory", map[string]any{"kind": "vm", "name": "shared-name"}, &matches)
	if len(matches) != 2 {
		t.Fatalf("exact name lookup returned %d VMs, want 2", len(matches))
	}
	for _, dc := range f.dcs {
		f.call(t, "vsphere_list_inventory", map[string]any{"kind": "vm", "name": "shared-name", "datacenter_id": dc.Reference().Value}, &matches)
		if len(matches) != 1 || matches[0].Name != "shared-name" || !strings.HasPrefix(matches[0].Path, dc.InventoryPath+"/") {
			t.Fatalf("datacenter and exact-name lookup is ambiguous: %+v", matches)
		}
	}
}

func (f *queryFixture) performanceResponse(t *testing.T) *types.PerfEntityMetric {
	t.Helper()
	var raw methods.QueryPerfBody
	envelope := soap.Envelope{Body: &raw}
	select {
	case data := <-f.perf:
		decoder := xml.NewDecoder(bytes.NewReader(data))
		decoder.TypeFunc = types.TypeFunc()
		if err := decoder.Decode(&envelope); err != nil {
			t.Fatalf("decode actual provider response: %v", err)
		}
	default:
		t.Fatal("tool did not query provider performance values")
	}
	entity, ok := raw.Res.Returnval[0].(*types.PerfEntityMetric)
	if !ok {
		t.Fatalf("unexpected provider series type %T", raw.Res.Returnval[0])
	}
	return entity
}

func rawPerformanceValue(t *testing.T, entity *types.PerfEntityMetric, id int32, instance string, timestamp time.Time) int64 {
	t.Helper()
	for _, sample := range entity.Value {
		series := sample.(*types.PerfMetricIntSeries)
		if series.Id.CounterId != id || series.Id.Instance != instance {
			continue
		}
		for i, info := range entity.SampleInfo {
			if info.Timestamp.Equal(timestamp) {
				return series.Value[i]
			}
		}
	}
	t.Fatalf("no raw provider value for counter %d instance %q at %s", id, instance, timestamp)
	return 0
}
