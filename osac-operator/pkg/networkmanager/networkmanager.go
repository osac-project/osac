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
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// LabelFabricManager is the label key that identifies a ConfigMap as a Fabric Manager registration.
	LabelFabricManager = "osac.openshift.io/network-fabric-manager"
	// LabelK8sManager is the label key that identifies a ConfigMap as a Kubernetes Manager registration.
	LabelK8sManager = "osac.openshift.io/network-k8s-manager"
	// LabelValue is the value of a registration label.
	LabelValue = "true"

	// DataKeyName is the registration name a NetworkClass selects.
	DataKeyName = "name"
	// DataKeyAnsibleRole is the fully qualified Ansible role of the manager.
	DataKeyAnsibleRole = "ansible_role"
	// DataKeyDescription is optional free text.
	DataKeyDescription = "description"
	// DataKeyCapabilities is the comma-separated capability list.
	DataKeyCapabilities = "capabilities"
	// DataKeyNetworkTypes is the comma-separated network type list (Fabric only).
	DataKeyNetworkTypes = "network_types"
	// DataKeyNetworkType is the network type of a Kubernetes Manager.
	DataKeyNetworkType = "network_type"

	// CapabilityEastWestEthernet means the Fabric Manager implements the FabricDomain tasks.
	CapabilityEastWestEthernet = "eastWestEthernet"
	// CapabilitySecurityGroupICMP means the manager's create_security_group accepts icmp rules.
	CapabilitySecurityGroupICMP = "security_group_icmp"
	// NetworkTypeEVPN is the only network type of this release.
	NetworkTypeEVPN = "evpn"
)

// ManagerType is the manager type; its value is osac_job_vars.manager.type.
type ManagerType string

const (
	// FabricManager configures the provider fabric.
	FabricManager ManagerType = "fabric"
	// KubernetesManager configures hub networking (OSAC's own managers).
	KubernetesManager ManagerType = "kubernetes"
)

// Label returns the registration label key of the manager type.
func (t ManagerType) Label() string {
	if t == KubernetesManager {
		return LabelK8sManager
	}
	return LabelFabricManager
}

