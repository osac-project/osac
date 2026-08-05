/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package networkmanager

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// LabelFabricManager is the label key that identifies a ConfigMap as a fabric manager registration.
	LabelFabricManager = "osac.openshift.io/network/fabric-manager"

	// LabelK8sManager is the label key that identifies a ConfigMap as a k8s manager registration.
	LabelK8sManager = "osac.openshift.io/network/k8s-manager"

	// DataKeyName is the ConfigMap data key for the manager's unique identifier.
	DataKeyName = "name"

	// DataKeyDescription is the ConfigMap data key for a human-readable description.
	DataKeyDescription = "description"

	// DataKeyCapabilities is the ConfigMap data key for a comma-separated list of supported capabilities.
	DataKeyCapabilities = "capabilities"
)

// ManagerType distinguishes fabric managers from k8s managers.
type ManagerType string

const (
	// FabricManager handles physical networking: VLAN/VxLAN segments, ACLs, IP allocation, NAT.
	FabricManager ManagerType = "fabric"

	// K8sManager bridges the Kubernetes OVN overlay to the physical fabric.
	K8sManager ManagerType = "k8s"
)

// Capability represents a networking capability that a manager can support.
type Capability string

const (
	CapabilityIPv4       Capability = "ipv4"
	CapabilityIPv6       Capability = "ipv6"
	CapabilityDualStack  Capability = "dualStack"
	CapabilityDPUSupport Capability = "dpuSupport"
)

// validCapabilities is the fixed set of capability values the system recognizes.
var validCapabilities = map[Capability]struct{}{
	CapabilityIPv4:       {},
	CapabilityIPv6:       {},
	CapabilityDualStack:  {},
	CapabilityDPUSupport: {},
}

// Manager is the parsed representation of a network manager registration ConfigMap.
type Manager struct {
	// Name is the unique identifier for this manager (from data.name).
	Name string

	// Description is a human-readable summary of the manager.
	Description string

	// Capabilities lists the networking capabilities this manager supports.
	Capabilities []Capability

	// Type indicates whether this is a fabric or k8s manager.
	Type ManagerType

	// ConfigMapRef is the namespace/name of the source ConfigMap for debugging.
	ConfigMapRef types.NamespacedName
}

// HasCapability reports whether the manager supports the given capability.
func (m *Manager) HasCapability(capability Capability) bool {
	for _, c := range m.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// ParseConfigMap validates a ConfigMap and returns a typed Manager.
// The managerType parameter indicates whether this is being parsed as a fabric or k8s manager.
func ParseConfigMap(cm *corev1.ConfigMap, managerType ManagerType) (*Manager, error) {
	if cm == nil {
		return nil, fmt.Errorf("ConfigMap is nil")
	}

	name, ok := cm.Data[DataKeyName]
	if !ok || strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("ConfigMap %s/%s: missing or empty required field data.%s",
			cm.Namespace, cm.Name, DataKeyName)
	}

	caps, err := parseCapabilities(cm.Data[DataKeyCapabilities])
	if err != nil {
		return nil, fmt.Errorf("ConfigMap %s/%s: %w", cm.Namespace, cm.Name, err)
	}

	return &Manager{
		Name:         strings.TrimSpace(name),
		Description:  strings.TrimSpace(cm.Data[DataKeyDescription]),
		Capabilities: caps,
		Type:         managerType,
		ConfigMapRef: types.NamespacedName{
			Namespace: cm.Namespace,
			Name:      cm.Name,
		},
	}, nil
}

// parseCapabilities splits and validates a comma-separated capabilities string.
func parseCapabilities(raw string) ([]Capability, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("capabilities field must not be empty")
	}

	parts := strings.Split(raw, ",")
	caps := make([]Capability, 0, len(parts))
	seen := make(map[Capability]bool, len(parts))

	for _, p := range parts {
		c := Capability(strings.TrimSpace(p))
		if c == "" {
			continue
		}
		if _, valid := validCapabilities[c]; !valid {
			return nil, fmt.Errorf("invalid capability %q: must be one of %s",
				c, validCapabilityNames())
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		caps = append(caps, c)
	}

	if len(caps) == 0 {
		return nil, fmt.Errorf("capabilities field must not be empty")
	}

	if seen[CapabilityDualStack] {
		for _, implied := range []Capability{CapabilityIPv4, CapabilityIPv6} {
			if !seen[implied] {
				seen[implied] = true
				caps = append(caps, implied)
			}
		}
	}

	return caps, nil
}

// validCapabilityNames returns a sorted, human-readable list of valid capabilities.
func validCapabilityNames() string {
	names := make([]string, 0, len(validCapabilities))
	for c := range validCapabilities {
		names = append(names, string(c))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
