package fulfillment

import "context"

// ControlPlaneClient manages volume publish/unpublish operations through the
// OSAC control plane. Both methods block until the operation completes.
type ControlPlaneClient interface {
	PublishVolume(ctx context.Context, volumeID, nodeID string) error
	UnpublishVolume(ctx context.Context, volumeID, nodeID string) error
}
