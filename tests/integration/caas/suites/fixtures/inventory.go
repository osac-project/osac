// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"context"
	"fmt"
	"log/slog"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

// InventoryGroup supplies original hardware and a product selector for one active cluster.
// Names identify fixture groups independently of Ginkgo processes.
type InventoryGroup struct {
	Name             string
	Inventory        harness.Inventory
	InstanceTypeName string
}

// CreateInventoryGroups prepares an explicit hardware batch and retains partial creation results.
func CreateInventoryGroups(
	ctx context.Context, environment *harness.Environment, names []string, hostsPerGroup int,
) ([]InventoryGroup, error) {
	namespace, err := harness.InventoryNamespace(ctx, environment)
	if err != nil {
		return nil, err
	}

	inventory, err := harness.CreateInventory(ctx, environment, namespace, names, hostsPerGroup)
	groups := make([]InventoryGroup, 0, len(names))
	for _, name := range names {
		groups = append(groups, InventoryGroup{
			Name: name, Inventory: inventory.ForSlot(name), InstanceTypeName: "caas-it-worker-" + name,
		})
	}

	return groups, err
}

// CheckInventoryReleased requires native release and removal of application allocation inputs.
func CheckInventoryReleased(ctx context.Context, environment *harness.Environment, inventory harness.Inventory) error {
	if err := inventory.CheckReleased(ctx, environment); err != nil {
		return err
	}

	for _, configured := range inventory.Hosts {
		host := &metal3v1.BareMetalHost{}
		if err := environment.Client.Get(
			ctx,
			client.ObjectKey{Namespace: inventory.Namespace, Name: configured.Name},
			host,
		); err != nil {
			return err
		}

		for _, label := range []string{"osac.openshift.io/bare-metal-instance-id", "osac.openshift.io/pool-id"} {
			if _, present := host.GetLabels()[label]; present {
				slog.DebugContext(ctx, "inventory release blocked by allocation label", "host", configured.Name, "label", label)
				return fmt.Errorf("host %s retains allocation %s", configured.Name, label)
			}
		}

		_, generated := host.GetAnnotations()["osac.openshift.io/fallback-root-device"]
		if host.Spec.RootDeviceHints != nil || generated {
			return fmt.Errorf("host %s retains generated root device hints", configured.Name)
		}

		// The provisioning role owns these per-host Secrets and deletes them on release.
		for _, suffix := range []string{"user-data", "meta-data", "network-data"} {
			_, err := environment.Kube.CoreV1().
				Secrets(inventory.Namespace).
				Get(ctx, configured.Name+"-"+suffix, metav1.GetOptions{})
			switch {
			case err == nil:
				slog.DebugContext(ctx, "inventory release blocked by provisioning Secret",
					"host", configured.Name,
					"secretType", suffix,
				)

				return fmt.Errorf("host %s retains its %s Secret", configured.Name, suffix)
			case !apierrors.IsNotFound(err):
				return err
			}
		}
	}

	return nil
}
