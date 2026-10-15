package vsphere

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/futuretea/vsphere-mcp-server/pkg/toolset"
)

func (t *Toolset) resourceTools() []toolset.ServerTool {
	return []toolset.ServerTool{
		{
			Tool: mcp.NewTool("vsphere_query_metric_samples",
				mcp.WithDescription("Read actual performance samples and timestamps. Discover counter names with vsphere_query_metrics. Values are raw vSphere readings; multiply by scale for the named unit (percent scale is 0.01). Empty values mean no readings returned. Historic availability depends on the target's collection settings."),
				mcp.WithString("entity_type", mcp.Required(), mcp.Description("Managed object type, such as VirtualMachine or HostSystem")),
				mcp.WithString("entity_id", mcp.Required(), mcp.Description("Managed object ID from inventory")),
				mcp.WithArray("metrics", mcp.Required(), mcp.WithStringItems(), mcp.MinItems(1), mcp.MaxItems(maxLimit), mcp.Description("Counter names, e.g. cpu.usage.average, mem.usage.average, disk.read.average, net.received.average")),
				mcp.WithString("instance", mcp.Description("Device instance; defaults to aggregate (empty string); '*' requests all instances")),
				mcp.WithInteger("max_samples", mcp.Min(1), mcp.Max(maxLimit), mcp.Description("Maximum samples per series; defaults to 1, maximum 1000")),
				mcp.WithInteger("interval_seconds", mcp.Min(1), mcp.Description("Sampling interval in seconds; defaults to the provider's real-time refresh rate; historical intervals must be enabled on the target")),
				mcp.WithString("start_time", mcp.Description("Optional RFC3339 start time in server time")),
				mcp.WithString("end_time", mcp.Description("Optional RFC3339 end time in server time"))),
			Handler: t.queryMetricSamples, Domain: Domain,
		},
		{
			Tool: mcp.NewTool("vsphere_get_resource",
				mcp.WithDescription("Read one inventory resource's name, path, status and selected properties. VM summary includes CPU/memory configuration, power state, guest information when available, and quick statistics; host/datastore summaries are also available."),
				mcp.WithString("entity_type", mcp.Required(), mcp.Description("Managed object type from inventory")),
				mcp.WithString("entity_id", mcp.Required(), mcp.Description("Managed object ID from inventory"))),
			Handler: t.getResource, Domain: Domain,
		},
	}
}

func activityTool(name, description string, handler toolset.ToolHandler) toolset.ServerTool {
	return toolset.ServerTool{
		Tool: mcp.NewTool(name, mcp.WithDescription(description),
			mcp.WithNumber("limit", mcp.Description("Maximum returned records; defaults to 100 and cannot exceed 1000")),
			mcp.WithString("entity_type", mcp.Description("Optional managed object type; provide together with entity_id")),
			mcp.WithString("entity_id", mcp.Description("Optional managed object ID; provide together with entity_type")),
			mcp.WithString("start_time", mcp.Description("Optional RFC3339 lower time bound; tasks use queue time")),
			mcp.WithString("end_time", mcp.Description("Optional RFC3339 upper time bound; tasks use queue time"))),
		Handler: handler, Domain: Domain,
	}
}

func (t *Toolset) resourceService() (ResourceQueryService, error) {
	if t.service == nil {
		return nil, fmt.Errorf("vSphere target is not configured")
	}
	service, ok := t.service.(ResourceQueryService)
	if !ok {
		return nil, fmt.Errorf("query service does not implement extended resource queries")
	}
	return service, nil
}

func (t *Toolset) listInventoryPage(ctx context.Context, kind string, params map[string]any) (string, error) {
	offset, err := integer(params, "offset", 0, 0, 2147483647)
	if err != nil {
		return "", err
	}
	dc, err := optionalString(params, "datacenter_id")
	if err != nil {
		return "", err
	}
	name, err := optionalString(params, "name")
	if err != nil {
		return "", err
	}
	service, err := t.resourceService()
	if err != nil {
		return "", err
	}
	return service.ListInventoryPage(ctx, kind, dc, name, limit(params), offset)
}

func (t *Toolset) queryMetricSamples(ctx context.Context, params map[string]any) (string, error) {
	entity, err := queryEntity(params, true)
	if err != nil {
		return "", err
	}
	names, err := metricNames(params["metrics"])
	if err != nil {
		return "", err
	}
	instance, err := optionalString(params, "instance")
	if err != nil {
		return "", err
	}
	count, err := integer(params, "max_samples", 1, 1, maxLimit)
	if err != nil {
		return "", err
	}
	interval, err := integer(params, "interval_seconds", 0, 1, 2147483647)
	if err != nil {
		return "", err
	}
	start, end, err := queryWindow(params)
	if err != nil {
		return "", err
	}
	service, err := t.resourceService()
	if err != nil {
		return "", err
	}
	return service.QueryMetricSamples(ctx, types.PerfQuerySpec{
		Entity: *entity, MetricId: []types.PerfMetricId{{Instance: instance}},
		MaxSample: int32(count), IntervalId: int32(interval), StartTime: start, EndTime: end,
	}, names)
}

func (t *Toolset) getResource(ctx context.Context, params map[string]any) (string, error) {
	entity, err := queryEntity(params, true)
	if err != nil {
		return "", err
	}
	service, err := t.resourceService()
	if err != nil {
		return "", err
	}
	return service.GetResource(ctx, entity.Type, entity.Value)
}

func (t *Toolset) listTriggeredAlarms(ctx context.Context, params map[string]any) (string, error) {
	entity, err := queryEntity(params, false)
	if err != nil {
		return "", err
	}
	service, err := t.resourceService()
	if err != nil {
		return "", err
	}
	if entity == nil {
		return service.ListTriggeredAlarms(ctx, "", "", limit(params))
	}
	return service.ListTriggeredAlarms(ctx, entity.Type, entity.Value, limit(params))
}

func metricNames(value any) ([]string, error) {
	var names []string
	switch items := value.(type) {
	case []string:
		names = append(names, items...)
	case []any:
		for _, item := range items {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("metrics must be an array of counter names")
			}
			names = append(names, name)
		}
	}
	if len(names) == 0 || len(names) > maxLimit {
		return nil, fmt.Errorf("metrics must contain between 1 and 1000 counter names")
	}
	for i, name := range names {
		names[i] = strings.TrimSpace(name)
		if names[i] == "" {
			return nil, fmt.Errorf("metrics must not contain empty counter names")
		}
	}
	return names, nil
}