var (
	namePattern        = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	ansibleRolePattern = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+\.[a-z0-9_]+$`)

	validCapabilities = map[ManagerType]map[string]bool{
		FabricManager:     {CapabilityEastWestEthernet: true, CapabilitySecurityGroupICMP: true},
		KubernetesManager: {CapabilitySecurityGroupICMP: true},
	}
	validNetworkTypes = map[string]bool{NetworkTypeEVPN: true}

	allowedKeys = map[ManagerType]map[string]bool{
		FabricManager: {
			DataKeyName: true, DataKeyAnsibleRole: true, DataKeyDescription: true,
			DataKeyCapabilities: true, DataKeyNetworkTypes: true,
		},
		KubernetesManager: {
			DataKeyName: true, DataKeyAnsibleRole: true, DataKeyDescription: true,
			DataKeyCapabilities: true, DataKeyNetworkType: true,
		},
	}
)

// Manager is a parsed and validated manager registration (contract §4.2).
type Manager struct {
	Name         string
	AnsibleRole  string
	Description  string
	Capabilities []string
	NetworkTypes []string
	NetworkType  string
	Type         ManagerType
	ConfigMapRef types.NamespacedName
}

// HasCapability reports whether the manager declares the capability.
func (m *Manager) HasCapability(capability string) bool {
	for _, c := range m.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// HasNetworkType reports whether a Fabric Manager declares the network type.
func (m *Manager) HasNetworkType(networkType string) bool {
	for _, t := range m.NetworkTypes {
		if t == networkType {
			return true
		}
	}
	return false
}

// ParseConfigMap validates a registration ConfigMap as a manager of the given
// type. It does not check the labels; Find does.
func ParseConfigMap(cm *corev1.ConfigMap, managerType ManagerType) (*Manager, error) {
	ref := fmt.Sprintf("ConfigMap %s/%s", cm.Namespace, cm.Name)
	if cm.Immutable == nil || !*cm.Immutable {
		return nil, fmt.Errorf("%s must set immutable: true", ref)
	}
	keys := make([]string, 0, len(cm.Data))
	for key := range cm.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !allowedKeys[managerType][key] {
			return nil, fmt.Errorf("%s: unknown data key %q for a %s manager registration", ref, key, managerType)
		}
	}
	name := cm.Data[DataKeyName]
	if !namePattern.MatchString(name) {
		return nil, fmt.Errorf("%s: name %q must start with a lowercase letter and use only lowercase letters, digits, and underscores", ref, name)
	}
	role := cm.Data[DataKeyAnsibleRole]
	if !ansibleRolePattern.MatchString(role) {
		return nil, fmt.Errorf("%s: ansible_role %q is not a fully qualified role name <namespace>.<collection>.<role>", ref, role)
	}
	m := &Manager{
		Name:         name,
		AnsibleRole:  role,
		Description:  cm.Data[DataKeyDescription],
		Type:         managerType,
		ConfigMapRef: types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name},
	}
	var err error
	if m.Capabilities, err = parseList(ref, DataKeyCapabilities, cm.Data[DataKeyCapabilities], validCapabilities[managerType], "capability"); err != nil {
		return nil, err
	}
	if m.NetworkTypes, err = parseList(ref, DataKeyNetworkTypes, cm.Data[DataKeyNetworkTypes], validNetworkTypes, "network type"); err != nil {
		return nil, err
	}
	if managerType == KubernetesManager {
		m.NetworkType = cm.Data[DataKeyNetworkType]
		if m.NetworkType == "" {
			return nil, fmt.Errorf("Kubernetes Manager %q has no network_type", name) //nolint:staticcheck // user-facing NetworkClass message
		}
		if !validNetworkTypes[m.NetworkType] {
			return nil, fmt.Errorf("%s: unknown network type %q", ref, m.NetworkType)
		}
		if !strings.HasPrefix(role, "osac.") {
			return nil, fmt.Errorf("Kubernetes Manager %q: ansible_role %q must be in an osac.* collection", name, role) //nolint:staticcheck // user-facing NetworkClass message
		}
	}
	return m, nil
}

// parseList parses a comma-separated list without spaces. An empty value is absent.
func parseList(ref, key, raw string, valid map[string]bool, what string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		if part == "" || seen[part] {
			return nil, fmt.Errorf("%s: %s has an empty or repeated value %q", ref, key, part)
		}
		seen[part] = true
		if !valid[part] {
			return nil, fmt.Errorf("%s: unknown %s %q", ref, what, part)
		}
	}
	return parts, nil
}

// Discovery finds manager registrations in the operator namespace.
type Discovery struct {
	reader    client.Reader
	namespace string
}

// NewDiscovery creates a Discovery that reads registrations in namespace.
func NewDiscovery(reader client.Reader, namespace string) (*Discovery, error) {
	if namespace == "" {
		return nil, fmt.Errorf("networkmanager: namespace must not be empty")
	}
	return &Discovery{reader: reader, namespace: namespace}, nil
}

// Find returns the registration of the given type whose data.name is name.
// Only the matching registration is validated, so a bad unrelated
// registration never fails the lookup.
func (d *Discovery) Find(ctx context.Context, name string, managerType ManagerType) (*Manager, error) {
	list := &corev1.ConfigMapList{}
	if err := d.reader.List(ctx, list,
		client.InNamespace(d.namespace),
		client.MatchingLabels{managerType.Label(): LabelValue},
	); err != nil {
		return nil, fmt.Errorf("listing %s manager registrations: %w", managerType, err)
	}
	var matches []*corev1.ConfigMap
	for i := range list.Items {
		if list.Items[i].Data[DataKeyName] == name {
			matches = append(matches, &list.Items[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%s manager %q matches no ConfigMap labelled %s", managerType, name, managerType.Label())
	case 1:
	default:
		names := make([]string, 0, len(matches))
		for _, cm := range matches {
			names = append(names, cm.Name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("%s manager %q matches more than one ConfigMap: %s", managerType, name, strings.Join(names, ", "))
	}
	cm := matches[0]
	if cm.Labels[LabelFabricManager] == LabelValue && cm.Labels[LabelK8sManager] == LabelValue {
		return nil, fmt.Errorf("ConfigMap %s/%s carries both manager-type labels", cm.Namespace, cm.Name)
	}
	return ParseConfigMap(cm, managerType)
}

// Resolution is the result of resolving a NetworkClass.
type Resolution struct {
	Fabric      *Manager
	Kubernetes  *Manager // nil without a Kubernetes Manager
	NetworkType string   // empty without a Kubernetes Manager
}

// SecurityGroupICMP reports whether every resolved manager declares security_group_icmp.
func (r *Resolution) SecurityGroupICMP() bool {
	if !r.Fabric.HasCapability(CapabilitySecurityGroupICMP) {
		return false
	}
	return r.Kubernetes == nil || r.Kubernetes.HasCapability(CapabilitySecurityGroupICMP)
}

// Resolve resolves the managers a NetworkClass selects (contract §4.2).
// k8sName may be empty. eastWestEthernet is the NetworkClass
// supports_east_west_ethernet setting.
func (d *Discovery) Resolve(ctx context.Context, fabricName, k8sName string, eastWestEthernet bool) (*Resolution, error) {
	fabric, err := d.Find(ctx, fabricName, FabricManager)
	if err != nil {
		return nil, err
	}
	res := &Resolution{Fabric: fabric}
	if k8sName != "" {
		k8s, err := d.Find(ctx, k8sName, KubernetesManager)
		if err != nil {
			return nil, err
		}
		if !fabric.HasNetworkType(k8s.NetworkType) {
			return nil, fmt.Errorf("Kubernetes Manager %q network type %q is not in the network_types of Fabric Manager %q", //nolint:staticcheck // user-facing NetworkClass message
				k8s.Name, k8s.NetworkType, fabric.Name)
		}
		res.Kubernetes = k8s
		res.NetworkType = k8s.NetworkType
	}
	if eastWestEthernet && !fabric.HasCapability(CapabilityEastWestEthernet) {
		return nil, fmt.Errorf("east-west Ethernet is enabled but Fabric Manager %q does not declare eastWestEthernet", fabric.Name)
	}
	return res, nil
}
