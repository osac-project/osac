// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = Describe("HyperShift output ownership", func() {
	DescribeTable("rejects writes from an outdated output observation", func(ctx SpecContext, variant string) {
		pool := &hypershiftv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers"}}
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}
		current := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "workers-bootstrap", UID: "original-output",
				Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
				Annotations: map[string]string{kube.NodePoolAnnotation: "hub/workers"},
			},
			Data: map[string][]byte{"value": []byte("old bootstrap")},
		}
		Expect(controllerutil.SetControllerReference(cluster, current, hypershiftScheme())).To(Succeed())
		c := fake.NewClientBuilder().
			WithScheme(hypershiftScheme()).
			WithGlobalResourceVersionCounter().
			WithObjects(ignitionInputs(pool, cluster)...).WithObjects(current).
			Build()
		changed := false

		var expected *corev1.Secret

		reader := interceptor.NewClient(c, interceptor.Funcs{
			Get: func(
				ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption,
			) error {
				if err := c.Get(ctx, key, object, opts...); err != nil {
					return err
				}

				if !changed && key == client.ObjectKeyFromObject(current) {
					changed = true
					// Keep the original observation while another writer changes the stored output.
					expected = object.(*corev1.Secret).DeepCopy()

					switch variant {
					case "source":
						expected.Annotations[kube.NodePoolAnnotation] = "hub/other"
						Expect(c.Update(ctx, expected)).To(Succeed())
					case "replacement":
						Expect(c.Delete(ctx, expected)).To(Succeed())
						expected.UID, expected.ResourceVersion = "replacement-output", ""
						Expect(c.Create(ctx, expected)).To(Succeed())
					case "missing":
						Expect(c.Delete(ctx, expected)).To(Succeed())
						expected = nil
					}
				}

				return nil
			},
		})
		r := &NodePoolReconciler{Client: c, APIReader: reader}

		By("rejecting a write after the source changes, disappears or is replaced")

		err := r.ensureBootstrap(ctx, pool, cluster)

		Expect(changed).To(BeTrue(), "the concurrent writer must act after the validation read")

		if variant == "missing" {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		} else {
			Expect(apierrors.IsConflict(err)).To(BeTrue())
		}

		By("preserving the concurrent writer's complete result")

		after := &corev1.Secret{}

		err = c.Get(ctx, client.ObjectKeyFromObject(current), after)
		if expected == nil {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		} else {
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(expected))
		}
	},
		Entry("source annotations change after validation", "source"),
		Entry("the output is replaced after validation", "replacement"),
		Entry("the output disappears after validation", "missing"),
	)

	DescribeTable(
		"preserves rejected HostedCluster outputs during cleanup",
		func(ctx SpecContext, testCase hostedClusterCleanupCase) {
			hc := agentHostedCluster()
			hc.Finalizers = []string{hostedClusterFinalizer, "foreign.example/cleanup"}
			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "hub-cluster", UID: "foreign-namespace"}}
			cluster := &capiv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "hub-cluster", Name: "cluster", UID: "foreign-cluster"},
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "cluster-admin-kubeconfig", UID: "foreign-secret"},
			}
			old := hc.DeepCopy()
			old.UID = "previous-hc"
			Expect(controllerutil.SetControllerReference(old, secret, hypershiftScheme())).To(Succeed())

			for _, output := range []struct {
				object client.Object
				source string
			}{{namespace, testCase.namespaceSource}, {cluster, testCase.clusterSource}} {
				if output.source != "" {
					output.object.SetLabels(map[string]string{kube.ManagedByLabel: kube.ManagerName})
					output.object.SetAnnotations(map[string]string{kube.HostedClusterAnnotation: output.source})
				}
			}

			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc, namespace, cluster, secret).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}

			var snapshots []client.Object

			for _, object := range []client.Object{namespace, cluster, secret} {
				Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
				snapshots = append(snapshots, object.DeepCopyObject().(client.Object))
			}

			By("rejecting foreign control-plane resources during setup")

			_, err := r.inputs(ctx, hc)
			Expect(err).To(HaveOccurred())
			By("deleting the failed request without taking ownership of rejected outputs")
			Expect(c.Delete(ctx, hc)).To(Succeed())

			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			if testCase.wantCleanupError {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(result.RequeueAfter).To(BeZero())

			for _, before := range snapshots {
				after := before.DeepCopyObject().(client.Object)
				Expect(c.Get(ctx, client.ObjectKeyFromObject(before), after)).To(Succeed())
				Expect(after).To(Equal(before))
			}

			Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), hc)).To(Succeed())

			if testCase.keepFinalizer {
				Expect(hc.Finalizers).To(ContainElement(hostedClusterFinalizer))
			} else {
				Expect(hc.Finalizers).NotTo(ContainElement(hostedClusterFinalizer))
			}

			Expect(hc.Finalizers).To(ContainElement("foreign.example/cleanup"))
		},
		Entry("unmanaged outputs", hostedClusterCleanupCase{}),
		Entry(
			"outputs linked to another HostedCluster",
			hostedClusterCleanupCase{namespaceSource: "hub/other", clusterSource: "hub/other"},
		),
		Entry("a foreign Cluster inside the current namespace", hostedClusterCleanupCase{
			namespaceSource: "hub/cluster", wantCleanupError: true, keepFinalizer: true,
		}),
	)

	DescribeTable(
		"checks NodePool and CAPI controller identity during deletion",
		func(ctx SpecContext, testCase nodePoolCleanupCase) {
			hc := agentHostedCluster()
			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub-cluster", Name: "cluster", UID: "capi-cluster",
				Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			}}
			pool := &hypershiftv1.NodePool{ObjectMeta: metav1.ObjectMeta{
				Namespace:  "hub",
				Name:       "workers",
				UID:        "current-pool",
				Finalizers: []string{nodePoolFinalizer, "foreign.example/cleanup"},
			}}
			pool.Spec.ClusterName = hc.Name
			controller := cluster.DeepCopy()
			controller.UID = testCase.controllerUID
			outputs := []client.Object{
				&capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Namespace: "hub-cluster", Name: "workers", UID: "set"}},
				&agentv1.AgentMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{Namespace: "hub-cluster", Name: "workers", UID: "template"},
				},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "hub-cluster", Name: "workers-bootstrap", UID: "secret"}},
			}
			objects := []client.Object{hc, cluster, pool}

			for _, output := range outputs {
				if testCase.managed {
					output.SetLabels(map[string]string{kube.ManagedByLabel: kube.ManagerName})
				} else if testCase.foreignMarker {
					output.SetLabels(map[string]string{kube.ManagedByLabel: "other-provider"})
				}

				output.SetAnnotations(map[string]string{kube.NodePoolAnnotation: testCase.poolSource})

				if !testCase.ownerless {
					Expect(controllerutil.SetControllerReference(controller, output, hypershiftScheme())).To(Succeed())
				}

				objects = append(objects, output)
			}

			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(objects...).Build()
			for _, output := range outputs {
				Expect(c.Get(ctx, client.ObjectKeyFromObject(output), output)).To(Succeed())
			}

			r := &NodePoolReconciler{Client: c, APIReader: c}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())

			By("releasing owned outputs across successive deletion observations")

			for range 5 {
				_, err := r.delete(ctx, pool)
				Expect(err).NotTo(HaveOccurred())
				Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			}

			for _, output := range outputs {
				current := output.DeepCopyObject().(client.Object)

				err := c.Get(ctx, client.ObjectKeyFromObject(output), current)
				if testCase.outputsDeleted {
					Expect(apierrors.IsNotFound(err)).To(BeTrue())
				} else {
					Expect(err).NotTo(HaveOccurred())
					Expect(current).To(Equal(output))
				}
			}

			Expect(pool.Finalizers).NotTo(ContainElement(nodePoolFinalizer))
			Expect(pool.Finalizers).To(ContainElement("foreign.example/cleanup"))
		},
		Entry(
			"deletes current owned outputs",
			nodePoolCleanupCase{managed: true, poolSource: "hub/workers", controllerUID: "capi-cluster", outputsDeleted: true},
		),
		Entry("deletes unmarked outputs with the original Cluster owner",
			nodePoolCleanupCase{poolSource: "hub/workers", controllerUID: "capi-cluster", outputsDeleted: true}),
		Entry(
			"deletes foreign-marked outputs with the original Cluster owner",
			nodePoolCleanupCase{
				foreignMarker: true, poolSource: "hub/workers", controllerUID: "capi-cluster", outputsDeleted: true,
			},
		),
		Entry("preserves unmarked ownerless outputs",
			nodePoolCleanupCase{ownerless: true, poolSource: "hub/workers"}),
		Entry("preserves foreign-marked ownerless outputs",
			nodePoolCleanupCase{ownerless: true, foreignMarker: true, poolSource: "hub/workers"}),
		Entry("preserves another pool's unmarked outputs",
			nodePoolCleanupCase{poolSource: "hub/other", controllerUID: "capi-cluster"}),
		Entry("preserves another controller's unmarked outputs",
			nodePoolCleanupCase{poolSource: "hub/workers", controllerUID: "other-cluster"}),
	)

	It("keeps the source pool and original Cluster owner on every worker input", func(ctx SpecContext) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}
		pool := &hypershiftv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers", UID: "pool"},
			Spec: hypershiftv1.NodePoolSpec{
				Replicas: new(int32(2)), Management: hypershiftv1.NodePoolManagement{UpgradeType: hypershiftv1.UpgradeTypeInPlace},
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(hypershiftScheme()).
			WithObjects(ignitionInputs(pool, cluster)...).
			WithObjects(cluster).
			WithInterceptorFuncs(interceptor.Funcs{Create: func(
				ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption,
			) error {
				object.SetUID(types.UID(fmt.Sprintf("%T-%s", object, object.GetName())))
				return c.Create(ctx, object, opts...)
			}}).
			Build()
		r := &NodePoolReconciler{Client: c, APIReader: c}
		_, err := r.inputs(ctx, pool, cluster)
		Expect(err).NotTo(HaveOccurred())

		for _, output := range []client.Object{
			&agentv1.AgentMachineTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers"}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers-bootstrap"}},
			&capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers"}},
		} {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(output), output)).To(Succeed())
			Expect(nodePoolOutput(output, pool)).To(BeTrue())

			if _, workerSet := output.(*capiv1.MachineSet); workerSet {
				Expect(output.GetOwnerReferences()).To(BeEmpty(), "CAPI adopts the standalone MachineSet")
			} else {
				Expect(kube.MatchesController(output, cluster, capiv1.GroupVersion.WithKind("Cluster"))).To(BeTrue())
			}

			Expect(output.GetAnnotations()).To(Equal(map[string]string{kube.NodePoolAnnotation: "hub/workers"}))
			output.SetLabels(nil)
			Expect(c.Update(ctx, output)).To(Succeed())
		}

		By("recovering unmarked inputs before and after CAPI adopts the MachineSet")

		worker, err := r.inputs(ctx, pool, cluster)
		Expect(err).NotTo(HaveOccurred())

		originalUID := worker.UID
		Expect(originalUID).NotTo(BeEmpty())
		Expect(controllerutil.SetControllerReference(cluster, worker, c.Scheme())).To(Succeed())
		worker.Labels = nil
		Expect(c.Update(ctx, worker)).To(Succeed())
		Expect(r.pause(ctx, pool, cluster)).To(Succeed())
		worker, err = r.inputs(ctx, pool, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(worker.UID).To(Equal(originalUID))
		Expect(kube.MatchesController(worker, cluster, capiv1.GroupVersion.WithKind("Cluster"))).To(BeTrue())
		Expect(worker.Spec.Replicas).To(Equal(pool.Spec.Replicas))
	})

	It("accepts an unmarked output with its original Cluster controller", func() {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original"}}
		desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "workers-bootstrap",
			Annotations: map[string]string{kube.NodePoolAnnotation: "hub/workers"},
		}}
		Expect(controllerutil.SetControllerReference(cluster, desired, hypershiftScheme())).To(Succeed())
		current := desired.DeepCopy()
		Expect(validateOutput(current, desired)).To(Succeed())
	})

	DescribeTable("rejects other source links without changing the output", func(ctx SpecContext, key string) {
		desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "bootstrap", Labels: map[string]string{kube.ManagedByLabel: kube.ManagerName},
			Annotations: map[string]string{key: "hub/current-parent"},
		}}
		if key == kube.NodePoolAnnotation {
			parent := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "unchanged-cluster"}}
			Expect(controllerutil.SetControllerReference(parent, desired, hypershiftScheme())).To(Succeed())
		}

		current := desired.DeepCopy()
		current.UID = "existing-output"
		current.Annotations[key] = "hub/other-parent"
		before := current.DeepCopy()
		Expect(validateOutput(current, desired)).NotTo(Succeed())
		Expect(current).To(Equal(before))
	},
		Entry("HostedCluster source", kube.HostedClusterAnnotation),
		Entry("NodePool source within the current CAPI Cluster", kube.NodePoolAnnotation),
	)
})

type hostedClusterCleanupCase struct {
	namespaceSource  string
	clusterSource    string
	wantCleanupError bool
	keepFinalizer    bool
}

type nodePoolCleanupCase struct {
	managed        bool
	foreignMarker  bool
	ownerless      bool
	poolSource     string
	controllerUID  types.UID
	outputsDeleted bool
}
