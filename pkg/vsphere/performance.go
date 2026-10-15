package vsphere

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/vmware/govmomi/performance"
	"github.com/vmware/govmomi/vim25/types"
)

type sampledMetric struct {
	metric
	Values []int64 `json:"values"`
	Scale  float64 `json:"scale"`
}

type metricSamples struct {
	Entity     inventoryObject        `json:"entity"`
	SampleInfo []types.PerfSampleInfo `json:"sample_info"`
	Metrics    []sampledMetric        `json:"metrics"`
}

// QueryMetricSamples returns raw readings and the scale used to display their units.
func (s *Service) QueryMetricSamples(ctx context.Context, spec types.PerfQuerySpec, names []string) (string, error) {
	if _, err := resourceReference(spec.Entity.Type, spec.Entity.Value); err != nil {
		return "", err
	}
	if len(names) == 0 || spec.MaxSample < 1 || spec.MaxSample > 1000 {
		return "", fmt.Errorf("metrics are required and max_samples must be between 1 and 1000")
	}
	client, err := s.clientFor(ctx)
	if err != nil {
		return "", err
	}
	if client.ServiceContent.PerfManager == nil {
		return "", fmt.Errorf("performance metrics are not available on this target")
	}
	manager := performance.NewManager(client.Client)
	if err := sampleInterval(ctx, manager, &spec); err != nil {
		return "", err
	}
	spec.Format = string(types.PerfFormatNormal)
	if spec.EndTime != nil && spec.StartTime == nil {
		start := spec.EndTime.Add(-time.Duration(spec.IntervalId) * time.Duration(spec.MaxSample) * 2 * time.Second)
		spec.StartTime = &start
	}
	raw, err := manager.SampleByName(ctx, spec, names, []types.ManagedObjectReference{spec.Entity})
	if err != nil {
		return "", fmt.Errorf("query performance samples: %w", err)
	}
	series, err := manager.ToMetricSeries(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("decode performance samples: %w", err)
	}
	counters, err := manager.CounterInfoByName(ctx)
	if err != nil {
		return "", fmt.Errorf("query metric counters: %w", err)
	}
	result := metricSamples{
		Entity:     inventoryObject{Type: spec.Entity.Type, ID: spec.Entity.Value},
		SampleInfo: []types.PerfSampleInfo{}, Metrics: []sampledMetric{},
	}
	if len(series) != 0 {
		if err := projectSamples(&result, series[0], counters, int(spec.MaxSample)); err != nil {
			return "", err
		}
	}
	return marshal(result)
}

func sampleInterval(ctx context.Context, manager *performance.Manager, spec *types.PerfQuerySpec) error {
	provider, err := manager.ProviderSummary(ctx, spec.Entity)
	if err != nil {
		return fmt.Errorf("query performance provider: %w", err)
	}
	if spec.IntervalId == 0 {
		if !provider.CurrentSupported || provider.RefreshRate <= 0 {
			return fmt.Errorf("real-time samples are not supported for this entity; specify a supported historical interval_seconds")
		}
		spec.IntervalId = provider.RefreshRate
	}
	if provider.CurrentSupported && spec.IntervalId == provider.RefreshRate {
		return nil
	}
	intervals, err := manager.HistoricalInterval(ctx)
	if err != nil {
		return fmt.Errorf("query historical intervals: %w", err)
	}
	for _, interval := range intervals {
		if provider.SummarySupported && interval.Enabled && interval.SamplingPeriod == spec.IntervalId {
			return nil
		}
	}
	return fmt.Errorf("interval_seconds %d is not supported for this entity", spec.IntervalId)
}

func projectSamples(result *metricSamples, series performance.EntityMetric, counters map[string]*types.PerfCounterInfo, maxSamples int) error {
	indices := make([]int, len(series.SampleInfo))
	for i := range indices {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool {
		return series.SampleInfo[indices[i]].Timestamp.Before(series.SampleInfo[indices[j]].Timestamp)
	})
	if len(indices) > maxSamples {
		indices = indices[len(indices)-maxSamples:]
	}
	for _, i := range indices {
		result.SampleInfo = append(result.SampleInfo, series.SampleInfo[i])
	}
	for _, values := range series.Value {
		if len(values.Value) != 0 && len(values.Value) != len(series.SampleInfo) {
			return fmt.Errorf("metric %q returned %d values for %d sampling times", values.Name, len(values.Value), len(series.SampleInfo))
		}
		sample := sampledMetric{
			metric: metric{ID: counters[values.Name].Key, Instance: values.Instance, Name: values.Name, Unit: values.Unit},
			Values: []int64{}, Scale: 1,
		}
		if values.Unit == string(types.PerformanceManagerUnitPercent) {
			sample.Scale = 0.01
		}
		if len(values.Value) != 0 {
			for _, i := range indices {
				sample.Values = append(sample.Values, values.Value[i])
			}
		}
		result.Metrics = append(result.Metrics, sample)
	}
	return nil
}
