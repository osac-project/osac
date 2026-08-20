// Package fulfillment provides the client interfaces for the OSAC fulfillment
// service. The CSI driver uses VolumeClient for volume lifecycle; publish and
// unpublish are proxied to vendor CSI controllers by the driver's controller
// server (see pkg/driver), not routed through this package.
package fulfillment
