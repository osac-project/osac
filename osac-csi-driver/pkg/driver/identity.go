package driver

import (
	"context"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"k8s.io/klog/v2"
)

// IdentityServer implements the CSI Identity service.
type IdentityServer struct {
	csi.UnimplementedIdentityServer
	name    string
	version string
	// nodeOnly omits CONTROLLER_SERVICE from the plugin capabilities.
	nodeOnly bool
}

// NewIdentityServer creates a new CSI identity server.
func NewIdentityServer(name, version string) *IdentityServer {
	return &IdentityServer{
		name:    name,
		version: version,
	}
}

// GetPluginInfo returns metadata about this CSI plugin.
func (s *IdentityServer) GetPluginInfo(_ context.Context, _ *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	klog.Infof("GetPluginInfo called: name=%s version=%s", s.name, s.version)
	return &csi.GetPluginInfoResponse{
		Name:          s.name,
		VendorVersion: s.version,
		Manifest: map[string]string{
			"name":    s.name,
			"version": s.version,
		},
	}, nil
}

// GetPluginCapabilities returns the capabilities of this CSI plugin.
func (s *IdentityServer) GetPluginCapabilities(_ context.Context, _ *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	klog.Infof("GetPluginCapabilities called")
	var capabilities []*csi.PluginCapability
	if !s.nodeOnly {
		capabilities = append(capabilities, serviceCapability(csi.PluginCapability_Service_CONTROLLER_SERVICE))
	}
	capabilities = append(capabilities,
		serviceCapability(csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS))
	return &csi.GetPluginCapabilitiesResponse{Capabilities: capabilities}, nil
}

func serviceCapability(t csi.PluginCapability_Service_Type) *csi.PluginCapability {
	return &csi.PluginCapability{
		Type: &csi.PluginCapability_Service_{
			Service: &csi.PluginCapability_Service{Type: t},
		},
	}
}

// Probe checks whether the plugin is ready to serve requests.
func (s *IdentityServer) Probe(_ context.Context, _ *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	klog.Infof("Probe called")
	return &csi.ProbeResponse{
		Ready: wrapperspb.Bool(true),
	}, nil
}
