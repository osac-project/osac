// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package host models boot prerequisites and Assisted registration for omitted hardware.
// Suite fixtures invoke this model after provisioning; it is not a deployed controller.
// BMO reports provisioning; this model supplies inventory, never approval or claims.
package host

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"

	ignition "github.com/coreos/ignition/v2/config/v3_5"
	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// Machine retains the original BareMetalHost and HardwareData identities across allocation.
type Machine struct {
	Namespace   string
	Name        string
	UID         types.UID
	HardwareUID types.UID
}

// Interfaces translates HardwareData NICs into host-reported inventory.
// Documentation addresses satisfy external-access input; DHCP and dataplane are omitted.
func Interfaces(hardware *metal3v1.HardwareData) ([]assistedv1.HostInterface, error) {
	if hardware.Spec.HardwareDetails == nil || len(hardware.Spec.HardwareDetails.NIC) == 0 {
		return nil, fmt.Errorf("hardware inventory contains no NICs")
	}

	interfaces := make([]assistedv1.HostInterface, 0, len(hardware.Spec.HardwareDetails.NIC))
	for _, nic := range hardware.Spec.HardwareDetails.NIC {
		mac, err := net.ParseMAC(nic.MAC)
		if err != nil {
			return nil, fmt.Errorf("invalid hardware MAC")
		}

		interfaces = append(interfaces, assistedv1.HostInterface{
			Name: nic.Name, MacAddress: mac.String(), Flags: []string{"up"}, HasCarrier: true,
			IPV4Addresses: []string{fmt.Sprintf("192.0.2.%d/24", 20+int(mac[len(mac)-1])%200)},
			IPV6Addresses: []string{},
		})
	}

	return interfaces, nil
}

// Boot registers a provisioned and powered host against its original InfraEnv.
// The discovery fixture runs no Agent process, so the caller supplies its InfraEnv.
// Repeated boot recovers partial registration.
func (m Machine) Boot(
	ctx context.Context, c client.Client, original *assistedv1.InfraEnv, annotations map[string]string,
) (*assistedv1.Agent, error) {
	interfaces, err := m.bootInventory(ctx, c)
	if err != nil {
		return nil, err
	}

	infra := &assistedv1.InfraEnv{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(original), infra); err != nil {
		return nil, err
	}

	if infra.UID != original.UID || infra.UID == "" || infra.DeletionTimestamp != nil {
		return nil, fmt.Errorf("discovery environment incarnation changed")
	}

	if infra.Status.BootArtifacts.DiscoveryIgnitionURL == "" {
		return nil, fmt.Errorf("discovery artifact is pending")
	}

	return m.registerAgent(ctx, c, infra, interfaces, annotations)
}

// registerAgent creates an unapproved registration or recovers its original owner,
// metadata and hardware. Inventory is submitted against the observed UID and version.
func (m Machine) registerAgent(
	ctx context.Context, c client.Client, infra *assistedv1.InfraEnv,
	interfaces []assistedv1.HostInterface, annotations map[string]string,
) (*assistedv1.Agent, error) {
	agent := &assistedv1.Agent{}
	err := c.Get(ctx, client.ObjectKey{Namespace: infra.Namespace, Name: "host-" + m.Name}, agent)
	if apierrors.IsNotFound(err) {
		agent = &assistedv1.Agent{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: infra.Namespace, Name: "host-" + m.Name,
				Labels: map[string]string{kube.InfraEnvLabel: infra.Name}, Annotations: annotations,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: assistedv1.GroupVersion.String(), Kind: "InfraEnv", Name: infra.Name, UID: infra.UID,
				}},
			},
			Spec: assistedv1.AgentSpec{Approved: false, Role: "worker"},
		}
		err = c.Create(ctx, agent)
	}

	if err != nil {
		return nil, err
	}

	if agent.DeletionTimestamp != nil || !kube.MatchesOwner(agent, infra, assistedv1.GroupVersion.WithKind("InfraEnv")) ||
		agent.Labels[kube.InfraEnvLabel] != infra.Name {
		return nil, fmt.Errorf("agent registration belongs to another discovery environment")
	}

	for key, value := range annotations {
		if agent.Annotations[key] != value {
			return nil, fmt.Errorf("agent registration metadata differs")
		}
	}

	if agent.Status.Inventory.Interfaces != nil {
		if !equality.Semantic.DeepEqual(agent.Status.Inventory.Interfaces, interfaces) {
			return nil, fmt.Errorf("agent inventory differs from original hardware")
		}

		return agent, nil
	}

	next := agent.DeepCopy()
	next.Status.Inventory.Interfaces = interfaces
	// Include the observed UID as well as the library's resource-version check.
	base := agent.DeepCopy()
	base.UID = ""
	if err := c.Status().Patch(ctx, next,
		client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return agent, err
	}

	slog.DebugContext(ctx, "registered provisioned host inventory",
		"hostNamespace", m.Namespace, "host", m.Name, "hostUID", m.UID,
		"agent", next.Name, "agentUID", next.UID, "infraEnvUID", infra.UID,
	)

	return next, nil
}

