package driver

import (
	"context"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
)

func TestGetPluginCapabilitiesIncludesVolumeAccessibilityConstraints(t *testing.T) {
	server := NewIdentityServer("csi.osac.openshift.io", "test")

	resp, err := server.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetPluginCapabilities returned error: %v", err)
	}

	var haveControllerService, haveVolumeAccessibility bool
	for _, capability := range resp.GetCapabilities() {
		if service := capability.GetService(); service != nil {
			switch service.GetType() {
			case csi.PluginCapability_Service_CONTROLLER_SERVICE:
				haveControllerService = true
			case csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS:
				haveVolumeAccessibility = true
			}
		}
	}

	if !haveControllerService {
		t.Error("expected CONTROLLER_SERVICE capability")
	}
	if !haveVolumeAccessibility {
		t.Error("expected VOLUME_ACCESSIBILITY_CONSTRAINTS capability")
	}
}
