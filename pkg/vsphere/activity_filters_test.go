package vsphere_test

import (
	"testing"
	"time"

	"github.com/vmware/govmomi/event"
	"github.com/vmware/govmomi/vim25/types"
)

// These synthetic cases exercise filtering through the public MCP handlers.
func TestActivityFiltersEventsBeforeLimit(t *testing.T) {
	f := newQueryFixture(t, 1)
	manager := event.NewManager(f.client.Client)
	labels := []string{"target-old", "target-selected", "other-vm", "target-new"}
	for i, label := range labels {
		ref := f.vms[0].Reference()
		if i == 2 {
			ref = f.vms[1].Reference()
		}
		entry := &types.EventEx{Event: types.Event{Vm: &types.VmEventArgument{Vm: ref}},
			EventTypeId: "local.activity.filter", ObjectType: ref.Type, ObjectId: ref.Value, Message: label}
		if err := manager.PostEvent(f.ctx, entry); err != nil {
			t.Fatalf("post synthetic event: %v", err)
		}
	}
	entries, err := manager.QueryEvents(f.ctx, types.EventFilterSpec{MaxCount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	observed := make(map[string]*types.EventEx)
	for _, entry := range entries {
		if entry, ok := entry.(*types.EventEx); ok && entry.EventTypeId == "local.activity.filter" {
			observed[entry.Message] = entry
		}
	}
	for i, label := range labels {
		if observed[label] == nil {
			t.Fatalf("fixture event %q was not persisted", label)
		}
		if i > 0 && !observed[label].CreatedTime.After(observed[labels[i-1]].CreatedTime) {
			t.Fatal("fixture events do not have distinct increasing provider timestamps")
		}
	}
	midpoint := func(a, b time.Time) time.Time { return a.Add(b.Sub(a) / 2) }
	end := midpoint(observed[labels[2]].CreatedTime, observed[labels[3]].CreatedTime)
	for _, test := range []struct {
		name  string
		start time.Time
		count int
	}{
		{"matching_event_behind_newer_nonmatches", midpoint(observed[labels[0]].CreatedTime, observed[labels[1]].CreatedTime), 1},
		{"window_contains_only_other_vm", midpoint(observed[labels[1]].CreatedTime, observed[labels[2]].CreatedTime), 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var rows []struct {
				Key         int32
				CreatedTime time.Time
				Vm          *types.VmEventArgument
				Message     string
			}
			f.call(t, "vsphere_list_events", map[string]any{
				"entity_type": "VirtualMachine", "entity_id": f.vms[0].Reference().Value,
				"start_time": test.start.Format(time.RFC3339Nano), "end_time": end.Format(time.RFC3339Nano), "limit": float64(1),
			}, &rows)
			if len(rows) != test.count {
				t.Fatalf("filtered event count = %d, want %d; newest unrelated records must not consume limit", len(rows), test.count)
			}
			if len(rows) == 1 {
				got, want := rows[0], observed["target-selected"]
				if got.Key != want.Key || got.Message != want.Message || !got.CreatedTime.Equal(want.CreatedTime) || got.Vm == nil || got.Vm.Vm != f.vms[0].Reference() {
					t.Fatalf("returned wrong VM/time event: %+v; want key %d", got, want.Key)
				}
			}
		})
	}
}

func TestActivityFiltersTasksBeforeLimit(t *testing.T) {
	f := newQueryFixture(t, 1)
	base := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	var selected types.ManagedObjectReference
	for i, label := range []string{"target-old", "target-selected", "other-vm", "target-new"} {
		vm := f.vms[0]
		if i == 2 {
			vm = f.vms[1]
		}
		task, err := vm.Rename(f.ctx, label)
		if err != nil {
			t.Fatalf("rename synthetic VM: %v", err)
		}
		if err := task.Wait(f.ctx); err != nil {
			t.Fatalf("complete synthetic task: %v", err)
		}
		simctx := f.model.Service.Context
		simctx.Map.AtomicUpdate(simctx, simctx.Map.Get(task.Reference()), []types.PropertyChange{{
			Name: "info.queueTime", Val: base.Add(time.Duration((i+1)*10) * time.Second),
		}})
		if i == 1 {
			selected = task.Reference()
		}
	}
	for _, test := range []struct {
		name    string
		seconds int
		count   int
	}{
		{"matching_task_behind_newer_nonmatches", 15, 1},
		{"window_contains_only_other_vm", 25, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var rows []struct {
				Info struct {
					Task      types.ManagedObjectReference
					Entity    *types.ManagedObjectReference
					QueueTime time.Time
				}
			}
			f.call(t, "vsphere_list_tasks", map[string]any{
				"entity_type": "VirtualMachine", "entity_id": f.vms[0].Reference().Value,
				"start_time": base.Add(time.Duration(test.seconds) * time.Second).Format(time.RFC3339),
				"end_time":   base.Add(35 * time.Second).Format(time.RFC3339), "limit": float64(1),
			}, &rows)
			if len(rows) != test.count {
				t.Fatalf("filtered task count = %d, want %d; newer unrelated records must not consume limit", len(rows), test.count)
			}
			if len(rows) == 1 {
				got := rows[0].Info
				if got.Task != selected || got.Entity == nil || *got.Entity != f.vms[0].Reference() || !got.QueueTime.Equal(base.Add(20*time.Second)) {
					t.Fatalf("returned wrong VM/time task: %+v; want %s", got, selected)
				}
			}
		})
	}
}
