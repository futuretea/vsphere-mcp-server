package vsphere_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/futuretea/vsphere-mcp-server/pkg/core/config"
	"github.com/futuretea/vsphere-mcp-server/pkg/vsphere"
)

type inventoryCompatibilityFixture struct {
	ctx     context.Context
	service *vsphere.Service
	finder  *find.Finder
	pod     types.ManagedObjectReference
}

func newInventoryCompatibilityFixture(t *testing.T) inventoryCompatibilityFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	model := simulator.VPX()
	model.Host, model.ClusterHost, model.Machine = 1, 1, 1
	model.Portgroup, model.OpaqueNetwork, model.Pool = 1, 1, 1
	if err := model.Create(); err != nil {
		t.Fatalf("create synthetic inventory: %v", err)
	}
	t.Cleanup(model.Remove)
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	t.Cleanup(server.Close)
	client, err := govmomi.NewClient(ctx, server.URL, true)
	if err != nil {
		t.Fatal("connect synthetic inventory")
	}
	t.Cleanup(func() { _ = client.Logout(context.Background()) })
	finder := find.NewFinder(client.Client, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finder.SetDatacenter(dc)
	folders, err := dc.Folders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pod, err := folders.DatastoreFolder.CreateStoragePod(ctx, "synthetic-storage-cluster")
	if err != nil {
		t.Fatalf("create storage cluster through SDK: %v", err)
	}
	service := vsphere.NewService(config.VSphereConfig{Endpoint: server.URL.String(), Insecure: true})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	return inventoryCompatibilityFixture{ctx, service, finder, pod.Reference()}
}

func TestInventoryCompatibilityPreservesFinderMembership(t *testing.T) {
	f := newInventoryCompatibilityFixture(t)
	for _, kind := range []string{"vm", "host", "datastore", "network", "datacenter", "cluster", "resource_pool", "folder", "distributed_portgroup"} {
		t.Run(kind, func(t *testing.T) {
			want, err := finderInventoryReferences(f.ctx, f.finder, kind)
			if err != nil {
				t.Fatalf("legacy Finder %s: %v", kind, err)
			}
			counts := make(map[string]int)
			for ref := range want {
				counts[ref.Type]++
			}
			t.Logf("legacy Finder %s membership by actual object type: %v", kind, counts)
			if kind == "network" && counts["DistributedVirtualSwitch"]+counts["VmwareDistributedVirtualSwitch"] == 0 {
				t.Fatal("fixture did not provide a real distributed switch")
			}
			if kind == "folder" && !want[f.pod] {
				t.Fatal("legacy Finder did not return the SDK-created storage cluster")
			}
			result, err := f.service.ListInventory(f.ctx, kind, 1000)
			if err != nil {
				t.Fatalf("public inventory %s: %v", kind, err)
			}
			var rows []struct{ Type, ID string }
			if err := json.Unmarshal([]byte(result), &rows); err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				ref := types.ManagedObjectReference{Type: row.Type, Value: row.ID}
				if !want[ref] {
					t.Errorf("kind %s returned unexpected or duplicate reference %s", kind, ref)
				}
				delete(want, ref)
			}
			for ref := range want {
				t.Errorf("kind %s omitted legacy Finder member %s", kind, ref)
			}
		})
	}
}

type sdkInventoryReference interface {
	Reference() types.ManagedObjectReference
}

func inventoryReferenceSet[T sdkInventoryReference](items []T, err error) (map[types.ManagedObjectReference]bool, error) {
	if err != nil {
		return nil, err
	}
	result := make(map[types.ManagedObjectReference]bool, len(items))
	for _, item := range items {
		result[item.Reference()] = true
	}
	return result, nil
}

// Each branch calls the same public Finder list used by the original service.
func finderInventoryReferences(ctx context.Context, finder *find.Finder, kind string) (map[types.ManagedObjectReference]bool, error) {
	switch kind {
	case "vm":
		return inventoryReferenceSet(finder.VirtualMachineList(ctx, "*"))
	case "host":
		return inventoryReferenceSet(finder.HostSystemList(ctx, "*"))
	case "datastore":
		return inventoryReferenceSet(finder.DatastoreList(ctx, "*"))
	case "network":
		return inventoryReferenceSet(finder.NetworkList(ctx, "*"))
	case "datacenter":
		return inventoryReferenceSet(finder.DatacenterList(ctx, "*"))
	case "cluster":
		return inventoryReferenceSet(finder.ClusterComputeResourceList(ctx, "*"))
	case "resource_pool":
		return inventoryReferenceSet(finder.ResourcePoolList(ctx, "*"))
	case "folder":
		return inventoryReferenceSet(finder.FolderList(ctx, "*"))
	case "distributed_portgroup":
		refs, err := inventoryReferenceSet(finder.NetworkList(ctx, "*"))
		for ref := range refs {
			if ref.Type != "DistributedVirtualPortgroup" {
				delete(refs, ref)
			}
		}
		return refs, err
	default:
		return nil, fmt.Errorf("unsupported legacy inventory kind %q", kind)
	}
}
