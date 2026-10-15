package vsphere

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

var inventoryTypes = map[string][]string{
	"vm": {"VirtualMachine"}, "host": {"HostSystem"},
	"datastore":  {"Datastore"},
	"network":    {"Network", "OpaqueNetwork", "DistributedVirtualPortgroup", "DistributedVirtualSwitch", "VmwareDistributedVirtualSwitch"},
	"datacenter": {"Datacenter"}, "cluster": {"ClusterComputeResource"},
	"resource_pool": {"ResourcePool"}, "folder": {"Folder", "StoragePod"},
	"distributed_portgroup": {"DistributedVirtualPortgroup"},
}

// ListInventoryPage lists identifiable resources across data centers in stable path order.
func (s *Service) ListInventoryPage(ctx context.Context, kind, datacenterID, name string, limit, offset int) (string, error) {
	kinds, ok := inventoryTypes[strings.TrimSpace(kind)]
	if !ok {
		return "", fmt.Errorf("unsupported inventory kind %q", kind)
	}
	if limit <= 0 || offset < 0 {
		return "", fmt.Errorf("limit must be positive and offset must be non-negative")
	}
	entities, err := s.inventoryEntities(ctx)
	if err != nil {
		return "", err
	}
	paths := inventoryPaths(entities)
	dcPath := ""
	if datacenterID != "" {
		var exists bool
		dcPath, exists = paths[types.ManagedObjectReference{Type: "Datacenter", Value: datacenterID}]
		if !exists {
			return "", fmt.Errorf("datacenter %q not found or not accessible", datacenterID)
		}
	}
	rows := make([]inventoryObject, 0)
	for _, entity := range entities {
		if !containsType(kinds, entity.Self.Type) || (name != "" && entity.Name != name) {
			continue
		}
		path := paths[entity.Self]
		if dcPath != "" && path != dcPath && !strings.HasPrefix(path, dcPath+"/") {
			continue
		}
		rows = append(rows, inventoryObject{Type: entity.Self.Type, ID: entity.Self.Value, Name: entity.Name, Path: path})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Path != rows[j].Path {
			return rows[i].Path < rows[j].Path
		}
		return rows[i].Type+":"+rows[i].ID < rows[j].Type+":"+rows[j].ID
	})
	if offset >= len(rows) {
		return marshal([]inventoryObject{})
	}
	return marshal(rows[offset : offset+min(limit, len(rows)-offset)])
}

func (s *Service) inventoryEntities(ctx context.Context) ([]mo.ManagedEntity, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	container, err := view.NewManager(client.Client).CreateContainerView(ctx, client.ServiceContent.RootFolder, []string{"ManagedEntity"}, true)
	if err != nil {
		return nil, fmt.Errorf("create inventory view: %w", err)
	}
	defer func() { _ = container.Destroy(context.WithoutCancel(ctx)) }()
	var entities []mo.ManagedEntity
	if err := container.Retrieve(ctx, []string{"ManagedEntity"}, []string{"name", "parent"}, &entities); err != nil {
		return nil, fmt.Errorf("retrieve inventory: %w", err)
	}
	return entities, nil
}

func inventoryPaths(entities []mo.ManagedEntity) map[types.ManagedObjectReference]string {
	byRef := make(map[types.ManagedObjectReference]mo.ManagedEntity, len(entities))
	paths := make(map[types.ManagedObjectReference]string, len(entities))
	for _, entity := range entities {
		byRef[entity.Self] = entity
	}
	var pathFor func(types.ManagedObjectReference) string
	pathFor = func(ref types.ManagedObjectReference) string {
		if path, ok := paths[ref]; ok {
			return path
		}
		entity, ok := byRef[ref]
		if !ok {
			return ""
		}
		parent := ""
		if entity.Parent != nil {
			parent = pathFor(*entity.Parent)
		}
		paths[ref] = parent + "/" + entity.Name
		return paths[ref]
	}
	for _, entity := range entities {
		pathFor(entity.Self)
	}
	return paths
}

func containsType(kinds []string, kind string) bool {
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}
