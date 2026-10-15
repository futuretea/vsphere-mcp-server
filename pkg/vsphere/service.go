package vsphere

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/alarm"
	"github.com/vmware/govmomi/fault"
	"github.com/vmware/govmomi/performance"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/futuretea/vsphere-mcp-server/pkg/core/config"
)

// Service implements read-only queries against one configured vSphere target.
type Service struct {
	target config.VSphereConfig

	mu     sync.Mutex
	client *govmomi.Client
}

// Capabilities describes optional read-only services exposed by a target.
type Capabilities struct {
	Events bool
	Alarms bool
}

// NewService creates a lazily connected vSphere query service.
func NewService(target config.VSphereConfig) *Service {
	return &Service{target: target}
}

// Close logs out the client session created by this service.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	client := s.client
	s.client = nil
	s.mu.Unlock()
	if client == nil {
		return nil
	}
	if err := client.Logout(ctx); err != nil {
		return fmt.Errorf("logout from vSphere target: %w", err)
	}
	return nil
}

// Capabilities reads the target service directory without changing target state.
func (s *Service) Capabilities(ctx context.Context) (Capabilities, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return Capabilities{}, err
	}
	capabilities := Capabilities{Alarms: client.ServiceContent.AlarmManager != nil}
	if client.ServiceContent.EventManager != nil {
		_, err := s.ListEvents(ctx, 1)
		if err != nil && !fault.Is(err, &types.NotImplemented{}) && !fault.Is(err, &types.NotSupported{}) {
			return Capabilities{}, fmt.Errorf("discover event capability: %w", err)
		}
		capabilities.Events = err == nil
	}
	return capabilities, nil
}

// ListInventory lists a supported inventory kind without changing target state.
func (s *Service) ListInventory(ctx context.Context, kind string, limit int) (string, error) {
	return s.ListInventoryPage(ctx, kind, "", "", limit, 0)
}

// QueryMetrics lists available performance counters for one managed object.
func (s *Service) QueryMetrics(ctx context.Context, entityType, entityID string, limit int) (string, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return "", err
	}
	if client.ServiceContent.PerfManager == nil {
		return "", fmt.Errorf("performance metrics are not available on this target")
	}

	entity := types.ManagedObjectReference{Type: entityType, Value: entityID}
	manager := performance.NewManager(client.Client)
	metrics, err := manager.AvailableMetric(ctx, entity, performance.Intervals["real"])
	if err != nil {
		return "", fmt.Errorf("query available metrics: %w", err)
	}
	counters, err := manager.CounterInfoByKey(ctx)
	if err != nil {
		return "", fmt.Errorf("query metric counters: %w", err)
	}

	result := make([]metric, 0, min(limit, len(metrics)))
	for _, candidate := range metrics {
		if len(result) == limit {
			break
		}
		counter, ok := counters[candidate.CounterId]
		if !ok {
			continue
		}
		result = append(result, metric{ID: candidate.CounterId, Instance: candidate.Instance, Name: counter.Name(), Unit: counter.UnitInfo.GetElementDescription().Key})
	}
	return marshal(result)
}

// ListEvents lists recent events without creating an event collector.
func (s *Service) ListEvents(ctx context.Context, limit int) (string, error) {
	return s.QueryEvents(ctx, types.EventFilterSpec{MaxCount: int32(limit)})
}

// ListTasks lists recent tasks without creating a task collector.
func (s *Service) ListTasks(ctx context.Context, limit int) (string, error) {
	return s.QueryTasks(ctx, types.TaskFilterSpec{}, limit)
}

func retrieveTasks(ctx context.Context, collector *property.Collector, references []types.ManagedObjectReference) (string, error) {
	if len(references) == 0 {
		return marshal([]mo.Task{})
	}
	var tasks []mo.Task
	if err := collector.Retrieve(ctx, references, []string{"info"}, &tasks); err != nil {
		return "", fmt.Errorf("retrieve task details: %w", err)
	}
	return marshal(tasks)
}

// ListAlarms lists configured alarms when the target advertises AlarmManager.
func (s *Service) ListAlarms(ctx context.Context, limit int) (string, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return "", err
	}
	manager, err := alarm.GetManager(client.Client)
	if err != nil {
		return "", fmt.Errorf("alarms are not available on this target: %w", err)
	}
	alarms, err := manager.GetAlarm(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("query alarms: %w", err)
	}
	if len(alarms) > limit {
		alarms = alarms[:limit]
	}
	return marshal(alarms)
}

func (s *Service) clientFor(ctx context.Context) (*govmomi.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	if strings.TrimSpace(s.target.Endpoint) == "" {
		return nil, fmt.Errorf("vSphere target is not configured")
	}

	target, err := targetURL(s.target)
	if err != nil {
		return nil, err
	}
	client, err := govmomi.NewClient(ctx, target, s.target.Insecure)
	if err != nil {
		return nil, fmt.Errorf("connect to vSphere target: %w", err)
	}
	s.client = client
	return client, nil
}

func targetURL(target config.VSphereConfig) (*url.URL, error) {
	endpoint := strings.TrimSpace(target.Endpoint)
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid vSphere endpoint")
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("vSphere endpoint must use https")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/sdk"
	}
	if target.Username != "" {
		parsed.User = url.UserPassword(target.Username, target.Password)
	}
	return parsed, nil
}

type inventoryObject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}
type metric struct {
	ID       int32  `json:"id"`
	Instance string `json:"instance"`
	Name     string `json:"name"`
	Unit     string `json:"unit"`
}

func marshal(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode query result: %w", err)
	}
	return string(data), nil
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
