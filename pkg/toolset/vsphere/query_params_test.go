package vsphere_test

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/futuretea/vsphere-mcp-server/pkg/toolset"
	"github.com/futuretea/vsphere-mcp-server/pkg/toolset/vsphere"
)

func publicHandlers(service vsphere.QueryService) map[string]toolset.ToolHandler {
	handlers := map[string]toolset.ToolHandler{}
	for _, tool := range vsphere.NewToolset(service, true, true).GetTools() {
		handlers[tool.Tool.Name] = tool.Handler
	}
	return handlers
}

func TestMetricSampleSchemaMatchesPublicParameters(t *testing.T) {
	found := false
	for _, tool := range vsphere.NewToolset(nil, true, true).GetTools() {
		if tool.Domain != vsphere.Domain {
			t.Fatalf("tool %q domain = %q", tool.Tool.Name, tool.Domain)
		}
		if tool.Tool.Name != "vsphere_query_metric_samples" {
			continue
		}
		found = true
		data, err := json.Marshal(tool.Tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Type       string                    `json:"type"`
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		slices.Sort(schema.Required)
		if schema.Type != "object" || len(schema.Properties) != 8 || !reflect.DeepEqual(schema.Required, []string{"entity_id", "entity_type", "metrics"}) {
			t.Fatalf("sample schema type=%q properties=%d required=%v", schema.Type, len(schema.Properties), schema.Required)
		}
		for name, want := range map[string]map[string]any{
			"metrics":          {"type": "array", "minItems": float64(1), "maxItems": float64(1000), "items": map[string]any{"type": "string"}},
			"max_samples":      {"type": "integer", "minimum": float64(1), "maximum": float64(1000)},
			"interval_seconds": {"type": "integer", "minimum": float64(1)},
		} {
			property, ok := schema.Properties[name]
			if !ok {
				t.Fatalf("property %q = %T, want object", name, schema.Properties[name])
			}
			for key, value := range want {
				if !reflect.DeepEqual(property[key], value) {
					t.Errorf("property %s.%s=%v, want %v", name, key, property[key], value)
				}
			}
		}
	}
	if !found {
		t.Fatal("metric sample tool not found")
	}
}

func TestPublicQueriesRejectMalformedParameters(t *testing.T) {
	handlers := publicHandlers(nil)
	entity := `"entity_type":"VirtualMachine","entity_id":"vm-42"`
	sample := entity + `,"metrics":["cpu.usage.average"]`
	cases := []struct{ name, tool, payload, want string }{
		{"missing counters", "vsphere_query_metric_samples", `{` + entity + `}`, "metrics must contain"},
		{"counter is not an array", "vsphere_query_metric_samples", `{` + entity + `,"metrics":"cpu.usage.average"}`, "metrics must contain"},
		{"mixed counter types", "vsphere_query_metric_samples", `{` + entity + `,"metrics":["cpu.usage.average",7]}`, "metrics must be an array"},
		{"blank counter", "vsphere_query_metric_samples", `{` + entity + `,"metrics":[" "]}`, "empty counter names"},
		{"sample below minimum", "vsphere_query_metric_samples", `{` + sample + `,"max_samples":0}`, "max_samples must be an integer between 1 and 1000"},
		{"sample above maximum", "vsphere_query_metric_samples", `{` + sample + `,"max_samples":1001}`, "max_samples must be an integer between 1 and 1000"},
		{"fractional sample", "vsphere_query_metric_samples", `{` + sample + `,"max_samples":1.5}`, "max_samples must be an integer"},
		{"zero interval", "vsphere_query_metric_samples", `{` + sample + `,"interval_seconds":0}`, "interval_seconds must be an integer"},
		{"fractional interval", "vsphere_query_metric_samples", `{` + sample + `,"interval_seconds":1.5}`, "interval_seconds must be an integer"},
		{"string interval", "vsphere_query_metric_samples", `{` + sample + `,"interval_seconds":"20"}`, "interval_seconds must be an integer"},
		{"invalid sample time", "vsphere_query_metric_samples", `{` + sample + `,"start_time":"2026-10-14"}`, "start_time must be an RFC3339 timestamp"},
		{"reversed sample window", "vsphere_query_metric_samples", `{` + sample + `,"start_time":"2026-10-14T12:00:00Z","end_time":"2026-10-14T11:00:00Z"}`, "start_time must not be after end_time"},
		{"missing resource id", "vsphere_get_resource", `{"entity_type":"VirtualMachine"}`, "must be provided together"},
		{"event id without type", "vsphere_list_events", `{"entity_id":"vm-42"}`, "must be provided together"},
		{"task invalid end time", "vsphere_list_tasks", `{"end_time":"yesterday"}`, "end_time must be an RFC3339 timestamp"},
		{"alarm type without id", "vsphere_list_triggered_alarms", `{"entity_type":"VirtualMachine"}`, "must be provided together"},
		{"negative offset", "vsphere_list_inventory", `{"kind":"vm","offset":-1}`, "offset must be an integer"},
		{"fractional offset", "vsphere_list_inventory", `{"kind":"vm","offset":0.5}`, "offset must be an integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var params map[string]any
			if err := json.Unmarshal([]byte(tc.payload), &params); err != nil {
				t.Fatal(err)
			}
			_, err := handlers[tc.tool](context.Background(), params)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s error = %v, want %q", tc.tool, err, tc.want)
			}
		})
	}
}

