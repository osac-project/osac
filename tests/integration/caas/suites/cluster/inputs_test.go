// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

// observeInputs binds native roots and reports whether this pass observed the CAPI Cluster.
func (s *clusterFixture) observeInputs(ctx context.Context, g Gomega) bool {
	GinkgoHelper()
	s.observeClusterOrder(ctx, g)

	if s.order == nil {
		return false
	}

	s.observeHostedCluster(ctx, g)
	foundCluster := s.observeCAPICluster(ctx, g)
	s.observeInfraEnv(ctx, g)
	s.observeNodePool(ctx, g)

	return foundCluster
}

// observeClusterOrder retains the request's order and checks its tenant and UID.
func (s *clusterFixture) observeClusterOrder(ctx context.Context, g Gomega) {
	GinkgoHelper()

	var orders osacv1.ClusterOrderList

	err := environment.Client.List(ctx, &orders, client.InNamespace(environment.Namespace),
		client.MatchingLabels{"osac.openshift.io/clusterorder-uuid": s.clusterID})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(len(orders.Items)).To(BeNumerically("<=", 1))

	if len(orders.Items) == 1 {
		order := orders.Items[0].DeepCopy()
		g.Expect(order.Annotations[harness.TenantAnnotation]).To(Equal(tenantID))

		if s.order != nil {
			g.Expect(order.UID).To(Equal(s.order.UID))
		}

		s.order = order
	}
}

// observeHostedCluster binds the reference published by OSAC and preserves its UID.
func (s *clusterFixture) observeHostedCluster(ctx context.Context, g Gomega) {
	GinkgoHelper()
	ref := s.order.Status.ClusterReference
	if ref == nil || ref.Namespace == "" || ref.HostedClusterName == "" {
		return
	}

	hc := &hypershiftv1.HostedCluster{}

	err := environment.Client.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.HostedClusterName}, hc)
	if apierrors.IsNotFound(err) {
		return
	}

	g.Expect(err).NotTo(HaveOccurred())

	if s.hostedCluster != nil {
		g.Expect(hc.GetUID()).To(Equal(s.hostedCluster.GetUID()))
	}

	s.hostedCluster = hc
	g.Expect(s.settings.BindHostedCluster(ctx, hc.GetUID())).To(Succeed())
}

// observeCAPICluster reports a current observation and retains the bound Cluster for UID checks.
func (s *clusterFixture) observeCAPICluster(ctx context.Context, g Gomega) bool {
	GinkgoHelper()

	hc := s.hostedCluster
	if hc == nil || hc.Spec.InfraID == "" {
		return false
	}

	cluster := &capiv1.Cluster{}
	key := client.ObjectKey{Namespace: hc.Namespace + "-" + hc.Name, Name: hc.Spec.InfraID}

	err := environment.Client.Get(ctx, key, cluster)
	if apierrors.IsNotFound(err) {
		return false
	}

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cluster.Annotations[harness.HostedClusterAnnotation]).To(Equal(hc.Namespace + "/" + hc.Name))
	g.Expect(cluster.UID).NotTo(BeEmpty())

	if s.capiCluster != nil {
		g.Expect(cluster.UID).To(Equal(s.capiCluster.UID))
	}

	g.Expect(s.settings.BindCluster(ctx, cluster.UID)).To(Succeed())
	s.capiCluster = cluster

	return true
}

// observeInfraEnv binds the order's controlled InfraEnv independently of
// HostedCluster readiness and preserves the original InfraEnv UID.
func (s *clusterFixture) observeInfraEnv(ctx context.Context, g Gomega) {
	GinkgoHelper()

	var infraEnvs assistedv1.InfraEnvList

	err := environment.Client.List(ctx, &infraEnvs, client.InNamespace(environment.Namespace))
	g.Expect(err).NotTo(HaveOccurred())

	for _, infra := range infraEnvs.Items {
		owner := metav1.GetControllerOf(&infra)
		if owner == nil || owner.UID != s.order.UID {
			continue
		}

		if s.infraEnv != nil {
			g.Expect(infra.GetUID()).To(Equal(s.infraEnv.GetUID()))
		}

		s.infraEnv = infra.DeepCopy()
		g.Expect(s.settings.BindInfraEnv(ctx, infra.GetUID())).To(Succeed())
	}
}

// observeNodePool retains the playbook's NodePool for this HostedCluster and
// ClusterOrder, checking that its UID stays unchanged.
func (s *clusterFixture) observeNodePool(ctx context.Context, g Gomega) {
	GinkgoHelper()

	if s.hostedCluster == nil {
		return
	}

	var pools hypershiftv1.NodePoolList

	err := environment.Client.List(ctx, &pools, client.InNamespace(s.hostedCluster.Namespace),
		client.MatchingLabels{harness.OrderLabel: s.order.Name})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(len(pools.Items)).To(BeNumerically("<=", 1))

	if len(pools.Items) == 1 {
		pool := &pools.Items[0]
		g.Expect(pool.Spec.ClusterName).To(Equal(s.hostedCluster.Name))

		if s.nodePool != nil {
			g.Expect(pool.UID).To(Equal(s.nodePool.UID))
		}

		s.nodePool = pool.DeepCopy()
	}
}

