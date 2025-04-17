package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pkg/config/host"
)

func (t *computedTelemetries) TelemetryKey() telemetryKey {
	return t.telemetryKey
}

type ComputedTelemetries struct {
	computedTelemetries
}

func (t *Telemetries) ApplicableTelemetries(proxy *Proxy) ComputedTelemetries {
	return ComputedTelemetries{
		computedTelemetries: t.applicableTelemetries(proxy, nil),
	}
}

func (sc *SidecarScope) DestinationRules() map[host.Name][]*ConsolidatedDestRule {
	return sc.destinationRules
}

func (ps *PushContext) GetMatchedEnvoyFilters(proxy *Proxy, namespaces string) []*EnvoyFilterWrapper {
	return ps.getMatchedEnvoyFilters(proxy, namespaces)
}

func (ps *PushContext) ClusterLocalHosts() ClusterLocalHosts {
	return ps.clusterLocalHosts
}

func (ps *PushContext) MergeGateways(proxy *Proxy) *MergedGateway {
	return ps.mergeGateways(proxy)
}
func (ps *PushContext) MarshalFull() ([]byte, error) {
	return Marshal(ps)
}

func (ef *EnvoyFilterWrapper) FullSpec() *networking.EnvoyFilter {
	return ef.fullSpec
}
func Marshal(v interface{}) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return nil, errors.New("must be a pointer to struct")
	}

	structVal := rv.Elem()
	structType := structVal.Type()

	m := make(map[string]interface{})

	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		jsonTagName := field.Tag.Get("json")
		if jsonTagName == "-" {
			continue
		}

		parts := strings.Split(jsonTagName, ",")
		jsonKey := parts[0]
		if jsonKey == "" {
			jsonKey = field.Name
		}

		omitEmpty := false
		for _, opt := range parts[1:] {
			if opt == "omitempty" {
				omitEmpty = true
				break
			}
		}

		fieldValue := structVal.Field(i)
		var val interface{}

		switch field.Type.Kind() {
		case reflect.Struct:
			// 递归处理嵌套结构
			subVal := reflect.New(field.Type).Interface()
			reflect.ValueOf(subVal).Elem().Set(fieldValue)
			subBytes, err := Marshal(subVal)
			if err != nil {
				return nil, err
			}
			var subMap map[string]interface{}
			if err := json.Unmarshal(subBytes, &subMap); err != nil {
				return nil, err
			}
			val = subMap
		default:
			val = fieldValue.Interface()
		}

		// 检查是否需要跳过
		if omitEmpty && isEmpty(val) {
			continue
		}

		m[jsonKey] = val
	}

	return json.Marshal(m)
}

// isEmpty 判断值是否为零值
func isEmpty(v interface{}) bool {
	if v == nil {
		return true
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan:
		return reflect.ValueOf(v).IsNil()
	case reflect.String:
		return v == ""
	case reflect.Array, reflect.Struct:
		return reflect.DeepEqual(v, reflect.Zero(reflect.TypeOf(v)).Interface())
	case reflect.Bool:
		return !v.(bool)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.(int64) == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.(uint64) == 0
	case reflect.Float32, reflect.Float64:
		return v.(float64) == 0
	default:
		return false
	}
}
