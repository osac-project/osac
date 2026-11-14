// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/awx"
	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

// CheckCleanEnvironment composes application, native, inventory and execution reuse observations.
func CheckCleanEnvironment(
	ctx context.Context,
	environment *harness.Environment,
	inventory harness.Inventory,
	executions *awx.Installation,
	connection grpc.ClientConnInterface,
	tenantID string,
) error {
	if err := checkApplicationInputsGone(ctx, environment, connection, tenantID); err != nil {
		return err
	}

	if err := harness.CheckNativeResourcesGone(ctx, environment); err != nil {
		return err
	}

	if err := CheckInventoryReleased(ctx, environment, inventory); err != nil {
		return err
	}

	return executions.CheckIdle(ctx, environment.Namespace,
		"osac-aap/playbook_osac_create_hosted_cluster.yml", "osac-aap/playbook_osac_delete_hosted_cluster.yml",
		"osac-aap/playbook_osac_create_bare_metal_instance.yml", "osac-aap/playbook_osac_delete_bare_metal_instance.yml",
		"osac-aap/playbook_osac_network_task.yml")
}

// checkApplicationInputsGone checks requests and working namespaces owned by OSAC.
func checkApplicationInputsGone(
	ctx context.Context, environment *harness.Environment, connection grpc.ClientConnInterface, tenantID string,
) error {
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	filter := new(fmt.Sprintf(`this.metadata.tenant == %q`, tenantID))

	clusters, err := publicv1.NewClustersClient(connection).List(requestCtx, publicv1.ClustersListRequest_builder{
		Filter: filter, Limit: new(int32(1)),
	}.Build())
	if err != nil {
		return fmt.Errorf("list Fulfillment clusters: %w", err)
	}

	if len(clusters.GetItems()) > 0 {
		return fmt.Errorf("fulfillment clusters remain in tenant %s", tenantID)
	}

	allocations, err := publicv1.NewBareMetalInstancesClient(connection).
		List(requestCtx, publicv1.BareMetalInstancesListRequest_builder{
			Filter: filter, Limit: new(int32(1)),
		}.Build())
	if err != nil {
		return fmt.Errorf("list Fulfillment bare metal allocations: %w", err)
	}

	if len(allocations.GetItems()) > 0 {
		return fmt.Errorf("fulfillment bare metal allocations remain in tenant %s", tenantID)
	}

	var orders osacv1.ClusterOrderList

	if err := environment.Client.List(ctx, &orders, client.InNamespace(environment.Namespace)); err != nil {
		return err
	}

	if len(orders.Items) != 0 {
		return fmt.Errorf("%d clusterorders remain", len(orders.Items))
	}

	var instances bmfv1.BareMetalInstanceList

	if err := environment.Client.List(ctx, &instances, client.InNamespace(environment.Namespace)); err != nil {
		return err
	}

	if len(instances.Items) != 0 {
		return fmt.Errorf("%d baremetalinstances remain", len(instances.Items))
	}

	working, err := environment.Kube.CoreV1().
		Namespaces().
		List(ctx, metav1.ListOptions{LabelSelector: "osac.openshift.io/clusterorder,app.kubernetes.io/name=osac-operator"})
	if err != nil {
		return err
	}

	if len(working.Items) != 0 {
		slog.DebugContext(ctx, "OSAC working namespaces remain", "count", len(working.Items))
		return fmt.Errorf("OSAC working namespaces remain")
	}

	return nil
}
