package vsphere

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/futuretea/vsphere-mcp-server/pkg/toolset"
)

const (
	Domain       = "vsphere"
	defaultLimit = 100
	maxLimit     = 1000
)

// QueryService provides read-only vSphere queries for MCP tools.
type QueryService interface {
	ListInventory(context.Context, string, int) (string, error)
	QueryMetrics(context.Context, string, string, int) (string, error)
	ListEvents(context.Context, int) (string, error)
	ListTasks(context.Context, int) (string, error)
	ListAlarms(context.Context, int) (string, error)
}

// Toolset exposes the read-only vSphere MCP catalog.
type Toolset struct {
	service         QueryService
	eventsAvailable bool
	alarmsAvailable bool
}

// NewToolset creates a vSphere toolset. A nil service keeps schemas discoverable
// while rejecting calls until a configured target is attached.
func NewToolset(service QueryService, eventsAvailable, alarmsAvailable bool) *Toolset {
	return &Toolset{service: service, eventsAvailable: eventsAvailable, alarmsAvailable: alarmsAvailable}
}

func (t *Toolset) GetName() string { return Domain }

func (t *Toolset) GetDescription() string {
	return "Read-only inventory, metrics, events, and tasks queries for one configured vSphere target"
}

// Close releases a session opened by the underlying query service.
func (t *Toolset) Close(ctx context.Context) error {
	if closer, ok := t.service.(interface{ Close(context.Context) error }); ok {
		return closer.Close(ctx)
	}
	return nil
}

func (t *Toolset) GetTools() []toolset.ServerTool {
	tools := []toolset.ServerTool{
		{
			Tool: mcp.NewTool("vsphere_list_inventory",
				mcp.WithDescription("List inventory objects from the configured ESXi or vCenter target. Common kinds are vm, host, datastore, and network; vCenter also supports datacenter, cluster, resource_pool, folder, and distributed_portgroup."),
				mcp.WithString("kind", mcp.Required(), mcp.Description("Inventory kind to list")),
				mcp.WithNumber("limit", mcp.Description("Maximum results; defaults to 100 and cannot exceed 1000")),
			),
			Handler: t.listInventory,
			Domain:  Domain,
		},
		{
			Tool: mcp.NewTool("vsphere_query_metrics",
				mcp.WithDescription("Query available performance metrics for one inventory object on the configured target."),
				mcp.WithString("entity_type", mcp.Required(), mcp.Description("Managed object type, such as VirtualMachine or HostSystem")),
				mcp.WithString("entity_id", mcp.Required(), mcp.Description("Managed object ID, such as vm-42 or host-12")),
				mcp.WithNumber("limit", mcp.Description("Maximum counters; defaults to 100 and cannot exceed 1000")),
			),
			Handler: t.queryMetrics,
			Domain:  Domain,
		},
		{
			Tool: mcp.NewTool("vsphere_list_tasks",
				mcp.WithDescription("List recent tasks from the configured vSphere target."),
				mcp.WithNumber("limit", mcp.Description("Maximum tasks; defaults to 100 and cannot exceed 1000")),
			),
			Handler: t.listTasks,
			Domain:  Domain,
		},
	}
	if t.eventsAvailable {
		tools = append(tools, toolset.ServerTool{
			Tool: mcp.NewTool("vsphere_list_events",
				mcp.WithDescription("List recent events when the configured target supports event queries."),
				mcp.WithNumber("limit", mcp.Description("Maximum events; defaults to 100 and cannot exceed 1000")),
			),
			Handler: t.listEvents,
			Domain:  Domain,
		})
	}
	if t.alarmsAvailable {
		tools = append(tools, toolset.ServerTool{
			Tool: mcp.NewTool("vsphere_list_alarms",
				mcp.WithDescription("List alarms from the configured target when its AlarmManager is available."),
				mcp.WithNumber("limit", mcp.Description("Maximum alarms; defaults to 100 and cannot exceed 1000")),
			),
			Handler: t.listAlarms,
			Domain:  Domain,
		})
	}
	return tools
}

func (t *Toolset) listInventory(ctx context.Context, params map[string]any) (string, error) {
	kind, err := requiredString(params, "kind")
	if err != nil {
		return "", err
	}
	return t.queryService().ListInventory(ctx, kind, limit(params))
}

func (t *Toolset) queryMetrics(ctx context.Context, params map[string]any) (string, error) {
	entityType, err := requiredString(params, "entity_type")
	if err != nil {
		return "", err
	}
	entityID, err := requiredString(params, "entity_id")
	if err != nil {
		return "", err
	}
	return t.queryService().QueryMetrics(ctx, entityType, entityID, limit(params))
}

func (t *Toolset) listEvents(ctx context.Context, params map[string]any) (string, error) {
	return t.queryService().ListEvents(ctx, limit(params))
}

func (t *Toolset) listTasks(ctx context.Context, params map[string]any) (string, error) {
	return t.queryService().ListTasks(ctx, limit(params))
}

func (t *Toolset) listAlarms(ctx context.Context, params map[string]any) (string, error) {
	return t.queryService().ListAlarms(ctx, limit(params))
}

func (t *Toolset) queryService() QueryService {
	if t.service != nil {
		return t.service
	}
	return unavailableService{}
}

func requiredString(params map[string]any, name string) (string, error) {
	value, _ := params[name].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func limit(params map[string]any) int {
	value, ok := params["limit"].(float64)
	if !ok || value <= 0 {
		return defaultLimit
	}
	if value > maxLimit {
		return maxLimit
	}
	return int(value)
}

type unavailableService struct{}

func (unavailableService) ListInventory(context.Context, string, int) (string, error) {
	return "", fmt.Errorf("vSphere target is not configured")
}

func (unavailableService) QueryMetrics(context.Context, string, string, int) (string, error) {
	return "", fmt.Errorf("vSphere target is not configured")
}

func (unavailableService) ListEvents(context.Context, int) (string, error) {
	return "", fmt.Errorf("vSphere target is not configured")
}

func (unavailableService) ListTasks(context.Context, int) (string, error) {
	return "", fmt.Errorf("vSphere target is not configured")
}

func (unavailableService) ListAlarms(context.Context, int) (string, error) {
	return "", fmt.Errorf("vSphere target is not configured")
}
