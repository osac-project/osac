// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = Describe("NodePool inputs and observations", func() {
	DescribeTable("establishes native parent ownership without replacing another incarnation",
		func(ctx SpecContext, stale bool) {
			hc := agentHostedCluster()
			retained := metav1.OwnerReference{APIVersion: "v1", Kind: "Secret", Name: "retained", UID: "retained"}

			pool := &hypershiftv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hc.Namespace, Name: "workers", UID: "pool",
					OwnerReferences: []metav1.OwnerReference{retained},
				},
				Spec: hypershiftv1.NodePoolSpec{
					ClusterName: hc.Name,
					Platform:    hypershiftv1.NodePoolPlatform{Type: hypershiftv1.AgentPlatform},
				},
			}
			if stale {
				owner := hc.DeepCopy()
				owner.UID = "previous-cluster"
				Expect(controllerutil.SetOwnerReference(owner, pool, hypershiftScheme())).To(Succeed())
			}

			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc, pool).Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			before := pool.DeepCopy()
			r := &NodePoolReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)})
			Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())

			if stale {
				Expect(err).To(MatchError(ContainSubstring("another HostedCluster incarnation")))
				Expect(pool).To(Equal(before))
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(pool.OwnerReferences).To(ContainElement(retained))
				Expect(kube.MatchesOwner(pool, hc, hypershiftv1.GroupVersion.WithKind("HostedCluster"))).To(BeTrue())
				Expect(metav1.GetControllerOf(pool)).To(BeNil())
				Expect(pool.Finalizers).To(ContainElement(nodePoolFinalizer))
			}
		}, Entry("pending native ownership", false), Entry("previous parent incarnation", true))

	It("rejects Replace upgrades before publishing worker inputs", func(ctx SpecContext) {
		pool := &hypershiftv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers"},
			Spec: hypershiftv1.NodePoolSpec{
				Management: hypershiftv1.NodePoolManagement{UpgradeType: hypershiftv1.UpgradeTypeReplace},
			},
		}
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).Build()
		r := &NodePoolReconciler{Client: c, APIReader: c}
		_, err := r.inputs(ctx, pool, &capiv1.Cluster{})
		Expect(err).To(MatchError(`unsupported NodePool upgrade type "Replace"`))

		var (
			secrets   corev1.SecretList
			templates agentv1.AgentMachineTemplateList
			workers   capiv1.MachineSetList
		)

		Expect(c.List(ctx, &secrets)).To(Succeed())
		Expect(c.List(ctx, &templates)).To(Succeed())
		Expect(c.List(ctx, &workers)).To(Succeed())
		Expect(secrets.Items).To(BeEmpty())
		Expect(templates.Items).To(BeEmpty())
		Expect(workers.Items).To(BeEmpty())
	})

	DescribeTable("preserves the NodePool when the HostedCluster cannot be read", func(ctx SpecContext, deleting bool) {
		pool := &hypershiftv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Namespace: "working", Name: "workers", UID: "original-pool"},
			Spec: hypershiftv1.NodePoolSpec{
				ClusterName: "cluster", Replicas: new(int32(2)),
				Platform: hypershiftv1.NodePoolPlatform{Type: hypershiftv1.AgentPlatform},
			},
			Status: hypershiftv1.NodePoolStatus{Replicas: 2},
		}
		if deleting {
			pool.DeletionTimestamp = new(metav1.Now())
			pool.Finalizers = []string{nodePoolFinalizer}
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(pool).WithStatusSubresource(pool).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		original := pool.DeepCopy()
		failure := errors.New("native dependency unavailable")
		r := &NodePoolReconciler{Client: c, APIReader: failedControlPlaneRead{Reader: c, failure: failure}}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)})
		Expect(errors.Is(err, failure)).To(BeTrue(), "original failure lost: %v", err)
		Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool).To(Equal(original))
	}, Entry("live", false), Entry("deleting", true))

	DescribeTable("cleans bootstrap inputs and preserves foreign tokens", func(ctx SpecContext, parentExists bool) {
		hc := agentHostedCluster()
		pool := &hypershiftv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub", Name: "workers", UID: "pool", Finalizers: []string{nodePoolFinalizer},
				DeletionTimestamp: new(metav1.Now()),
			},
			Spec: hypershiftv1.NodePoolSpec{
				ClusterName: hc.Name, Platform: hypershiftv1.NodePoolPlatform{Type: hypershiftv1.AgentPlatform},
			},
		}
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{
			Namespace: "hub-cluster", Name: "cluster", UID: "capi-cluster",
			Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
			Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
		}}
		source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: "hub-cluster", Name: "workers-bootstrap", UID: "shared-input",
			Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
			Annotations: map[string]string{kube.NodePoolAnnotation: "hub/workers"},
		}}
		token := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: "hub-cluster", Name: "workers-token", UID: "foreign-token",
		}}

		Expect(controllerutil.SetControllerReference(cluster, source, hypershiftScheme())).To(Succeed())

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(pool, cluster, source, token).Build()
		if parentExists {
			Expect(c.Create(ctx, hc)).To(Succeed())
		}

		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		originalToken := token.DeepCopy()
		r := &NodePoolReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)}
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		err = c.Get(ctx, client.ObjectKeyFromObject(source), source)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		err = c.Get(ctx, client.ObjectKeyFromObject(pool), pool)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cleanup must reach the foreign token lookup")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(token).To(Equal(originalToken))
	}, Entry("live parent", true), Entry("missing parent", false))

	DescribeTable("preserves deleting NodePools outside their live parent's Agent scope",
		func(ctx SpecContext, agentNamespace string) {
			hc := agentHostedCluster()
			if agentNamespace == "" {
				hc.Spec.Platform.Agent = nil
			} else {
				hc.Spec.Platform.Agent.AgentNamespace = agentNamespace
			}

			pool := &hypershiftv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hc.Namespace, Name: "workers", UID: "original-pool",
					Finalizers: []string{nodePoolFinalizer}, DeletionTimestamp: new(metav1.Now()),
				},
				Spec: hypershiftv1.NodePoolSpec{
					ClusterName: hc.Name, Platform: hypershiftv1.NodePoolPlatform{Type: hypershiftv1.AgentPlatform},
				},
			}
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(pool, hc).Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			original := pool.DeepCopy()
			r := &NodePoolReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			Expect(pool).To(Equal(original))
		},
		Entry("unserved Agent namespace", "other"),
		Entry("missing Agent settings", ""),
	)

	It("uses separate native readiness sources and pauses worker demand without changing inputs", func(ctx SpecContext) {
		hc := agentHostedCluster()
		hc.Status.IgnitionEndpoint = "ignition.example.invalid"
		pool := &hypershiftv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers", UID: "original-pool"},
			Spec: hypershiftv1.NodePoolSpec{
				ClusterName: hc.Name,
				Replicas:    new(int32(2)),
				Platform:    hypershiftv1.NodePoolPlatform{Type: hypershiftv1.AgentPlatform},
				Management:  hypershiftv1.NodePoolManagement{UpgradeType: hypershiftv1.UpgradeTypeInPlace},
			},
		}
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "hub-cluster",
				Name:        "cluster",
				UID:         "original-cluster",
				Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			},
		}
		set := &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   cluster.Namespace,
				Name:        pool.Name,
				UID:         "original-set",
				Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
				Annotations: map[string]string{kube.NodePoolAnnotation: "hub/workers"},
			},
			Spec: capiv1.MachineSetSpec{
				Replicas: new(int32(2)),
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{kube.ClusterLabel: cluster.Name}},
			},
			Status: capiv1.MachineSetStatus{AvailableReplicas: 2},
		}
		conditions.MarkFalse(
			set,
			capiv1.ReadyCondition,
			"NativeResizePending",
			capiv1.ConditionSeverityInfo,
			"Waiting for resize observation",
		)
		Expect(controllerutil.SetOwnerReference(cluster, set, hypershiftScheme())).To(Succeed())
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Namespace: set.Namespace, Name: "worker", UID: "original-machine"},
		}
		conditions.MarkTrue(machine, capiv1.ReadyCondition)
		Expect(controllerutil.SetControllerReference(set, machine, hypershiftScheme())).To(Succeed())
		objects := append([]client.Object{pool, cluster, set, machine}, ignitionInputs(pool, cluster)...)
		c := fake.NewClientBuilder().
			WithScheme(hypershiftScheme()).
			WithObjects(objects...).
			WithStatusSubresource(pool, set).
			Build()
		r := &NodePoolReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)}
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, pool)).To(Succeed())
		Expect(pool.Status.Replicas).To(Equal(int32(2)))
		Expect(
			pool.Status.Conditions,
		).To(ContainElement(And(
			HaveField("Type", "Ready"),
			HaveField("Status", corev1.ConditionFalse),
			HaveField("Reason", "NativeResizePending"),
		)))
		Expect(
			pool.Status.Conditions,
		).To(ContainElement(And(HaveField("Type", "AllMachinesReady"), HaveField("Status", corev1.ConditionTrue))))

		secret := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: "workers-bootstrap"}, secret)).To(Succeed())
		data := secret.DeepCopy().Data
		Expect(nodePoolForChild(ctx, secret)).To(ConsistOf(request))
		Expect(r.poolForMachine(ctx, machine)).To(ConsistOf(request))

		By("preserving CAPI selector normalization across repeated reconciliation")

		Expect(c.Get(ctx, client.ObjectKeyFromObject(set), set)).To(Succeed())
		Expect(set.Spec.Selector.MatchLabels).To(HaveKeyWithValue(kube.ClusterLabel, cluster.Name))
		Expect(set.Spec.Template.Labels).To(HaveKeyWithValue(kube.ClusterLabel, cluster.Name))
		before := set.DeepCopy()
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(set), set)).To(Succeed())
		Expect(set).To(Equal(before))

		for _, value := range []string{"true", "True"} {
			pool.Spec.PausedUntil = new(value)
			pool.Spec.Replicas = new(int32(3))
			Expect(c.Update(ctx, pool)).To(Succeed())
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(set), set)).To(Succeed())
			Expect(set.Annotations).To(HaveKeyWithValue(capiv1.PausedAnnotation, "true"))
			Expect(*set.Spec.Replicas).To(Equal(int32(2)))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(secret), secret)).To(Succeed())
			Expect(secret.Data).To(Equal(data))
		}

		pool.Spec.PausedUntil = nil
		Expect(c.Update(ctx, pool)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(set), set)).To(Succeed())
		Expect(set.Annotations).NotTo(HaveKey(capiv1.PausedAnnotation))
		Expect(*set.Spec.Replicas).To(Equal(int32(3)))
		Expect(set.UID).To(BeEquivalentTo("original-set"))
	})
})

type failedControlPlaneRead struct {
	client.Reader
	failure error
}

// Get fails the parent lookup while allowing the test to observe the NodePool.
func (r failedControlPlaneRead) Get(
	ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption,
) error {
	if _, isHostedCluster := obj.(*hypershiftv1.HostedCluster); isHostedCluster {
		return r.failure
	}

	return r.Reader.Get(ctx, key, obj, opts...)
}