// observeCleanupInputs retains partial resources even if provisioning stopped early.
func (s *clusterFixture) observeCleanupInputs(ctx context.Context, g Gomega) {
	GinkgoHelper()
	s.observeInputs(ctx, g)

	if s.order == nil {
		return
	}

	s.observeCleanupAllocations(ctx, g)
	s.observeCleanupAgents(ctx, g)
	s.observeCleanupNamespaces(ctx, g)
}

// observeCleanupAllocations retains every allocation and its owned Secrets,
// including workers that were never registered.
func (s *clusterFixture) observeCleanupAllocations(ctx context.Context, g Gomega) {
	GinkgoHelper()

	var instances bmfv1.BareMetalInstanceList

	err := environment.Client.List(ctx, &instances, client.InNamespace(environment.Namespace),
		client.MatchingLabels{"osac.openshift.io/cluster-order": s.order.Name})
	g.Expect(err).NotTo(HaveOccurred())

	for _, instance := range instances.Items {
		g.Expect(instance.GetAnnotations()[harness.TenantAnnotation]).To(Equal(tenantID))
		g.Expect(instance.GetAnnotations()["osac.openshift.io/owner-reference"]).To(Equal("ClusterOrder/" + s.order.Name))
		s.instances[instance.GetUID()] = instance.DeepCopy()
		secrets, err := environment.Kube.CoreV1().
			Secrets(instance.GetNamespace()).
			List(
				ctx,
				metav1.ListOptions{
					LabelSelector: "osac.openshift.io/baremetalinstance-uuid=" +
						instance.GetLabels()["osac.openshift.io/baremetalinstance-uuid"],
				},
			)
		g.Expect(err).NotTo(HaveOccurred())

		for _, secret := range secrets.Items {
			for _, owner := range secret.OwnerReferences {
				if owner.UID == instance.GetUID() {
					s.userDataSecrets[types.NamespacedName{Namespace: secret.Namespace, Name: secret.Name}] = struct{}{}
				}
			}
		}
	}
}

// observeCleanupAgents retains registrations belonging to the original InfraEnv
// incarnation, including registrations with a non-controller owner reference.
func (s *clusterFixture) observeCleanupAgents(ctx context.Context, g Gomega) {
	GinkgoHelper()

	if s.infraEnv == nil {
		return
	}

	var agents assistedv1.AgentList

	err := environment.Client.List(ctx, &agents, client.InNamespace(s.infraEnv.Namespace),
		client.MatchingLabels{"infraenvs.agent-install.openshift.io": s.infraEnv.Name})
	g.Expect(err).NotTo(HaveOccurred())

	for _, agent := range agents.Items {
		for _, owner := range agent.GetOwnerReferences() {
			if owner.UID == s.infraEnv.GetUID() {
				s.agents[agent.GetUID()] = agent.DeepCopy()
			}
		}
	}
}

// observeCleanupNamespaces retains working and control-plane namespaces by their
// original identity, including namespaces created before readiness.
func (s *clusterFixture) observeCleanupNamespaces(ctx context.Context, g Gomega) {
	GinkgoHelper()
	namespaces, err := environment.Kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	g.Expect(err).NotTo(HaveOccurred())

	for _, ns := range namespaces.Items {
		working := ns.Labels[harness.OrderLabel] == s.order.Name && ns.Labels["app.kubernetes.io/name"] == "osac-operator"
		controlPlane := s.hostedCluster != nil &&
			ns.Name == s.hostedCluster.GetNamespace()+"-"+s.hostedCluster.GetName() &&
			ns.Annotations[harness.HostedClusterAnnotation] == s.hostedCluster.GetNamespace()+"/"+s.hostedCluster.GetName()
		if working || controlPlane {
			if uid, seen := s.namespaces[ns.Name]; seen {
				g.Expect(ns.UID).To(Equal(uid), "case namespace incarnation changed")
			}

			s.namespaces[ns.Name] = ns.UID
		}
	}
}

// getClusterOrder observes the native application contract through the harness client.
func getClusterOrder(ctx context.Context, environment *harness.Environment, name string) (*osacv1.ClusterOrder, error) {
	order := &osacv1.ClusterOrder{}
	err := environment.Client.Get(ctx, client.ObjectKey{Namespace: environment.Namespace, Name: name}, order)
	return order, err
}
