package provisioning

import (
	"fmt"
)

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
	data, err := validateAAPResultEnvelope(
		outputs,
		externalIPAttachmentResultKey,
		"ExternalIPAttachment",
		operation,
		resourceUID,
		generation,
	)
	if err != nil {
		return err
	}
	if len(data) != 0 {
		return fmt.Errorf("ExternalIPAttachment result does not match the current operation and resource")
	}
	return nil
}
