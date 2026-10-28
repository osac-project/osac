package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/osac-project/osac-metering/schema"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func HeartbeatIdentity(resourceType string, dimensions map[string]any, sliceStart *time.Time) (string, error) {
	identity, err := json.Marshal(struct {
		ResourceType string         `json:"resource_type"`
		Dimensions   map[string]any `json:"dimensions"`
		SliceStart   *time.Time     `json:"slice_start"`
	}{
		ResourceType: resourceType,
		Dimensions:   dimensions,
		SliceStart:   sliceStart,
	})
	if err != nil {
		return "", fmt.Errorf("marshalling heartbeat identity: %w", err)
	}
	hash := sha256.Sum256(identity)
	return hex.EncodeToString(hash[:]), nil
}

var requiredBillingDimensions = map[string][]string{
	schema.ResourceTypeExternalIP: {
		"deployment",
		"pool",
		"ip_family",
		"attached",
		"tenant_id",
	},
	schema.ResourceTypeNATGateway: {
		"deployment",
		"virtual_network",
		"external_ip",
		"tenant_id",
		"project_id",
	},
}

var networkingResourceTypes = map[string]struct{}{
	schema.ResourceTypeExternalIP: {},
	schema.ResourceTypeNATGateway: {},
}

func IsNetworkingResourceType(resourceType string) bool {
	_, ok := networkingResourceTypes[resourceType]
	return ok
}

func validateNetworkingBillingDimensions(resourceType string, dimensions map[string]any) error {
	for _, key := range requiredBillingDimensions[resourceType] {
		value, ok := dimensions[key]
		if !ok {
			return fmt.Errorf("%w: resource type %s is missing billing dimension %q", ErrDataQuality, resourceType, key)
		}
		if key == "attached" {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%w: resource type %s has invalid billing dimension %q", ErrDataQuality, resourceType, key)
			}
			continue
		}
		valueString, ok := value.(string)
		if !ok || (valueString == "" && key != "project_id") {
			return fmt.Errorf("%w: resource type %s has invalid billing dimension %q", ErrDataQuality, resourceType, key)
		}
	}
	if resourceType == schema.ResourceTypeExternalIP && dimensions["attached"] == true {
		for _, key := range []string{"attribution_type", "attribution_id"} {
			value, ok := dimensions[key].(string)
			if !ok || value == "" {
				return fmt.Errorf("%w: attached ExternalIP is missing billing dimension %q", ErrDataQuality, key)
			}
		}
	}
	return nil
}

func NetworkingTransitionTime(deletionTime, stateTime *timestamppb.Timestamp, resourceID string) (time.Time, error) {
	if deletionTime != nil {
		return deletionTime.AsTime(), nil
	}
	if stateTime == nil {
		return time.Time{}, fmt.Errorf("networking resource %s has no authoritative state transition time", resourceID)
	}
	return stateTime.AsTime(), nil
}

func attributionDimensions(attribution *privatev1.ExternalIPAttribution) (map[string]any, error) {
	if attribution == nil {
		return nil, fmt.Errorf("%w: attached ExternalIP has no settled attribution", ErrDataQuality)
	}

	dimensions := map[string]any{}
	switch target := attribution.GetTarget().(type) {
	case *privatev1.ExternalIPAttribution_ComputeInstance:
		dimensions["attribution_type"] = schema.ResourceTypeComputeInstance
		dimensions["attribution_id"] = target.ComputeInstance.GetId()
	case *privatev1.ExternalIPAttribution_Cluster:
		dimensions["attribution_type"] = schema.ResourceTypeClusterOrder
		dimensions["attribution_id"] = target.Cluster.GetId()
		endpoints := map[privatev1.ExternalIPAttachmentEndpoint]string{
			privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API:     "api",
			privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS: "ingress",
		}
		endpoint, ok := endpoints[attribution.GetEndpoint()]
		if !ok {
			return nil, fmt.Errorf("%w: cluster ExternalIP attribution has no valid endpoint", ErrDataQuality)
		}
		dimensions["attribution_endpoint"] = endpoint
	case *privatev1.ExternalIPAttribution_BaremetalInstance:
		dimensions["attribution_type"] = "baremetal_instance"
		dimensions["attribution_id"] = target.BaremetalInstance.GetId()
	default:
		return nil, fmt.Errorf("%w: attached ExternalIP has an unsupported settled attribution", ErrDataQuality)
	}
	if dimensions["attribution_id"] == "" {
		return nil, fmt.Errorf("%w: attached ExternalIP has an empty settled attribution id", ErrDataQuality)
	}
	return dimensions, nil
}
