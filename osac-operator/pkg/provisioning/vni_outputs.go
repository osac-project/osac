package provisioning

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	maxVNI                 = 1<<24 - 1
	l2VNIKey               = "l2_vni"
	l3VNIKey               = "l3_vni"
	fabricReservedRangeKey = "fabric_reserved_range"
)

type FabricVNIs struct {
	L2VNI *int32
	L3VNI *int32
}

func ParseFabricVNIs(extraVars map[string]any) (FabricVNIs, error) {
	l2VNI, err := parseVNI(extraVars, l2VNIKey)
	if err != nil {
		return FabricVNIs{}, err
	}
	l3VNI, err := parseVNI(extraVars, l3VNIKey)
	if err != nil {
		return FabricVNIs{}, err
	}

	return FabricVNIs{L2VNI: l2VNI, L3VNI: l3VNI}, nil
}

func ParseFabricOutputConfigMap(data map[string]string) (map[string]any, error) {
	for _, key := range []string{l2VNIKey, l3VNIKey, fabricReservedRangeKey} {
		if strings.TrimSpace(data[key]) == "" {
			return nil, fmt.Errorf("fabric output ConfigMap is missing %q", key)
		}
	}

	l2VNI, err := parseVNI(map[string]any{l2VNIKey: data[l2VNIKey]}, l2VNIKey)
	if err != nil {
		return nil, err
	}
	l3VNI, err := parseVNI(map[string]any{l3VNIKey: data[l3VNIKey]}, l3VNIKey)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		l2VNIKey:               *l2VNI,
		l3VNIKey:               *l3VNI,
		fabricReservedRangeKey: data[fabricReservedRangeKey],
	}, nil
}

func parseVNI(extraVars map[string]any, key string) (*int32, error) {
	value, found := extraVars[key]
	if !found || value == nil {
		return nil, nil
	}

	var parsed int64
	switch value := value.(type) {
	case string:
		var err error
		parsed, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, invalidVNI(key, value)
		}
	default:
		var err error
		parsed, err = int64FromNumber(value)
		if err != nil {
			return nil, invalidVNI(key, value)
		}
	}

	if parsed < 1 || parsed > maxVNI {
		return nil, invalidVNI(key, value)
	}
	vni := int32(parsed)
	return &vni, nil
}

func invalidVNI(key string, value any) error {
	return fmt.Errorf("%s must be an integer between 1 and %d, got %v (%T)", key, maxVNI, value, value)
}
