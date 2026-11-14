// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capi

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// capiScheme registers the worker and provider resources used by these tests.
func capiScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(capiv1.AddToScheme(scheme)).To(Succeed())
	Expect(agentv1.AddToScheme(scheme)).To(Succeed())
	Expect(hypershiftv1.AddToScheme(scheme)).To(Succeed())

	return scheme
}

var _ = Describe("CAPI Cluster ownership", func() {
	It("preserves transition times when the control plane stays ready", func() {
		cluster := &capiv1.Cluster{}
		transition := metav1.NewTime(time.Unix(1, 0))
		conditions.Set(cluster, &capiv1.Condition{
			Type: capiv1.ControlPlaneReadyCondition, Status: corev1.ConditionTrue, LastTransitionTime: transition,
		})
		cp := &hypershiftv1.HostedControlPlane{Status: hypershiftv1.HostedControlPlaneStatus{Ready: true}}
		next := clusterStatus(cluster, nil, cp)
		Expect(conditions.Get(next, capiv1.ControlPlaneReadyCondition).LastTransitionTime).To(Equal(transition))
		Expect(clusterStatus(next, nil, cp).Status).To(Equal(next.Status))
	})

	DescribeTable("protects Cluster deletion before checking provider ownership", func(ctx SpecContext, variant string) {
		By("preparing provider references and their owners")

		scheme := capiScheme()
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "cluster", UID: "current-cluster",
				Labels: map[string]string{kube.ManagedByLabel: kube.ManagerName},
			},
			Spec: capiv1.ClusterSpec{
				InfrastructureRef: &corev1.ObjectReference{
					APIVersion: agentv1.GroupVersion.String(), Kind: "AgentCluster", Name: "infrastructure",
				},
				ControlPlaneRef: &corev1.ObjectReference{
					APIVersion: hypershiftv1.GroupVersion.String(), Kind: "HostedControlPlane", Name: "control-plane",
				},
			},
		}
		infra := &agentv1.AgentCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp",
				Name:      "infrastructure",
				UID:       "provider-infrastructure",
				Labels:    map[string]string{kube.ClusterLabel: cluster.Name},
			},
			Status: agentv1.AgentClusterStatus{Ready: true},
		}
		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp",
				Name:      "control-plane",
				UID:       "provider-control-plane",
				Labels:    map[string]string{kube.ClusterLabel: cluster.Name},
			},
			Status: hypershiftv1.HostedControlPlaneStatus{Ready: true},
		}

		Expect(controllerutil.SetControllerReference(cluster, infra, scheme)).To(Succeed())
		Expect(controllerutil.SetControllerReference(cluster, cp, scheme)).To(Succeed())

		switch variant {
		case "unmanaged":
			cluster.Labels = nil
		case "paused":
			cluster.Spec.Paused = true
		case "unsupported":
			cluster.Spec.ControlPlaneRef.APIVersion = "v1"
			cluster.Spec.ControlPlaneRef.Kind = "Service"
		case "namespace":
			cluster.Spec.InfrastructureRef.Namespace = "other"
			cluster.Spec.ControlPlaneRef.Namespace = "other"
		case "unowned":
			infra.OwnerReferences, cp.OwnerReferences = nil, nil
			infra.Labels, cp.Labels = nil, nil
		case "previous", "ordinary-previous":
			infra.OwnerReferences[0].UID = "previous-cluster"
			cp.OwnerReferences[0].UID = "previous-cluster"

			if variant == "ordinary-previous" {
				cluster.Annotations = map[string]string{kube.HostedClusterAnnotation: "hub/cluster"}

				for _, provider := range []client.Object{infra, cp} {
					provider.SetLabels(map[string]string{kube.ManagedByLabel: kube.ManagerName})
					provider.SetAnnotations(map[string]string{kube.HostedClusterAnnotation: "hub/cluster"})
					refs := provider.GetOwnerReferences()
					refs[0].Controller = new(false)
					provider.SetOwnerReferences(refs)
				}
			}
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, infra, cp).
			WithStatusSubresource(cluster).Build()
		beforeInfra, beforeCP := infra.DeepCopy(), cp.DeepCopy()

		By("reconciling native references and protecting provider identities")

		r := &ClusterReconciler{Client: c, APIReader: c}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
		if variant == "current" || variant == "unmanaged" || variant == "unowned" || variant == "paused" {
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
		}

		Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())

		Expect(cluster.Finalizers).To(ContainElement(clusterFinalizer))

		if variant == "current" || variant == "unmanaged" || variant == "unowned" {
			Expect(cluster.Status.InfrastructureReady).To(BeTrue())
			Expect(cluster.Status.ControlPlaneReady).To(BeTrue())
		}

		By("adopting unowned providers and preserving established ownership")

		Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(cp), cp)).To(Succeed())

		if variant == "unowned" {
			Expect(kube.MatchesController(infra, cluster, capiv1.GroupVersion.WithKind("Cluster"))).To(BeTrue())
			Expect(kube.MatchesController(cp, cluster, capiv1.GroupVersion.WithKind("Cluster"))).To(BeTrue())
			Expect(infra.Labels[kube.ClusterLabel]).To(Equal(cluster.Name))
			Expect(cp.Labels[kube.ClusterLabel]).To(Equal(cluster.Name))
		} else {
			Expect(infra).To(Equal(beforeInfra))
			Expect(cp).To(Equal(beforeCP))
		}
	},
		Entry("accepts unlabelled Clusters", "unmanaged"),
		Entry("protects deletion while reconciliation is paused", "paused"),
		Entry("rejects unsupported providers", "unsupported"),
		Entry("rejects references to another namespace", "namespace"),
		Entry("adopts unlabelled referenced providers", "unowned"),
		Entry("rejects providers from a previous Cluster", "previous"),
		Entry("preserves an ordinary owner UID before controller adoption", "ordinary-previous"),
		Entry("finalizes a Cluster with current providers", "current"),
	)

	It("leaves providers unadopted when deletion protection cannot be persisted", func(ctx SpecContext) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original"},
			Spec: capiv1.ClusterSpec{InfrastructureRef: &corev1.ObjectReference{
				APIVersion: agentv1.GroupVersion.String(), Kind: "AgentCluster", Name: "infrastructure",
			}},
		}
		infra := &agentv1.AgentCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "infrastructure"}}
		failure := errors.New("finalizer write interrupted")
		c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(cluster, infra).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(
					ctx context.Context, c client.WithWatch, object client.Object,
					patch client.Patch, opts ...client.PatchOption,
				) error {
					if _, protection := object.(*capiv1.Cluster); protection {
						return failure
					}

					return c.Patch(ctx, object, patch, opts...)
				},
			}).Build()
		r := &ClusterReconciler{Client: c, APIReader: c}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
		Expect(err).To(MatchError(failure))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		Expect(infra.OwnerReferences).To(BeEmpty())
		Expect(infra.Labels).To(BeEmpty())
	})

	DescribeTable(
		"preserves foreign workers and providers during Cluster deletion",
		func(ctx SpecContext, hasWorker bool) {
			scheme := capiScheme()
			cluster := &capiv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cp", Name: "cluster", UID: "current-cluster", Finalizers: []string{clusterFinalizer},
					Labels: map[string]string{kube.ManagedByLabel: kube.ManagerName},
				},
				Spec: capiv1.ClusterSpec{InfrastructureRef: &corev1.ObjectReference{
					APIVersion: agentv1.GroupVersion.String(), Kind: "AgentCluster", Name: "infrastructure",
				}},
			}
			previous := cluster.DeepCopy()
			previous.UID = "old-cluster"
			provider := &agentv1.AgentCluster{ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "infrastructure", UID: "foreign-provider",
			}}
			worker := &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "workers", UID: "foreign-worker",
				Labels: map[string]string{kube.ClusterLabel: cluster.Name},
			}}

			Expect(controllerutil.SetControllerReference(previous, provider, scheme)).To(Succeed())
			Expect(controllerutil.SetControllerReference(previous, worker, scheme)).To(Succeed())

			objects := []client.Object{cluster, provider}
			if hasWorker {
				objects = append(objects, worker)
			}

			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			beforeProvider, beforeWorker := provider.DeepCopy(), worker.DeepCopy()

			By("waiting at native descendants or provider ownership boundaries")

			Expect(c.Delete(ctx, cluster)).To(Succeed())
			r := &ClusterReconciler{Client: c, APIReader: c}

			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
			if hasWorker {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				Expect(c.Get(ctx, client.ObjectKeyFromObject(worker), worker)).To(Succeed())
				Expect(worker).To(Equal(beforeWorker))
			} else {
				Expect(err).To(HaveOccurred())
			}

			Expect(c.Get(ctx, client.ObjectKeyFromObject(provider), provider)).To(Succeed())
			Expect(provider).To(Equal(beforeProvider))
		},
		Entry("waits for native-label-selected foreign workers", true),
		Entry("rejects foreign provider deletion", false),
	)

	It("adopts pending providers and observes readiness independently with sticky initialization", func(ctx SpecContext) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "cp",
				Name:        "cluster",
				UID:         "original-cluster",
				Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			},
			Spec: capiv1.ClusterSpec{
				InfrastructureRef: &corev1.ObjectReference{
					APIVersion: agentv1.GroupVersion.String(),
					Kind:       "AgentCluster",
					Name:       "infra",
				},
				ControlPlaneRef: &corev1.ObjectReference{
					APIVersion: hypershiftv1.GroupVersion.String(),
					Kind:       "HostedControlPlane",
					Name:       "control-plane",
				},
			},
		}
		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "cp",
				Name:        "control-plane",
				UID:         "original-control-plane",
				Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			},
			Status: hypershiftv1.HostedControlPlaneStatus{Ready: true, Initialized: true},
		}
		c := fake.NewClientBuilder().
			WithScheme(capiScheme()).
			WithObjects(cluster, cp).
			WithStatusSubresource(cluster, cp, &agentv1.AgentCluster{}).
			Build()
		r := &ClusterReconciler{Client: c, APIReader: c}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
		Expect(r.clustersForProvider(ctx, cp)).To(ConsistOf(request))
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, cluster)).To(Succeed())
		Expect(cluster.Status.InfrastructureReady).To(BeFalse())
		Expect(cluster.Status.ControlPlaneReady).To(BeTrue())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(cp), cp)).To(Succeed())
		Expect(kube.MatchesController(cp, cluster, capiv1.GroupVersion.WithKind("Cluster"))).To(BeTrue())

		infra := &agentv1.AgentCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "cp",
				Name:        "infra",
				UID:         "original-infra",
				Labels:      map[string]string{kube.ManagedByLabel: kube.ManagerName},
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			},
			Spec: agentv1.AgentClusterSpec{
				ControlPlaneEndpoint: capiv1.APIEndpoint{Host: "api.example.invalid", Port: 6443},
			},
			Status: agentv1.AgentClusterStatus{Ready: true},
		}
		Expect(c.Create(ctx, infra)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, cluster)).To(Succeed())
		Expect(cluster.Spec.ControlPlaneEndpoint).To(Equal(infra.Spec.ControlPlaneEndpoint))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		infra.Status.Ready = false
		cp.Status.Ready, cp.Status.Initialized = false, false
		Expect(c.Status().Update(ctx, infra)).To(Succeed())
		Expect(c.Status().Update(ctx, cp)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, cluster)).To(Succeed())
		Expect(cluster.Status.InfrastructureReady).To(BeTrue())
		Expect(cluster.Status.ControlPlaneReady).To(BeTrue())
		Expect(conditions.IsTrue(cluster, capiv1.ControlPlaneInitializedCondition)).To(BeTrue())
		Expect(conditions.IsFalse(cluster, capiv1.InfrastructureReadyCondition)).To(BeTrue())
		Expect(conditions.IsFalse(cluster, capiv1.ControlPlaneReadyCondition)).To(BeTrue())
	})

	DescribeTable("waits at each native deletion boundary", func(ctx SpecContext, blocked string) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:         "cp",
				Name:              "cluster",
				UID:               "original",
				Labels:            map[string]string{kube.ManagedByLabel: kube.ManagerName},
				Finalizers:        []string{clusterFinalizer},
				DeletionTimestamp: new(metav1.Now()),
			},
			Spec: capiv1.ClusterSpec{
				InfrastructureRef: &corev1.ObjectReference{
					APIVersion: agentv1.GroupVersion.String(),
					Kind:       "AgentCluster",
					Name:       "infra",
				},
				ControlPlaneRef: &corev1.ObjectReference{
					APIVersion: hypershiftv1.GroupVersion.String(),
					Kind:       "HostedControlPlane",
					Name:       "control-plane",
				},
			},
		}
		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  "cp",
				Name:       "control-plane",
				UID:        "cp",
				Finalizers: []string{"provider.example/cleanup"},
			},
		}
		infra := &agentv1.AgentCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "infra", UID: "infra"}}

		Expect(controllerutil.SetControllerReference(cluster, cp, capiScheme())).To(Succeed())
		Expect(controllerutil.SetControllerReference(cluster, infra, capiScheme())).To(Succeed())
		objects := []client.Object{cluster, cp, infra}
		set := &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  "cp",
				Name:       "workers",
				UID:        "set",
				Labels:     map[string]string{kube.ClusterLabel: cluster.Name},
				Finalizers: []string{setFinalizer},
			},
		}
		Expect(controllerutil.SetOwnerReference(cluster, set, capiScheme())).To(Succeed())

		if blocked == "workers" || blocked == "foreign-workers" {
			if blocked == "foreign-workers" {
				set.OwnerReferences[0].UID = "previous-cluster"
			}

			objects = append(objects, set)
		}

		machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: "standalone", UID: "original-machine",
			Labels: map[string]string{kube.ClusterLabel: cluster.Name}, Finalizers: []string{machineFinalizer},
		}}
		Expect(controllerutil.SetOwnerReference(cluster, machine, capiScheme())).To(Succeed())

		if blocked == "machine" || blocked == "foreign-machine" || blocked == "control-plane-machine" {
			if blocked == "foreign-machine" {
				machine.OwnerReferences[0].UID = "previous-cluster"
			}

			if blocked == "control-plane-machine" {
				machine.Labels[capiv1.MachineControlPlaneLabel] = ""
			}

			objects = append(objects, machine)
		}

		c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(objects...).Build()

		r := &ClusterReconciler{Client: c, APIReader: c}
		for range 2 {
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		Expect(infra.DeletionTimestamp).To(BeNil())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(cp), cp)).To(Succeed())

		if blocked != "control-plane" && blocked != "control-plane-machine" {
			Expect(cp.DeletionTimestamp).To(BeNil())

			switch blocked {
			case "workers":
				Expect(c.Get(ctx, client.ObjectKeyFromObject(set), set)).To(Succeed())
				Expect(set.DeletionTimestamp).NotTo(BeNil())
			case "machine":
				Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
				Expect(machine.DeletionTimestamp).NotTo(BeNil())
			}
		} else {
			Expect(cp.DeletionTimestamp).NotTo(BeNil())
		}

		if blocked == "foreign-workers" {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(set), set)).To(Succeed())
			Expect(set.DeletionTimestamp).To(BeNil())
		}

		if blocked == "foreign-machine" || blocked == "control-plane-machine" {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(machine.UID).To(BeEquivalentTo("original-machine"))
			Expect(machine.DeletionTimestamp).To(BeNil())
		}

		Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
		Expect(cluster.Finalizers).To(ContainElement(clusterFinalizer))
	}, Entry("workers precede control plane", "workers"),
		Entry("ordinary-owned Machines precede control plane", "machine"),
		Entry("previous Cluster Machines remain untouched and hold providers", "foreign-machine"),
		Entry("previous Cluster MachineSets remain untouched and hold providers", "foreign-workers"),
		Entry("control plane precedes infrastructure", "control-plane"),
		Entry("provider-managed control-plane Machines remain for their provider", "control-plane-machine"))
})
