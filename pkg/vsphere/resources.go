package vsphere

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/govmomi/alarm"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

func resourceReference(entityType, entityID string) (types.ManagedObjectReference, error) {
	entityType, entityID = strings.TrimSpace(entityType), strings.TrimSpace(entityID)
	for _, kinds := range inventoryTypes {
		if containsType(kinds, entityType) && entityID != "" {
			return types.ManagedObjectReference{Type: entityType, Value: entityID}, nil
		}
	}
	return types.ManagedObjectReference{}, fmt.Errorf("a supported inventory entity_type and non-empty entity_id are required")
}

// GetResource reads common identity and selected resource properties, not full configuration.
func (s *Service) GetResource(ctx context.Context, entityType, entityID string) (string, error) {
	ref, err := resourceReference(entityType, entityID)
	if err != nil {
		return "", err
	}
	client, err := s.clientFor(ctx)
	if err != nil {
		return "", err
	}
	properties := []string{"name", "overallStatus"}
	switch ref.Type {
	case "VirtualMachine", "HostSystem", "Datastore":
		properties = []string{"name", "summary"}
	}
	var objects []types.ObjectContent
	if err := property.DefaultCollector(client.Client).Retrieve(ctx, []types.ManagedObjectReference{ref}, properties, &objects); err != nil {
		return "", fmt.Errorf("retrieve resource details: %w", err)
	}
	if len(objects) == 0 {
		return "", fmt.Errorf("resource not found or not accessible")
	}
	for _, missing := range objects[0].MissingSet {
		return "", fmt.Errorf("retrieve resource property %s: %w", missing.Path, soap.WrapVimFault(missing.Fault.Fault))
	}
	path, err := find.InventoryPath(ctx, client.Client, ref)
	if err != nil {
		return "", fmt.Errorf("retrieve resource path: %w", err)
	}
	result := struct {
		inventoryObject
		Properties map[string]any `json:"properties"`
	}{inventoryObject: inventoryObject{Type: ref.Type, ID: ref.Value, Path: path}, Properties: make(map[string]any)}
	for _, value := range objects[0].PropSet {
		if value.Name == "name" {
			result.Name = value.Val.(string)
		} else {
			result.Properties[value.Name] = value.Val
		}
	}
	return marshal(result)
}

type triggeredAlarm struct {
	types.AlarmState
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
}

// ListTriggeredAlarms returns current state and readable names independently of alarm definitions.
func (s *Service) ListTriggeredAlarms(ctx context.Context, entityType, entityID string, limit int) (string, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return "", err
	}
	ref := client.ServiceContent.RootFolder
	if entityType != "" || entityID != "" {
		ref, err = resourceReference(entityType, entityID)
		if err != nil {
			return "", err
		}
	}
	manager, err := alarm.GetManager(client.Client)
	if err != nil {
		return "", fmt.Errorf("alarms are not available on this target: %w", err)
	}
	states, err := manager.GetStateInfo(ctx, object.NewCommon(client.Client, ref), alarm.StateInfoOptions{InventoryPath: true})
	if err != nil {
		return "", fmt.Errorf("query triggered alarms: %w", err)
	}
	result := make([]triggeredAlarm, 0, min(limit, len(states)))
	for _, state := range states {
		if len(result) == limit {
			break
		}
		row := triggeredAlarm{AlarmState: state.AlarmState, Path: state.Path}
		if state.Info != nil {
			row.Name, row.Description = state.Info.Name, state.Info.Description
		}
		result = append(result, row)
	}
	return marshal(result)
}
