package vsphere

import (
	"context"
	"fmt"
	"sort"

	"github.com/vmware/govmomi/event"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// QueryEvents applies entity and time filters on the target before limiting results.
func (s *Service) QueryEvents(ctx context.Context, filter types.EventFilterSpec) (string, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return "", err
	}
	if client.ServiceContent.EventManager == nil {
		return "", fmt.Errorf("events are not available on this target")
	}
	filter.DisableFullMessage = types.NewBool(false)
	items, err := event.NewManager(client.Client).QueryEvents(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("query events: %w", err)
	}
	return marshal(items)
}

// QueryTasks filters the recent-task cache and selects newest queue times first.
func (s *Service) QueryTasks(ctx context.Context, filter types.TaskFilterSpec, limit int) (string, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return "", err
	}
	if client.ServiceContent.TaskManager == nil {
		return "", fmt.Errorf("tasks are not available on this target")
	}
	collector := property.DefaultCollector(client.Client)
	var manager mo.TaskManager
	if err := collector.RetrieveOne(ctx, *client.ServiceContent.TaskManager, []string{"recentTask"}, &manager); err != nil {
		return "", fmt.Errorf("retrieve recent tasks: %w", err)
	}
	if len(manager.RecentTask) == 0 {
		return retrieveTasks(ctx, collector, nil)
	}
	var headers []mo.Task
	if err := collector.Retrieve(ctx, manager.RecentTask, []string{"info.queueTime", "info.entity"}, &headers); err != nil {
		return "", fmt.Errorf("retrieve task selection fields: %w", err)
	}
	sort.Slice(headers, func(i, j int) bool {
		if !headers[i].Info.QueueTime.Equal(headers[j].Info.QueueTime) {
			return headers[i].Info.QueueTime.After(headers[j].Info.QueueTime)
		}
		return headers[i].Self.Value > headers[j].Self.Value
	})
	selected := make([]types.ManagedObjectReference, 0)
	for _, header := range headers {
		if filter.Entity != nil && (header.Info.Entity == nil || *header.Info.Entity != filter.Entity.Entity) {
			continue
		}
		if filter.Time != nil && ((filter.Time.BeginTime != nil && header.Info.QueueTime.Before(*filter.Time.BeginTime)) ||
			(filter.Time.EndTime != nil && header.Info.QueueTime.After(*filter.Time.EndTime))) {
			continue
		}
		if len(selected) == limit {
			break
		}
		selected = append(selected, header.Self)
	}
	return retrieveTasks(ctx, collector, selected)
}
