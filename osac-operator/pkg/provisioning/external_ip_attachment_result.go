package provisioning

import (
	"encoding/json"
	"fmt"
)

const externalIPAttachmentResultKey = "osac_result"

const (
	ExternalIPAttachmentAttachOperation = "external_ip_attachment.attach"
	ExternalIPAttachmentDetachOperation = "external_ip_attachment.detach"
)

// ValidateExternalIPAttachmentResult checks the v1 AAP result for an
// ExternalIPAttachment operation. Attachment results carry no operation data.
func ValidateExternalIPAttachmentResult(
	outputs map[string]any,
	operation string,
	resourceUID string,
	generation int64,
) error {
	if operation != ExternalIPAttachmentAttachOperation && operation != ExternalIPAttachmentDetachOperation {
		return fmt.Errorf("unsupported ExternalIPAttachment operation %q", operation)
	}
	if resourceUID == "" || generation < 0 {
		return fmt.Errorf("ExternalIPAttachment UID and nonnegative generation are required")
	}
	encoded, err := json.Marshal(outputs[externalIPAttachmentResultKey])
	if err != nil {
		return fmt.Errorf("ExternalIPAttachment result is malformed")
	}
	var result struct {
		SchemaVersion      string                     `json:"schemaVersion"`
		Operation          string                     `json:"operation"`
		ResourceUID        string                     `json:"resourceUID"`
		ObservedGeneration *int64                     `json:"observedGeneration"`
		Data               map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return fmt.Errorf("ExternalIPAttachment result is malformed")
	}
	if result.SchemaVersion != "v1" || result.Operation != operation ||
		result.ResourceUID != resourceUID || result.ObservedGeneration == nil ||
		*result.ObservedGeneration != generation || result.Data == nil || len(result.Data) != 0 {
		return fmt.Errorf("ExternalIPAttachment result does not match the current operation and resource")
	}
	return nil
}
