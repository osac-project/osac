package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// The binding file is an administrator-owned ConfigMap mount. It contains only
// OSAC resource IDs, AWX credential IDs, and NICo Operating System IDs; NICo
// credentials remain in AWX. The selected AWX credential independently checks
// the tenant and subnet IDs.
//
// Version 2 adds the site-scoped Operating System catalog, which maps each
// approved OSAC DiskImage to the NICo Operating System that installs it.
// Creation requires version 2 and rejects a DiskImage with no catalog entry
// before any NICo call. Deletion only needs the tenant binding, so it accepts
// both versions.
type nicoBindingsFile struct {
	Version          int                        `json:"version"`
	Bindings         []nicoTenantBinding        `json:"bindings"`
	OperatingSystems []nicoOperatingSystemEntry `json:"operatingSystems,omitempty"`
}

type nicoTenantBinding struct {
	OSACTenantID  string `json:"osacTenantID"`
	OSACSubnetID  string `json:"osacSubnetID"`
	CredentialIDs []int  `json:"credentialIDs"`
}

type nicoOperatingSystemEntry struct {
	DiskImageID           string `json:"diskImageID"`
	NicoOperatingSystemID string `json:"nicoOperatingSystemID"`
}

// diskImageIDAnnotation carries the BMI DiskImage ID, set by the fulfillment
// service. It is an annotation so that it stays out of the provisioning
// configuration hash.
const diskImageIDAnnotation = "osac.openshift.io/disk-image-id"

// errNicoDiskImageUnsupported marks a BMI whose DiskImage has no approved NICo
// Operating System. It is terminal for the create attempt: no NICo call is made.
var errNicoDiskImageUnsupported = errors.New("DiskImage has no approved NICo Operating System")

func isCleanID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

func (r *BareMetalInstanceReconciler) loadNicoBindings() (*nicoBindingsFile, error) {
	if r.NicoBindingsConfigPath == "" {
		return nil, fmt.Errorf("NICo tenant bindings are not configured")
	}
	data, err := os.ReadFile(r.NicoBindingsConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read NICo tenant bindings: %w", err)
	}
	var config nicoBindingsFile
	if err := yaml.UnmarshalStrict(data, &config); err != nil {
		return nil, fmt.Errorf("parse NICo tenant bindings: %w", err)
	}
	if (config.Version != 1 && config.Version != 2) || len(config.Bindings) == 0 {
		return nil, fmt.Errorf("NICo tenant bindings require version 1 or 2 and at least one binding")
	}
	if config.Version == 1 && len(config.OperatingSystems) > 0 {
		return nil, fmt.Errorf("NICo Operating System catalog requires bindings version 2")
	}
	seen := make(map[string]struct{}, len(config.Bindings))
	for i := range config.Bindings {
		binding := &config.Bindings[i]
		if !isCleanID(binding.OSACTenantID) || !isCleanID(binding.OSACSubnetID) || len(binding.CredentialIDs) == 0 {
			return nil, fmt.Errorf("NICo tenant binding %d is incomplete", i)
		}
		key := binding.OSACTenantID + "\x00" + binding.OSACSubnetID
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate NICo tenant binding for one OSAC tenant and Subnet")
		}
		seen[key] = struct{}{}
		credentialIDs := make(map[int]struct{}, len(binding.CredentialIDs))
		for _, id := range binding.CredentialIDs {
			if id <= 0 {
				return nil, fmt.Errorf("NICo tenant binding %d has an invalid AWX credential ID", i)
			}
			if _, exists := credentialIDs[id]; exists {
				return nil, fmt.Errorf("NICo tenant binding %d repeats an AWX credential ID", i)
			}
			credentialIDs[id] = struct{}{}
		}
	}
	diskImages := make(map[string]struct{}, len(config.OperatingSystems))
	for i, entry := range config.OperatingSystems {
		if !isCleanID(entry.DiskImageID) || !isCleanID(entry.NicoOperatingSystemID) {
			return nil, fmt.Errorf("NICo Operating System catalog entry %d is incomplete", i)
		}
		if _, exists := diskImages[entry.DiskImageID]; exists {
			return nil, fmt.Errorf("duplicate NICo Operating System catalog entry for one DiskImage")
		}
		diskImages[entry.DiskImageID] = struct{}{}
	}
	return &config, nil
}

// withNicoCredentials selects the AWX credentials bound to the BMI tenant and
// Subnet. It also returns the validated bindings file, so a reconcile reads it
// once.
func (r *BareMetalInstanceReconciler) withNicoCredentials(ctx context.Context, bmi *v1alpha1.BareMetalInstance) (context.Context, *nicoBindingsFile, error) {
	if len(bmi.Spec.NetworkAttachments) != 1 {
		return nil, nil, fmt.Errorf("NICo tenant binding requires one OSAC Subnet")
	}
	tenantID := bmi.Annotations["osac.openshift.io/tenant"]
	subnetID := bmi.Spec.NetworkAttachments[0].SubnetRef
	if tenantID == "" || subnetID == "" {
		return nil, nil, fmt.Errorf("NICo tenant binding requires a BMI tenant and Subnet ID")
	}
	config, err := r.loadNicoBindings()
	if err != nil {
		return nil, nil, err
	}
	for i := range config.Bindings {
		binding := &config.Bindings[i]
		if binding.OSACTenantID == tenantID && binding.OSACSubnetID == subnetID {
			return provisioning.WithAAPCredentialIDs(ctx, binding.CredentialIDs), config, nil
		}
	}
	return nil, nil, fmt.Errorf("no NICo tenant binding for BMI tenant and Subnet")
}

// nicoOperatingSystemFor returns the NICo Operating System approved for the
// BMI DiskImage. It is only used before a create. A version 1 file has no
// catalog, so it is a configuration error. A BMI without a DiskImage ID, or
// with a DiskImage that has no catalog entry, gets errNicoDiskImageUnsupported;
// there is no default Operating System.
func nicoOperatingSystemFor(config *nicoBindingsFile, bmi *v1alpha1.BareMetalInstance) (string, error) {
	if config.Version != 2 {
		return "", fmt.Errorf("NICo BMI creation requires the Operating System catalog in bindings version 2")
	}
	diskImageID := bmi.Annotations[diskImageIDAnnotation]
	if diskImageID == "" {
		return "", fmt.Errorf("%w: BMI has no DiskImage ID", errNicoDiskImageUnsupported)
	}
	for _, entry := range config.OperatingSystems {
		if entry.DiskImageID == diskImageID {
			return entry.NicoOperatingSystemID, nil
		}
	}
	return "", fmt.Errorf("%w: DiskImage %s", errNicoDiskImageUnsupported, diskImageID)
}
