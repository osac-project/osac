// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/tests/integration/caas/suites/fixtures"
)

// createCluster validates requested capacity and retains the submitted identity for cleanup.
func (s *clusterFixture) createCluster(ctx context.Context) error {
	GinkgoHelper()
	spec, err := fixtures.ClusterSpec(s.group, s.requestedWorkers)
	if err != nil {
		return err
	}

	s.createAttempted = true
	created, err := s.clusters.Create(ctx, publicv1.ClustersCreateRequest_builder{Object: publicv1.Cluster_builder{
		Metadata: publicv1.Metadata_builder{Name: s.runID, Tenant: tenantID}.Build(),
		Spec:     spec,
	}.Build()}.Build())
	s.clusterID = created.GetObject().GetId()

	if err != nil {
		return err
	}

	if s.clusterID == "" {
		return fmt.Errorf("submitted cluster response has no identity")
	}

	slog.InfoContext(ctx, "cluster request accepted", "run", s.runID, "cluster", s.clusterID, "tenant", tenantID)
	AddReportEntry("Cluster identity", map[string]string{"run": s.runID, "id": s.clusterID})

	return nil
}

// recoverClusterID finds one original request after its Create acknowledgement is lost.
// Absence or ambiguous matches leave cleanup unresolved and retain response settings.
func (s *clusterFixture) recoverClusterID(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	response, err := s.clusters.List(ctx, publicv1.ClustersListRequest_builder{
		Filter: new(fmt.Sprintf(`this.metadata.tenant == %q && this.metadata.name == %q`,
			tenantID, s.runID)),
		Limit: new(int32(2)),
	}.Build())
	if err != nil {
		return fmt.Errorf("recover submitted cluster: %w", err)
	}

	if response.GetTotal() != 1 || len(response.GetItems()) != 1 || response.GetItems()[0].GetId() == "" {
		return fmt.Errorf("submitted cluster identity remains unresolved")
	}

	s.clusterID = response.GetItems()[0].GetId()

	return nil
}

// workerBMIs lists the complete set of this order's Fulfillment allocations.
func (s *clusterFixture) workerBMIs(ctx context.Context, g Gomega) []*publicv1.BareMetalInstance {
	GinkgoHelper()
	response, err := publicv1.NewBareMetalInstancesClient(connection).
		List(ctx, publicv1.BareMetalInstancesListRequest_builder{
			Filter: new(
				fmt.Sprintf(`this.metadata.labels["osac.openshift.io/cluster-order"] == %q`, s.order.Name),
			),
			Limit: new(int32(100)),
		}.Build())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(response.GetTotal()).To(Equal(int32(len(response.GetItems()))))

	return response.GetItems()
}

// requestDelete asks Fulfillment to delete this test's cluster and accepts an
// already absent cluster. Later checks wait for its dependent resources to vanish.
func (s *clusterFixture) requestDelete(ctx context.Context) {
	GinkgoHelper()
	slog.InfoContext(ctx, "requesting cluster deletion", "run", s.runID, "cluster", s.clusterID)

	_, err := s.clusters.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: s.clusterID}.Build())
	if status.Code(err) != codes.NotFound {
		Expect(err).NotTo(HaveOccurred())
	}
}
