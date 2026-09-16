package vsphere

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"

	"github.com/futuretea/vsphere-mcp-server/pkg/core/config"
	vspheretoolset "github.com/futuretea/vsphere-mcp-server/pkg/toolset/vsphere"
)

func TestTargetURLDefaultsToHTTPSAndSDK(t *testing.T) {
	target, err := targetURL(config.VSphereConfig{Endpoint: "vcenter.example.invalid", Username: "readonly-user"})
	if err != nil {
		t.Fatalf("targetURL: %v", err)
	}
	if target.Scheme != "https" || target.Path != "/sdk" {
		t.Fatalf("target URL = %s, want https endpoint with /sdk", target.Redacted())
	}
	if target.User.Username() != "readonly-user" {
		t.Fatalf("username = %q", target.User.Username())
	}
}

func TestTargetURLRejectsNonHTTPS(t *testing.T) {
	_, err := targetURL(config.VSphereConfig{Endpoint: "http://vcenter.example.invalid"})
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("targetURL error = %v", err)
	}
}

func TestRetrieveTasksReturnsEmptyArrayWithoutReferences(t *testing.T) {
	result, err := retrieveTasks(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("retrieveTasks: %v", err)
	}
	if result != "[]" {
		t.Fatalf("retrieveTasks result = %q, want []", result)
	}
}

func TestCapabilitiesAndCommonInventoryAcrossSimulators(t *testing.T) {
	tests := []struct {
		name       string
		model      func() *simulator.Model
		wantAlarms bool
	}{
		{name: "ESXi", model: simulator.ESX, wantAlarms: false},
		{name: "vCenter", model: simulator.VPX, wantAlarms: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := tt.model()
			if tt.name == "vCenter" {
				model.Portgroup = 1
			}
			defer model.Remove()
			if err := model.Create(); err != nil {
				t.Fatalf("create simulator: %v", err)
			}
			model.Service.TLS = new(tls.Config)
			server := model.Service.NewServer()
			defer server.Close()

			service := NewService(config.VSphereConfig{Endpoint: server.URL.String(), Insecure: true})
			capabilities, err := service.Capabilities(context.Background())
			if err != nil {
				t.Fatalf("Capabilities: %v", err)
			}
			if capabilities.Alarms != tt.wantAlarms {
				t.Fatalf("Alarms = %t, want %t", capabilities.Alarms, tt.wantAlarms)
			}
			wantEvents := tt.name == "vCenter"
			if capabilities.Events != wantEvents {
				t.Fatalf("Events = %t, want %t", capabilities.Events, wantEvents)
			}
			registered := make(map[string]bool)
			for _, tool := range vspheretoolset.NewToolset(service, capabilities.Events, capabilities.Alarms).GetTools() {
				registered[tool.Tool.Name] = true
			}
			if registered["vsphere_list_events"] != wantEvents {
				t.Fatalf("event tool registered = %t, want %t", registered["vsphere_list_events"], wantEvents)
			}

			result, err := service.ListInventory(context.Background(), "vm", 1)
			if err != nil {
				t.Fatalf("ListInventory: %v", err)
			}
			var inventory []inventoryObject
			if err := json.Unmarshal([]byte(result), &inventory); err != nil {
				t.Fatalf("decode inventory: %v", err)
			}
			if len(inventory) == 0 || inventory[0].Type != "VirtualMachine" {
				t.Fatalf("inventory = %#v", inventory)
			}
			if tt.name == "vCenter" {
				result, err := service.ListInventory(context.Background(), "distributed_portgroup", 1)
				if err != nil {
					t.Fatalf("ListInventory distributed_portgroup: %v", err)
				}
				var portgroups []inventoryObject
				if err := json.Unmarshal([]byte(result), &portgroups); err != nil {
					t.Fatalf("decode distributed_portgroup inventory: %v", err)
				}
				if len(portgroups) == 0 || portgroups[0].Type != "DistributedVirtualPortgroup" {
					t.Fatalf("distributed_portgroup inventory = %#v", portgroups)
				}
			}

			for _, query := range []struct {
				name string
				run  func() (string, error)
			}{
				{name: "metrics", run: func() (string, error) {
					return service.QueryMetrics(context.Background(), inventory[0].Type, inventory[0].ID, 1)
				}},
				{name: "tasks", run: func() (string, error) { return service.ListTasks(context.Background(), 1) }},
			} {
				t.Run(query.name, func(t *testing.T) {
					result, err := query.run()
					if err != nil {
						t.Fatalf("query: %v", err)
					}
					if !json.Valid([]byte(result)) {
						t.Fatalf("invalid JSON result: %q", result)
					}
				})
			}
			result, eventErr := service.ListEvents(context.Background(), 1)
			if tt.name == "ESXi" {
				if eventErr == nil || !strings.Contains(eventErr.Error(), "NotImplemented") {
					t.Fatalf("ESXi simulator event error = %v", eventErr)
				}
			} else if eventErr != nil || !json.Valid([]byte(result)) {
				t.Fatalf("vCenter events result=%q error=%v", result, eventErr)
			}

			if tt.wantAlarms {
				result, err := service.ListAlarms(context.Background(), 1)
				if err != nil {
					t.Fatalf("ListAlarms: %v", err)
				}
				if !json.Valid([]byte(result)) {
					t.Fatalf("invalid alarms JSON: %q", result)
				}
			}
		})
	}
}
