package vsphere

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/vmware/govmomi/vim25/types"
)

func hasParameters(params map[string]any, names ...string) bool {
	for _, name := range names {
		if _, ok := params[name]; ok {
			return true
		}
	}
	return false
}

func optionalString(params map[string]any, name string) (string, error) {
	value, exists := params[name]
	if !exists {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return strings.TrimSpace(text), nil
}

func integer(params map[string]any, name string, fallback, low, high int) (int, error) {
	value, exists := params[name]
	if !exists {
		return fallback, nil
	}
	var number float64
	switch value := value.(type) {
	case float64:
		number = value
	case int:
		number = float64(value)
	default:
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || number < float64(low) || number > float64(high) {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, low, high)
	}
	return int(number), nil
}

func queryEntity(params map[string]any, required bool) (*types.ManagedObjectReference, error) {
	entityType, err := optionalString(params, "entity_type")
	if err != nil {
		return nil, err
	}
	entityID, err := optionalString(params, "entity_id")
	if err != nil {
		return nil, err
	}
	if !required && entityType == "" && entityID == "" {
		return nil, nil
	}
	if entityType == "" || entityID == "" {
		return nil, fmt.Errorf("entity_type and entity_id must be provided together")
	}
	return &types.ManagedObjectReference{Type: entityType, Value: entityID}, nil
}

func queryWindow(params map[string]any) (*time.Time, *time.Time, error) {
	var bounds [2]*time.Time
	for i, name := range []string{"start_time", "end_time"} {
		text, err := optionalString(params, name)
		if err != nil {
			return nil, nil, err
		}
		if text == "" {
			continue
		}
		value, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return nil, nil, fmt.Errorf("%s must be an RFC3339 timestamp", name)
		}
		bounds[i] = &value
	}
	if bounds[0] != nil && bounds[1] != nil && bounds[0].After(*bounds[1]) {
		return nil, nil, fmt.Errorf("start_time must not be after end_time")
	}
	return bounds[0], bounds[1], nil
}

func eventFilter(params map[string]any) (types.EventFilterSpec, error) {
	entity, err := queryEntity(params, false)
	if err != nil {
		return types.EventFilterSpec{}, err
	}
	start, end, err := queryWindow(params)
	if err != nil {
		return types.EventFilterSpec{}, err
	}
	filter := types.EventFilterSpec{MaxCount: int32(limit(params))}
	if entity != nil {
		filter.Entity = &types.EventFilterSpecByEntity{Entity: *entity, Recursion: types.EventFilterSpecRecursionOptionSelf}
	}
	if start != nil || end != nil {
		filter.Time = &types.EventFilterSpecByTime{BeginTime: start, EndTime: end}
	}
	return filter, nil
}
