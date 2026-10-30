package provisioning

import "fmt"

const (
	NATGatewayEnsureOperation = "nat_gateway.ensure"
	NATGatewayDeleteOperation = "nat_gateway.delete"
)

// ValidateNATGatewayResult validates a NATGateway v1 result artifact.
func ValidateNATGatewayResult(
	outputs map[string]any,
	operation string,
	resourceUID string,
	generation int64,
) error {
	if operation != NATGatewayEnsureOperation && operation != NATGatewayDeleteOperation {
		return fmt.Errorf("unsupported NATGateway operation %q", operation)
	}
	data, err := validateAAPResultEnvelope(
		outputs, aapResultKey, "NATGateway", operation, resourceUID, generation,
	)
	if err != nil {
		return err
	}
	if len(data) != 0 {
		return fmt.Errorf("NATGateway result data must be empty")
	}
	return nil
}