// CheckReleased checks original inventory identities before evaluating availability,
// observed power and cleared provisioning inputs.
func (m Machine) CheckReleased(ctx context.Context, c client.Client) error {
	host := &metal3v1.BareMetalHost{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Name}, host); err != nil {
		return err
	}

	if m.UID == "" || host.UID != m.UID {
		return fmt.Errorf("inventory host %s was replaced", m.Name)
	}

	hardware := &metal3v1.HardwareData{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Name}, hardware); err != nil {
		return err
	}

	if m.HardwareUID == "" || hardware.UID != m.HardwareUID || hardware.DeletionTimestamp != nil {
		return fmt.Errorf("hardware inventory %s was replaced", m.Name)
	}

	return m.checkReleasedState(ctx, host)
}

// checkReleasedState checks availability, power and native inputs on the original host.
func (m Machine) checkReleasedState(ctx context.Context, host *metal3v1.BareMetalHost) error {
	slog.DebugContext(ctx, "observed persistent host release",
		"namespace", m.Namespace, "host", m.Name, "uid", host.UID,
		"state", host.Status.Provisioning.State, "operational", host.Status.OperationalStatus,
		"online", host.Spec.Online, "powered", host.Status.PoweredOn, "deleting", host.DeletionTimestamp != nil,
	)

	if host.DeletionTimestamp != nil || host.Status.Provisioning.State != metal3v1.StateAvailable ||
		host.Status.OperationalStatus != metal3v1.OperationalStatusOK || host.Status.PoweredOn != host.Spec.Online {
		return fmt.Errorf("host %s is not available with power matching spec.online", m.Name)
	}

	for _, input := range []struct {
		name    string
		present bool
	}{
		{"spec.consumerRef", host.Spec.ConsumerRef != nil},
		{"spec.image", host.Spec.Image != nil},
		{"spec.userData", host.Spec.UserData != nil},
		{"spec.metaData", host.Spec.MetaData != nil},
		{"spec.networkData", host.Spec.NetworkData != nil},
		{"status.provisioning.image", host.Status.Provisioning.Image != (metal3v1.Image{})},
	} {
		if input.present {
			return fmt.Errorf("host %s retains %s", m.Name, input.name)
		}
	}

	if _, paused := host.Annotations[metal3v1.PausedAnnotation]; paused {
		return fmt.Errorf("host %s is paused", m.Name)
	}

	for key := range host.Annotations {
		if key == metal3v1.RebootAnnotationPrefix || strings.HasPrefix(key, metal3v1.RebootAnnotationPrefix+"/") {
			return fmt.Errorf("host %s retains a reboot request", m.Name)
		}
	}

	return nil
}

// bootInventory requires provisioning, power, valid delivered user-data and the
// original hardware record. Callers poll until these prerequisites are satisfied.
func (m Machine) bootInventory(ctx context.Context, c client.Reader) ([]assistedv1.HostInterface, error) {
	host := &metal3v1.BareMetalHost{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Name}, host); err != nil {
		return nil, err
	}

	if m.UID == "" || host.UID != m.UID || host.DeletionTimestamp != nil {
		return nil, fmt.Errorf("physical host incarnation changed")
	}

	if host.Status.Provisioning.State != metal3v1.StateProvisioned || !host.Spec.Online || !host.Status.PoweredOn {
		return nil, fmt.Errorf("physical host is awaiting provisioning and power")
	}

	ref := host.Spec.UserData
	if ref == nil || ref.Name == "" {
		return nil, fmt.Errorf("discovery user-data is pending")
	}

	namespace := ref.Namespace
	if namespace == "" {
		namespace = host.Namespace
	}

	if namespace != host.Namespace {
		return nil, fmt.Errorf("user-data Secret must be in the host namespace")
	}

	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, secret); err != nil {
		return nil, err
	}

	if _, report, err := ignition.ParseCompatibleVersion(secret.Data["userData"]); err != nil || report.IsFatal() {
		return nil, fmt.Errorf("delivered discovery user-data is invalid")
	}

	hardware := &metal3v1.HardwareData{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Name}, hardware); err != nil {
		return nil, err
	}

	if m.HardwareUID == "" || hardware.UID != m.HardwareUID || hardware.DeletionTimestamp != nil {
		return nil, fmt.Errorf("hardware inventory incarnation changed")
	}

	return Interfaces(hardware)
}
