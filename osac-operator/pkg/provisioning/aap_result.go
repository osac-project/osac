package provisioning

import (
	"encoding/json"
	"fmt"
)

const aapResultKey = "osac_result"

const (
	externalIPResultKey           = aapResultKey
	externalIPAttachmentResultKey = aapResultKey
)

type aapResultEnvelope struct {
	SchemaVersion      string                     `json:"schemaVersion"`
	Operation          string                     `json:"operation"`
	ResourceUID        string                     `json:"resourceUID"`
	ObservedGeneration *int64                     `json:"observedGeneration"`
	Data               map[string]json.RawMessage `json:"data"`
}

func validateAAPResultEnvelope(
	outputs map[string]any,
	resultKey string,
	resource string,
	operation string,
	resourceUID string,
	generation int64,
) (map[string]json.RawMessage, error) {
	if resourceUID == "" || generation < 0 {
		return nil, fmt.Errorf("%s UID and nonnegative generation are required", resource)
	}
	encoded, err := json.Marshal(outputs[resultKey])
	if err != nil {
		return nil, fmt.Errorf("%s result is malformed: %w", resource, err)
	}
	var result aapResultEnvelope
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("%s result is malformed: %w", resource, err)
	}
	if result.SchemaVersion != "v1" || result.Operation != operation ||
		result.ResourceUID != resourceUID || result.ObservedGeneration == nil ||
		*result.ObservedGeneration != generation || result.Data == nil {
		return nil, fmt.Errorf("%s result does not match the current operation and resource", resource)
	}
	return result.Data, nil
}
