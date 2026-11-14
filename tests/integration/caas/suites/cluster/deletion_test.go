// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scenario "github.com/osac-project/osac/tests/integration/caas/internal/simulator/scenario"
	"github.com/osac-project/osac/tests/integration/caas/suites/fixtures"
)

// lostCreateAcknowledgement models lost Create replies and ambiguous recovery pages.
type lostCreateAcknowledgement struct {
	publicv1.ClustersClient
	acceptedID    string
	ambiguousPage bool
}

// Create retains execution evidence while modeling a client-side transport failure after persistence.
func (c *lostCreateAcknowledgement) Create(
	ctx context.Context, request *publicv1.ClustersCreateRequest, options ...grpc.CallOption,
) (*publicv1.ClustersCreateResponse, error) {
	response, err := c.ClustersClient.Create(ctx, request, options...)
	if err != nil {
		return response, err
	}

	c.acceptedID = response.GetObject().GetId()

	return nil, status.Error(codes.Unavailable, "Create acknowledgement lost after server success")
}

// List models a short page whose total still reports multiple matching requests.
func (c *lostCreateAcknowledgement) List(
	ctx context.Context, request *publicv1.ClustersListRequest, options ...grpc.CallOption,
) (*publicv1.ClustersListResponse, error) {
	response, err := c.ClustersClient.List(ctx, request, options...)
	if err == nil && c.ambiguousPage {
		response.SetTotal(2)
	}

	return response, err
}

var _ = Describe("Cluster deletion and consumer detach", Label("caas-it", "lifecycle"), func() {
	var s *clusterFixture

	Context("With stalled installation", Label("negative"), func() {
		BeforeEach(func(ctx SpecContext) {
			s = newClusterFixture(ctx, scenario.InstallationStalled, defaultGroup)
		}, NodeTimeout(5*time.Minute))
		It("deletes while approved workers are still installing", func(ctx SpecContext) {
			By("recovering one submitted request after losing its Create acknowledgement")
			original := connectionClusters
			lost := &lostCreateAcknowledgement{ClustersClient: original}

			func() {
				connectionClusters = lost
				defer func() { connectionClusters = original }()

				Expect(status.Code(s.createCluster(ctx))).To(Equal(codes.Unavailable))
				lost.ambiguousPage = true

				Expect(s.recoverClusterID(ctx)).To(MatchError("submitted cluster identity remains unresolved"))
				Expect(s.clusterID).To(BeEmpty())
			}()
			Expect(s.clusterID).To(BeEmpty())
			Expect(s.createAttempted).To(BeTrue())
			Expect(lost.acceptedID).NotTo(BeEmpty())
			Expect(s.recoverClusterID(ctx)).To(Succeed())
			Expect(s.clusterID).To(Equal(lost.acceptedID))
			AddReportEntry("Cluster identity", map[string]string{"run": s.runID, "id": s.clusterID})

			By("waiting for the original request's workers to be claimed")
			s.awaitProvisioningJob(ctx)
			s.awaitWorkerHandoff(ctx)
			s.awaitProvisioningSuccess(ctx)

			By("observing installation before requesting deletion")
			s.awaitInstallationStalled(ctx)
			s.checkInstallationStalled(ctx, Default)

			By("deleting the cluster while installation remains held")
			s.deleteClusterAndVerify(ctx)
		}, SpecTimeout(30*time.Minute))
	})

	Context("With stalled detach", Label("negative"), func() {
		BeforeEach(func(ctx SpecContext) {
			Skip("OSAC-7739")

			s = newClusterFixture(ctx, scenario.DetachStalled, defaultGroup)
		}, NodeTimeout(5*time.Minute))
		It("waits for worker detach before deletion", func(ctx SpecContext) {
			By("requesting a cluster and waiting for it to become ready")
			Expect(s.createCluster(ctx)).To(Succeed())
			s.awaitProvisioningJob(ctx)
			s.awaitWorkerHandoff(ctx)
			s.awaitProvisioningSuccess(ctx)
			s.awaitReady(ctx)

			By("requesting deletion while the consumer still holds its workers")
			s.requestDelete(ctx)
			s.awaitConsumerDeletion(ctx)

			By("requiring deletion to remain blocked while the consumer holds its workers")
			Consistently(
				func(g Gomega) { s.checkDetachStalled(ctx, g) },
			).WithContext(ctx).
				WithTimeout(8 * time.Second).
				Should(Succeed())

			By("checking retained jobs for premature worker deletion during the hold")

			for _, worker := range s.workers {
				instance := worker.instance
				jobs, err := executions.JobsForResource(
					ctx,
					instance,
					"osac-aap/playbook_osac_delete_bare_metal_instance.yml",
				)
				Expect(err).NotTo(HaveOccurred())
				jobCount := len(jobs)
				Expect(jobCount).To(BeZero(), "provider deletion must wait for consumer detach; job inputs omitted")
			}

			By("allowing worker detach so normal deletion can finish")
			Expect(s.settings.SetProfile(ctx, scenario.Normal)).To(Succeed())
			s.deleteClusterAndVerify(ctx)
		}, SpecTimeout(30*time.Minute))
	})
})