func TestPublicQueriesAcceptValidBoundariesBeforeCheckingTarget(t *testing.T) {
	handlers := publicHandlers(nil)
	cases := []struct{ name, tool, payload string }{
		{"sample defaults", "vsphere_query_metric_samples", `{"entity_type":"VirtualMachine","entity_id":"vm-42","metrics":["cpu.usage.average"]}`},
		{"sample lower limits", "vsphere_query_metric_samples", `{"entity_type":"VirtualMachine","entity_id":"vm-42","metrics":["cpu.usage.average"],"max_samples":1,"interval_seconds":1}`},
		{"sample upper limit and equal window", "vsphere_query_metric_samples", `{"entity_type":"VirtualMachine","entity_id":"vm-42","metrics":["cpu.usage.average"],"max_samples":1000,"start_time":"2026-10-14T12:00:00Z","end_time":"2026-10-14T12:00:00Z"}`},
		{"resource pair", "vsphere_get_resource", `{"entity_type":"VirtualMachine","entity_id":"vm-42"}`},
		{"alarm root", "vsphere_list_triggered_alarms", `{}`},
		{"alarm entity", "vsphere_list_triggered_alarms", `{"entity_type":"VirtualMachine","entity_id":"vm-42"}`},
		{"first inventory page", "vsphere_list_inventory", `{"kind":"vm","offset":0}`},
		{"filtered events", "vsphere_list_events", `{"entity_type":"VirtualMachine","entity_id":"vm-42","start_time":"2026-10-14T12:00:00-07:00"}`},
		{"filtered tasks", "vsphere_list_tasks", `{"end_time":"2026-10-14T12:00:00Z"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var params map[string]any
			if err := json.Unmarshal([]byte(tc.payload), &params); err != nil {
				t.Fatal(err)
			}
			_, err := handlers[tc.tool](context.Background(), params)
			if err == nil || err.Error() != "vSphere target is not configured" {
				t.Fatalf("%s error = %v, want unconfigured target", tc.tool, err)
			}
		})
	}
}

func TestOriginalQueryServiceStillServesExistingCalls(t *testing.T) {
	cases := []struct {
		tool   string
		params map[string]any
		want   []any
	}{
		{"vsphere_list_inventory", map[string]any{"kind": "vm"}, []any{"inventory", "vm", 100}},
		{"vsphere_query_metrics", map[string]any{"entity_type": "VirtualMachine", "entity_id": "vm-42", "limit": float64(25)}, []any{"metrics", "VirtualMachine", "vm-42", 25}},
		{"vsphere_list_tasks", map[string]any{}, []any{"tasks", 100}},
		{"vsphere_list_events", map[string]any{"limit": float64(25)}, []any{"events", 25}},
		{"vsphere_list_alarms", map[string]any{"limit": float64(1001)}, []any{"alarms", 1000}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			service := &originalQueryService{}
			result, err := publicHandlers(service)[tc.tool](context.Background(), tc.params)
			if err != nil || result != `{"legacy":true}` || !reflect.DeepEqual(service.calls, [][]any{tc.want}) {
				t.Fatalf("result=%q error=%v calls=%v, want %v", result, err, service.calls, tc.want)
			}
		})
	}
}

// This service deliberately implements only the original public interface.
type originalQueryService struct{ calls [][]any }

var _ vsphere.QueryService = (*originalQueryService)(nil)

func (s *originalQueryService) record(args ...any) (string, error) {
	s.calls = append(s.calls, args)
	return `{"legacy":true}`, nil
}

func (s *originalQueryService) ListInventory(_ context.Context, kind string, count int) (string, error) {
	return s.record("inventory", kind, count)
}

func (s *originalQueryService) QueryMetrics(_ context.Context, kind, id string, count int) (string, error) {
	return s.record("metrics", kind, id, count)
}

func (s *originalQueryService) ListEvents(_ context.Context, count int) (string, error) {
	return s.record("events", count)
}

func (s *originalQueryService) ListTasks(_ context.Context, count int) (string, error) {
	return s.record("tasks", count)
}

func (s *originalQueryService) ListAlarms(_ context.Context, count int) (string, error) {
	return s.record("alarms", count)
}
