// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
	scenario "github.com/osac-project/osac/tests/integration/caas/internal/simulator/scenario"
	"github.com/osac-project/osac/tests/integration/caas/suites/fixtures"
)

var _ = Describe("Discovery-data delivery and recovery", Label("caas-it", "lifecycle"), func() {
	var s *clusterFixture

	Context("With invalid discovery ignition", Label("negative"), func() {
		BeforeEach(func(ctx SpecContext) {
			s = newClusterFixture(ctx, scenario.Normal, defaultGroup)
		}, NodeTimeout(5*time.Minute))
		It("recovers without premature or duplicate worker allocations", func(ctx SpecContext) {
			By("serving invalid discovery data through the installed HTTP fixture")
			original := s.serveInvalidIgnition(ctx)

			By("requesting a cluster while discovery data is invalid")
			Expect(s.createCluster(ctx)).To(Succeed())
			s.awaitProvisioningJob(ctx)
			s.awaitWorkerInputs(ctx)
			Eventually(
				func(g Gomega) { s.checkDiscoveryBlocked(ctx, g) },
			).WithContext(ctx).
				WithTimeout(time.Minute).
				Should(Succeed())

			By("requiring the rejected discovery data to prevent worker allocation")
			Consistently(
				func(g Gomega) { s.checkDiscoveryBlocked(ctx, g) },
			).WithContext(ctx).
				WithTimeout(8 * time.Second).
				Should(Succeed())

			By("restoring valid discovery data and observing the served bytes")
			Expect(s.ignition.Set(ctx, original)).To(Succeed())
			Expect(s.ignition.WaitForContent(ctx, original)).To(Succeed())

			By("waiting for the original request to provision exactly its two workers")
			s.awaitWorkerHandoffWithAllocationTimeout(ctx, 20*time.Minute)
			s.awaitProvisioningSuccess(ctx)
			s.awaitReady(ctx)

			By("checking exact discovery-data delivery after recovery")
			s.verifyUserData(ctx, original)

			By("deleting the recovered cluster through Fulfillment")
			s.deleteClusterAndVerify(ctx)
		}, SpecTimeout(30*time.Minute))
	})
})

// serveInvalidIgnition changes the installed fixture and registers restoration before cluster cleanup.
func (s *clusterFixture) serveInvalidIgnition(ctx context.Context) []byte {
	GinkgoHelper()
	original, err := s.ignition.Stored(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Valid(original)).To(BeTrue())
	// Ginkgo cleanup is LIFO; restore the fixture before cluster teardown.
	DeferCleanup(func(ctx context.Context) {
		restored := false
		defer func() {
			if !restored {
				AbortSuite("discovery fixture restoration failed; stopping inventory group reuse")
			}
		}()

		By("restoring this worker's discovery fixture before cluster cleanup")
		Expect(s.ignition.Set(ctx, original)).To(Succeed())
		Expect(s.ignition.WaitForContent(ctx, original)).To(Succeed())
		restored = true
	}, NodeTimeout(3*time.Minute))
	malformed := []byte("{invalid discovery ignition")
	Expect(s.ignition.Set(ctx, malformed)).To(Succeed())
	Expect(s.ignition.WaitForContent(ctx, malformed)).To(Succeed())

	return original
}

// checkDiscoveryBlocked checks the real API boundary while invalid discovery data is served.
func (s *clusterFixture) checkDiscoveryBlocked(ctx context.Context, g Gomega) {
	GinkgoHelper()
	// Bound all reads in this monitoring sample independently of the spec deadline.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	order, err := getClusterOrder(ctx, environment, s.order.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(order.UID).To(Equal(s.order.UID))
	g.Expect(order.Status.Phase).NotTo(Equal(osacv1.ClusterOrderPhaseReady))
	g.Expect(apimeta.IsStatusConditionTrue(order.Status.Conditions, "InfraEnvReady")).To(BeFalse())

	for _, worker := range order.Status.Workers {
		g.Expect(worker.BareMetalInstance.ID).To(BeEmpty())
	}

	allocationCount := len(s.workerBMIs(ctx, g))
	slog.DebugContext(
		ctx,
		"observed allocation gate while discovery is invalid",
		"cluster",
		s.clusterID,
		"order",
		order.Name,
		"uid",
		order.UID,
		"allocations",
		allocationCount,
	)
	g.Expect(allocationCount).To(BeZero(), "invalid discovery must prevent real BMI requests; payloads omitted")
	infra := &assistedv1.InfraEnv{}
	g.Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(s.infraEnv), infra)).To(Succeed())
	url := infra.Status.BootArtifacts.DiscoveryIgnitionURL
	g.Expect(url).NotTo(BeEmpty(), "discovery input must be published before checking blocked allocation")
	s.checkFulfillmentNotReady(ctx, g)
}