// awaitConsumerDeletion observes owner-driven deletion before testing a held detach.
// The order and NodePool checkpoints share a four-minute budget.
func (s *clusterFixture) awaitConsumerDeletion(ctx context.Context) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	By("observing deletion of the original ClusterOrder")
	Eventually(func(g Gomega) {
		order, err := getClusterOrder(ctx, environment, s.order.Name)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(order.UID).To(Equal(s.order.UID))
		g.Expect(order.DeletionTimestamp).NotTo(BeNil())
	}).WithContext(ctx).Should(Succeed())
	By("observing owner-driven NodePool deletion")
	Eventually(func(g Gomega) {
		pool := &hypershiftv1.NodePool{}

		err := environment.Client.Get(ctx, client.ObjectKeyFromObject(s.nodePool), pool)
		if apierrors.IsNotFound(err) {
			return
		}

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(pool.GetUID()).To(Equal(s.nodePool.GetUID()))
		g.Expect(pool.GetDeletionTimestamp()).NotTo(BeNil())
	}).WithContext(ctx).Should(Succeed())
}

// checkDetachStalled requires the original bindings and BMIs to remain held after deletion.
func (s *clusterFixture) checkDetachStalled(ctx context.Context, g Gomega) {
	GinkgoHelper()
	// Bound all reads in this monitoring sample independently of the spec deadline.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	order, err := getClusterOrder(ctx, environment, s.order.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(order.UID).To(Equal(s.order.UID))
	slog.DebugContext(
		ctx,
		"observed detach hold",
		"order",
		order.Name,
		"uid",
		order.UID,
		"deleting",
		order.DeletionTimestamp != nil,
		"workers",
		len(order.Status.Workers),
	)
	g.Expect(order.DeletionTimestamp).NotTo(BeNil())
	g.Expect(order.Status.Workers).To(HaveLen(len(s.workers)))
	s.checkConsumerHandoff(ctx, g)

	for _, worker := range s.workers {
		saved := worker.instance
		current := &bmfv1.BareMetalInstance{}
		err := environment.Client.Get(ctx, client.ObjectKeyFromObject(saved), current)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(current.GetUID()).To(Equal(saved.GetUID()))
		g.Expect(current.GetDeletionTimestamp()).To(BeNil(), "BMI release must wait for consumer detach")
	}
}

// cleanupAfterFailure asks Fulfillment to delete the cluster and waits for its
// resources to disappear. It restores normal provider behavior so a stalled
// detach profile cannot prevent cleanup.
//
// A failed test may have stopped before recording every resource or job. This
// path finds the order by cluster ID and checks removal without requiring the
// automation jobs themselves to have succeeded.
func (s *clusterFixture) cleanupAfterFailure(ctx context.Context) {
	GinkgoHelper()
	By("restoring this case's provider responses before ordinary API deletion")
	Expect(s.settings.SetProfile(ctx, scenario.Normal)).To(Succeed())
	s.observeCleanupInputs(ctx, Default)
	s.requestDelete(ctx)
	By("waiting for the failed request's order to disappear while retaining partial inputs")
	Eventually(func(g Gomega) {
		s.observeCleanupInputs(ctx, g)

		var orders osacv1.ClusterOrderList

		err := environment.Client.List(ctx, &orders, client.InNamespace(environment.Namespace),
			client.MatchingLabels{"osac.openshift.io/clusterorder-uuid": s.clusterID})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(orders.Items).To(BeEmpty())
	}).WithContext(ctx).WithTimeout(8 * time.Minute).WithPolling(2 * time.Second).Should(Succeed())
	s.awaitCaseCleanup(ctx)
	s.cleanupFinished = true
}

// deleteClusterAndVerify requires every delete attempt to succeed before checking release.
func (s *clusterFixture) deleteClusterAndVerify(ctx context.Context) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()

	By("retaining partial inputs and requesting ordinary cluster deletion")
	s.observeCleanupInputs(ctx, Default)
	s.requestDelete(ctx)
	fixtures.ExpectSuccessfulJobs(
		ctx,
		executions,
		s.order,
		"osac-aap/playbook_osac_delete_hosted_cluster.yml",
		s.jobPods,
	)
	Eventually(func() error {
		_, err := getClusterOrder(ctx, environment, s.order.Name)
		return err
	}).WithContext(ctx).Should(MatchError(apierrors.IsNotFound, "Kubernetes NotFound"))
	s.awaitCaseCleanup(ctx)
	By("requiring every retained create and delete attempt to have succeeded")
	// Writers have stopped, so late submissions are included in this final check.
	for _, playbook := range []string{
		"osac-aap/playbook_osac_create_hosted_cluster.yml",
		"osac-aap/playbook_osac_delete_hosted_cluster.yml",
	} {
		fixtures.ExpectSuccessfulJobs(ctx, executions, s.order, playbook, s.jobPods)
	}

	for _, instance := range s.instances {
		for _, playbook := range []string{
			"osac-aap/playbook_osac_create_bare_metal_instance.yml",
			"osac-aap/playbook_osac_delete_bare_metal_instance.yml",
		} {
			fixtures.ExpectSuccessfulJobs(ctx, executions, instance, playbook, s.jobPods)
		}
	}

	s.cleanupFinished = true
	slog.InfoContext(ctx, "cluster deletion and inventory release verified", "cluster", s.clusterID, "order", s.order.Name)
}

