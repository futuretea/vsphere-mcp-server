package vsphere_test

import (
	"context"
	"strings"
	"testing"

	"github.com/futuretea/vsphere-mcp-server/pkg/toolset"
	"github.com/futuretea/vsphere-mcp-server/pkg/toolset/vsphere"
)

func TestToolsetExposesOnlyReadOnlyQueries(t *testing.T) {
	tools := vsphere.NewToolset(nil, false, false).GetTools()
	if len(tools) != 3 {
		t.Fatalf("tool count = %d, want 3", len(tools))
	}

	handlers := make(map[string]toolset.ToolHandler, len(tools))
	for _, tool := range tools {
		handlers[tool.Tool.Name] = tool.Handler
		if tool.Domain != vsphere.Domain {
			t.Fatalf("tool %q domain = %q", tool.Tool.Name, tool.Domain)
		}
		if strings.Contains(tool.Tool.Name, "create") || strings.Contains(tool.Tool.Name, "delete") {
			t.Fatalf("unexpected write tool %q", tool.Tool.Name)
		}
	}

	_, err := handlers["vsphere_list_inventory"](context.Background(), map[string]any{"kind": "vm"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unconfigured inventory call error = %v", err)
	}
}

func TestToolsetValidatesRequiredFields(t *testing.T) {
	tools := vsphere.NewToolset(nil, false, false).GetTools()
	for _, tool := range tools {
		if tool.Tool.Name != "vsphere_query_metrics" {
			continue
		}
		_, err := tool.Handler(context.Background(), map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "entity_type is required") {
			t.Fatalf("metric validation error = %v", err)
		}
		return
	}
	t.Fatal("metrics tool not found")
}

func TestToolsetRegistersAlarmsOnlyWhenAvailable(t *testing.T) {
	withoutAlarms := vsphere.NewToolset(nil, false, false).GetTools()
	withAlarms := vsphere.NewToolset(nil, false, true).GetTools()
	if len(withAlarms) != len(withoutAlarms)+1 {
		t.Fatalf("tool count with alarms = %d, without = %d", len(withAlarms), len(withoutAlarms))
	}
	if withAlarms[len(withAlarms)-1].Tool.Name != "vsphere_list_alarms" {
		t.Fatalf("alarm tool = %q", withAlarms[len(withAlarms)-1].Tool.Name)
	}
}