// verifyUserData follows completed provisioning's exact discovery-data delivery chain.
// Comparisons expose only equality, never user-data payloads or secret-bearing objects.
func (s *clusterFixture) verifyUserData(ctx context.Context, expected []byte) {
	GinkgoHelper()
	Expect(expected).NotTo(BeEmpty())
	infra := &assistedv1.InfraEnv{}
	Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(s.infraEnv), infra)).To(Succeed())
	Expect(infra.UID).To(Equal(s.infraEnv.UID))
	url := infra.Status.BootArtifacts.DiscoveryIgnitionURL
	Expect(url).NotTo(BeEmpty())
	allocations := s.workerBMIs(ctx, Default)
	allocationCount := len(allocations)
	Expect(allocationCount).To(Equal(len(s.workers)), "expected one API allocation per original worker; payloads omitted")

	byID := make(map[string]*publicv1.BareMetalInstance, len(allocations))
	for _, allocation := range allocations {
		byID[allocation.GetId()] = allocation
	}

	for _, worker := range s.workers {
		instance := worker.instance
		id := instance.GetLabels()["osac.openshift.io/baremetalinstance-uuid"]
		allocation := byID[id]
		Expect(allocation).NotTo(BeNil(), "original allocation must retain its user-data source")
		Expect(allocation.GetMetadata().GetTenant()).To(Equal(tenantID))
		Expect(
			allocation.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"],
		).To(Equal("ClusterOrder/" + s.order.Name))
		source := allocation.GetSpec().GetUserDataSource()
		Expect(source).NotTo(BeNil())
		Expect(source.GetSecret()).To(BeNil(), "OSAC submits inline discovery data")
		Expect(bytes.Equal([]byte(source.GetInline()), expected)).To(BeTrue(), "BMI inline ignition must equal served bytes")
		slog.DebugContext(
			ctx,
			"verified inline discovery delivery",
			"instance",
			instance.GetName(),
			"uid",
			instance.GetUID(),
			"allocation",
			id,
		)
		s.verifyProvisionedUserData(ctx, instance, expected)
	}
}

// verifyProvisionedUserData checks the Fulfillment-owned hub Secret, then the allocated host.
func (s *clusterFixture) verifyProvisionedUserData(
	ctx context.Context,
	saved *bmfv1.BareMetalInstance,
	expected []byte,
) {
	GinkgoHelper()
	instance := &bmfv1.BareMetalInstance{}
	Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(saved), instance)).To(Succeed())
	Expect(instance.UID).To(Equal(saved.UID))
	Expect(instance.Annotations[harness.TenantAnnotation]).To(Equal(tenantID))
	parameters := instance.Spec.TemplateParameters

	var inputs struct {
		UserDataSecret string `json:"userDataSecret"`
	}

	Expect(json.Unmarshal([]byte(parameters), &inputs)).To(Succeed())
	Expect(inputs.UserDataSecret).NotTo(BeEmpty())
	secret, err := environment.Kube.CoreV1().
		Secrets(instance.GetNamespace()).
		Get(ctx, inputs.UserDataSecret, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	Expect(
		secret.Labels["osac.openshift.io/baremetalinstance-uuid"],
	).To(Equal(saved.GetLabels()["osac.openshift.io/baremetalinstance-uuid"]))
	owned := false

	for _, owner := range secret.OwnerReferences {
		if owner.Kind == "BareMetalInstance" && owner.Name == instance.GetName() && owner.UID == instance.GetUID() {
			owned = true
		}
	}

	Expect(owned).To(BeTrue(), "Fulfillment user-data Secret belongs to this BMI incarnation")
	Expect(bytes.Equal(secret.Data["userdata"], expected)).To(BeTrue(), "hub Secret must preserve discovery bytes")
	s.userDataSecrets[types.NamespacedName{Namespace: secret.Namespace, Name: secret.Name}] = struct{}{}
	slog.DebugContext(
		ctx,
		"verified hub discovery Secret ownership and bytes",
		"instance",
		instance.GetName(),
		"instanceUID",
		instance.GetUID(),
		"secret",
		secret.Name,
		"secretUID",
		secret.UID,
	)
	key := fixtures.VerifyHostUserData(ctx, environment, instance, expected)
	s.userDataSecrets[key] = struct{}{}
}