// requestDelete asks Fulfillment to delete this test's cluster and accepts an
// already absent cluster. Later checks wait for its dependent resources to vanish.
func (s *clusterFixture) requestDelete(ctx context.Context) {
	GinkgoHelper()
	slog.InfoContext(ctx, "requesting cluster deletion", "run", s.runID, "cluster", s.clusterID)

	_, err := connectionClusters.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: s.clusterID}.Build())
	if status.Code(err) != codes.NotFound {
		Expect(err).NotTo(HaveOccurred())
	}
}

// awaitResourcesGone waits for this case's recorded graph and API request to disappear.
func (s *clusterFixture) awaitResourcesGone(ctx context.Context) {
	GinkgoHelper()
	By("waiting for this case's native roots and workers to disappear")

	var objects []client.Object

	if s.hostedCluster != nil {
		objects = append(objects, s.hostedCluster)
	}

	if s.nodePool != nil {
		objects = append(objects, s.nodePool)
	}

	if s.infraEnv != nil {
		objects = append(objects, s.infraEnv)
	}

	for _, agent := range s.agents {
		objects = append(objects, agent)
	}

	for _, instance := range s.instances {
		objects = append(objects, instance)
	}

	for _, object := range objects {
		current := object.DeepCopyObject().(client.Object)
		Eventually(func() error {
			return environment.Client.Get(ctx, client.ObjectKeyFromObject(object), current)
		}).WithContext(ctx).WithTimeout(time.Minute).Should(
			MatchError(apierrors.IsNotFound, "Kubernetes NotFound"),
			"%T %s/%s remains", object, object.GetNamespace(), object.GetName(),
		)
	}

	By("waiting for the case's user-data Secrets and namespaces to disappear")

	for secret := range s.userDataSecrets {
		Eventually(func() error {
			_, err := environment.Kube.CoreV1().Secrets(secret.Namespace).Get(ctx, secret.Name, metav1.GetOptions{})
			return err
		}).WithContext(ctx).WithTimeout(time.Minute).Should(
			MatchError(apierrors.IsNotFound, "Kubernetes NotFound"),
			"case user-data Secret remains",
		)
	}

	for namespace := range s.namespaces {
		Eventually(func() error {
			_, err := environment.Kube.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
			return err
		}).WithContext(ctx).WithTimeout(time.Minute).Should(
			MatchError(apierrors.IsNotFound, "Kubernetes NotFound"),
			"case namespace %s remains",
			namespace,
		)
	}

	By("waiting for Fulfillment to remove the request and all its allocations")

	if s.order != nil {
		Eventually(func(g Gomega) {
			g.Expect(s.workerBMIs(ctx, g)).To(BeEmpty())
		}).WithContext(ctx).WithTimeout(time.Minute).Should(Succeed())
	}

	Eventually(func() error {
		_, err := connectionClusters.Get(ctx, publicv1.ClustersGetRequest_builder{Id: s.clusterID}.Build())
		return err
	}).WithContext(ctx).WithTimeout(time.Minute).Should(
		MatchError(func(err error) bool { return status.Code(err) == codes.NotFound }, "Fulfillment NotFound"),
	)
}

// awaitCaseCleanup waits for case writers, their outputs and this cluster's inventory group.
func (s *clusterFixture) awaitCaseCleanup(ctx context.Context) {
	GinkgoHelper()
	By("waiting for this case's automation to stop while retaining partial inputs")
	Eventually(func(g Gomega) {
		s.observeCleanupInputs(ctx, g)

		if s.order != nil {
			g.Expect(executions.CheckIdleForResource(
				ctx,
				s.order,
				"osac-aap/playbook_osac_create_hosted_cluster.yml",
				"osac-aap/playbook_osac_delete_hosted_cluster.yml",
			)).
				To(Succeed())
		}

		for _, instance := range s.instances {
			g.Expect(executions.CheckIdleForResource(
				ctx,
				instance,
				"osac-aap/playbook_osac_create_bare_metal_instance.yml",
				"osac-aap/playbook_osac_delete_bare_metal_instance.yml",
			)).
				To(Succeed())
		}
	}).WithContext(ctx).WithTimeout(2 * time.Minute).Should(Succeed())

	// Once writers stop, resource removal and inventory release are stable checkpoints.
	s.awaitResourcesGone(ctx)

	By("waiting for this cluster's original inventory group to be released")
	Eventually(func() error {
		return fixtures.CheckInventoryReleased(ctx, environment, s.group.Inventory)
	}).WithContext(ctx).WithTimeout(2 * time.Minute).Should(Succeed())
}
