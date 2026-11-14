// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"context"
	"fmt"
	"net"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	physical "github.com/osac-project/osac/tests/integration/caas/internal/simulator/host"
)

// SlotLabel partitions fixture inventory using the product’s host selector.
const SlotLabel = "caas-test.osac-project.io/slot"

// Inventory records original host and hardware identities for fixture ownership.
type Inventory struct {
	Namespace string
	Hosts     []Host
}

// Host associates an original physical machine with an inventory group.
type Host struct {
	physical.Machine
	Slot string
}

// InventoryNamespace requires the installed simulator to use a separate inventory namespace.
func InventoryNamespace(ctx context.Context, environment *Environment) (string, error) {
	deployment, err := environment.Kube.AppsV1().Deployments(environment.Namespace).
		Get(ctx, simulatorDeployment, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, variable := range container.Env {
			if variable.Name == "METAL3_NAMESPACE" && variable.Value != "" {
				if variable.Value == environment.Namespace {
					return "", fmt.Errorf("hardware inventory requires a separate namespace")
				}

				return variable.Value, nil
			}
		}
	}

	return "", fmt.Errorf("simulator inventory namespace is missing")
}

// CheckInventoryEmpty rejects leftover hardware in the installed inventory namespace.
// It leaves existing objects untouched, including unlabelled hosts and orphaned HardwareData.
func CheckInventoryEmpty(ctx context.Context, environment *Environment) error {
	namespace, err := InventoryNamespace(ctx, environment)
	if err != nil {
		return err
	}

	var hosts metal3v1.BareMetalHostList

	if err := environment.Client.List(ctx, &hosts, client.InNamespace(namespace)); err != nil {
		return err
	}

	if len(hosts.Items) != 0 {
		return fmt.Errorf("inventory namespace %s contains %d BareMetalHosts", namespace, len(hosts.Items))
	}

	var hardware metal3v1.HardwareDataList

	if err := environment.Client.List(ctx, &hardware, client.InNamespace(namespace)); err != nil {
		return err
	}

	if len(hardware.Items) != 0 {
		return fmt.Errorf("inventory namespace %s contains %d HardwareData objects", namespace, len(hardware.Items))
	}

	return nil
}

// CreateInventory creates one suite-owned batch of hardware, independent of test processes.
// Partial results retain every created identity for setup-failure diagnostics and cleanup.
func CreateInventory(
	ctx context.Context, environment *Environment, namespace string, groups []string, hostsPerGroup int,
) (Inventory, error) {
	inventory := Inventory{Namespace: namespace}
	ordinal := uint64(0)

	for _, group := range groups {
		for index := range hostsPerGroup {
			ordinal++
			name := fmt.Sprintf("%s-worker-%02d", group, index+1)

			host := &metal3v1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{
					"app.kubernetes.io/part-of":    "osac-provider-simulator",
					"osac.openshift.io/host-type":  "simulated-worker",
					"osac.openshift.io/managed-by": "baremetal",
					"caas-it":                      "worker", SlotLabel: group,
				}},
				Spec: metal3v1.BareMetalHostSpec{Online: false},
			}
			if err := environment.Client.Create(ctx, host); err != nil {
				return inventory, err
			}

			inventory.Hosts = append(inventory.Hosts, Host{
				Machine: physical.Machine{Namespace: namespace, Name: name, UID: host.UID}, Slot: group,
			})
			mac := net.HardwareAddr{
				0x02, byte(ordinal >> 32), byte(ordinal >> 24), byte(ordinal >> 16), byte(ordinal >> 8), byte(ordinal),
			}.String()

			hardware := &metal3v1.HardwareData{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{
					"app.kubernetes.io/part-of": "osac-provider-simulator",
				}},
				Spec: metal3v1.HardwareDataSpec{HardwareDetails: &metal3v1.HardwareDetails{
					Storage: []metal3v1.Storage{
						{Name: "/dev/sdb", SizeBytes: 257698037760},
						{Name: "/dev/sda", SizeBytes: 128849018880},
					},
					NIC: []metal3v1.NIC{{Name: "eth0", MAC: mac, Model: "simulated", SpeedGbps: 10, PXE: true}},
				}},
			}
			if err := environment.Client.Create(ctx, hardware); err != nil {
				return inventory, err
			}

			inventory.Hosts[len(inventory.Hosts)-1].HardwareUID = hardware.UID
		}
	}

	return inventory, nil
}

// Delete removes the recorded fixture objects with atomic UID preconditions.
// Complete inventories require natural application cleanup and released hardware.
func (inventory Inventory) Delete(ctx context.Context, environment *Environment) error {
	for _, original := range inventory.Hosts {
		metadata := metav1.ObjectMeta{Name: original.Name, Namespace: original.Namespace}
		if err := environment.Client.Delete(ctx, &metal3v1.BareMetalHost{ObjectMeta: metadata},
			&client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &original.UID}}); client.IgnoreNotFound(err) != nil {
			return err
		}

		if original.HardwareUID == "" {
			continue
		}

		if err := environment.Client.Delete(
			ctx,
			&metal3v1.HardwareData{ObjectMeta: metadata},
			&client.DeleteOptions{
				Preconditions: &metav1.Preconditions{UID: &original.HardwareUID},
			},
		); client.IgnoreNotFound(err) != nil {
			return err
		}
	}

	return nil
}

// ForSlot selects original hosts using an inventory group's allocation selector.
func (inventory Inventory) ForSlot(slot string) Inventory {
	selected := Inventory{Namespace: inventory.Namespace}
	for _, host := range inventory.Hosts {
		if host.Slot == slot {
			selected.Hosts = append(selected.Hosts, host)
		}
	}

	return selected
}

// CheckReleased observes the native release of each original physical machine.
func (inventory Inventory) CheckReleased(ctx context.Context, environment *Environment) error {
	for _, original := range inventory.Hosts {
		if err := original.CheckReleased(ctx, environment.Client); err != nil {
			return err
		}
	}

	return nil
}
