// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capagent

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// capagentScheme registers worker discovery and installation records for fake clients.
func capagentScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, capiv1.AddToScheme, agentv1.AddToScheme,
		assistedv1.AddToScheme, hypershiftv1.AddToScheme, hivev1.AddToScheme, installv1.AddToScheme,
	} {
		Expect(add(scheme)).To(Succeed())
	}

	return scheme
}

var _ = Describe("Agent installation record ownership", func() {
	DescribeTable(
		"creates ordinary deployment ownership and leaves installation adoption to Assisted",
		func(ctx SpecContext, marker string) {
			_, _, cluster, cp := agentMachineRecoveryInputs()
			cp.Spec.DNS.BaseDomain = "example.invalid"
			cp.Status.KubeConfig = &hypershiftv1.KubeconfigSecretRef{Name: "access"}
			infra := &agentv1.AgentCluster{ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "cluster", UID: "agent-cluster",
			}}
			Expect(controllerutil.SetControllerReference(cluster, infra, capagentScheme())).To(Succeed())
			// Model API-assigned identities so each owner link uses a stored resource UID.
			created := 0
			c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(cluster, cp, infra).
				WithStatusSubresource(infra).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption) error {
						created++
						object.SetUID(types.UID(fmt.Sprintf("created-%d", created)))
						return c.Create(ctx, object, opts...)
					},
				}).Build()
			r := &AgentClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)})
			Expect(err).NotTo(HaveOccurred())

			deployment := &hivev1.ClusterDeployment{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), deployment)).To(Succeed())
			Expect(deployment.UID).NotTo(BeEmpty())
			Expect(kube.MatchesOwner(deployment, infra, agentv1.GroupVersion.WithKind("AgentCluster"))).To(BeTrue())

			installation := &installv1.AgentClusterInstall{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), installation)).To(Succeed())
			Expect(installation.UID).NotTo(BeEmpty())
			Expect(installation.UID).NotTo(Equal(deployment.UID))
			Expect(metav1.GetControllerOf(deployment)).To(BeNil())
			Expect(installation.OwnerReferences).To(BeEmpty())

			By("recovering pending Assisted ownership through the native deployment reference")

			installation.Labels = map[string]string{kube.ManagedByLabel: marker}
			Expect(c.Update(ctx, installation)).To(Succeed())
			original := installation.DeepCopy()
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)})
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
			Expect(installation).To(Equal(original))

			By("recovering a deleted installation through its native input mapping")

			requests := r.clustersForInput(ctx, installation)
			Expect(requests).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)}))
			Expect(c.Delete(ctx, installation)).To(Succeed())
			_, err = r.Reconcile(ctx, requests[0])
			Expect(err).NotTo(HaveOccurred())

			recreated := &installv1.AgentClusterInstall{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), recreated)).To(Succeed())
			Expect(recreated.UID).NotTo(Equal(installation.UID))
			Expect(recreated.OwnerReferences).To(BeEmpty())
		},
		Entry("unmarked installation", ""),
		Entry("installation with an unrelated marker", "other-provider"),
	)

	It("recovers the native deployment identity and installation inputs", func(ctx SpecContext) {
		By("recovering a lost status reference through the native association")

		_, _, cluster, cp := agentMachineRecoveryInputs()
		cp.Status.KubeConfig = &hypershiftv1.KubeconfigSecretRef{Name: "access"}
		infra := &agentv1.AgentCluster{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: "infrastructure", UID: "agent-cluster",
		}}
		Expect(controllerutil.SetControllerReference(cluster, infra, capagentScheme())).To(Succeed())

		deployment := &hivev1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{
			Namespace: infra.Namespace, Name: "recorded-deployment", UID: "deployment",
			Labels: map[string]string{"agentClusterRef": infra.Name},
		}}
		Expect(controllerutil.SetOwnerReference(infra, deployment, capagentScheme())).To(Succeed())
		unrelated := &hivev1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{
			Namespace: infra.Namespace, Name: infra.Name, UID: "unrelated",
			Labels: map[string]string{"agentClusterRef": "other"},
		}}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(cluster, cp, infra, deployment, unrelated).
			WithStatusSubresource(infra).Build()
		r := &AgentClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)}
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		installation := &installv1.AgentClusterInstall{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(deployment), installation)).To(Succeed())
		Expect(installation.Spec.ClusterDeploymentRef.Name).To(Equal(deployment.Name))
		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		Expect(infra.Status.ClusterDeploymentRef.Name).To(Equal(deployment.Name))
		Expect(infra.Status.Ready).To(BeFalse(), "the control-plane endpoint is still missing")

		By("recovering the ownerless association after pause and status loss")

		infra.Annotations = map[string]string{capiv1.PausedAnnotation: "true"}
		Expect(c.Update(ctx, infra)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
		Expect(deployment.OwnerReferences).To(BeEmpty())

		infra.Status.ClusterDeploymentRef = agentv1.ClusterDeploymentReference{}
		Expect(c.Status().Update(ctx, infra)).To(Succeed())
		delete(infra.Annotations, capiv1.PausedAnnotation)
		Expect(c.Update(ctx, infra)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
		Expect(kube.MatchesOwner(deployment, infra, agentv1.GroupVersion.WithKind("AgentCluster"))).To(BeTrue())

		By("repairing a missing label through the recorded deployment reference")

		deployment.Labels = nil
		Expect(c.Update(ctx, deployment)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
		Expect(deployment.UID).To(Equal(types.UID("deployment")))
		Expect(kube.MatchesOwner(deployment, infra, agentv1.GroupVersion.WithKind("AgentCluster"))).To(BeTrue())
		Expect(deployment.Labels["agentClusterRef"]).To(Equal(infra.Name))

		By("refusing an unrelated deployment when the recorded target disappears")

		Expect(c.Delete(ctx, deployment)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		var deployments hivev1.ClusterDeploymentList

		Expect(c.List(ctx, &deployments)).To(Succeed())
		Expect(deployments.Items).To(HaveLen(1), "a recorded reference must not fall back to another name")
		Expect(deployments.Items[0].UID).To(Equal(unrelated.UID))
		Expect(deployments.Items[0].Labels).To(Equal(unrelated.Labels))
		Expect(deployments.Items[0].OwnerReferences).To(BeEmpty())
	})

	It("releases an unlabelled AgentCluster after adding its finalizer", func(ctx SpecContext) {
		_, _, cluster, cp := agentMachineRecoveryInputs()
		infra := &agentv1.AgentCluster{ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "cluster", UID: "agent-cluster",
		}}
		Expect(controllerutil.SetControllerReference(cluster, infra, capagentScheme())).To(Succeed())
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(cluster, cp, infra).Build()
		r := &AgentClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)}

		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		Expect(infra.Labels).NotTo(HaveKey(kube.ManagedByLabel))
		Expect(infra.Finalizers).To(ContainElement(agentClusterFinalizer))

		Expect(c.Delete(ctx, infra)).To(Succeed())
		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		Expect(infra.DeletionTimestamp).NotTo(BeNil())

		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(c.Get(ctx, request.NamespacedName, infra))).To(BeTrue())
	})

	DescribeTable("orphaning a paused AgentCluster before removing its finalizer", func(ctx SpecContext, variant string) {
		owned := variant != "previous"
		_, _, cluster, cp := agentMachineRecoveryInputs()
		infra := &agentv1.AgentCluster{ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "cluster", UID: "agent-cluster",
		}}
		Expect(controllerutil.SetControllerReference(cluster, infra, capagentScheme())).To(Succeed())

		owner := infra.DeepCopy()
		if !owned {
			owner.UID = "previous-agent-cluster"
		}

		deployment := &hivev1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{
			Namespace: infra.Namespace, Name: "recorded-deployment",
		}}
		infra.Status.ClusterDeploymentRef = agentv1.ClusterDeploymentReference{
			Namespace: deployment.Namespace,
			Name:      deployment.Name,
		}
		Expect(controllerutil.SetOwnerReference(owner, deployment, capagentScheme())).To(Succeed())

		stale := deployment.OwnerReferences[0]
		stale.UID = "previous-agent-cluster"
		if variant == "mixed" {
			deployment.OwnerReferences = append([]metav1.OwnerReference{stale}, deployment.OwnerReferences...)
		}

		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(cluster, cp, infra, deployment).Build()
		r := &AgentClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)}

		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		Expect(infra.Finalizers).To(ContainElement(agentClusterFinalizer))
		before := infra.DeepCopy()
		infra.Annotations = map[string]string{capiv1.PausedAnnotation: "true"}
		Expect(c.Patch(ctx, infra, client.MergeFrom(before))).To(Succeed())
		Expect(c.Delete(ctx, infra)).To(Succeed())

		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(c.Get(ctx, request.NamespacedName, infra))).To(BeTrue())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())

		if variant == "mixed" {
			Expect(deployment.OwnerReferences).To(ConsistOf(stale))
		} else if owned {
			Expect(deployment.OwnerReferences).To(BeEmpty())
		} else {
			Expect(kube.MatchesOwner(deployment, owner, agentv1.GroupVersion.WithKind("AgentCluster"))).To(BeTrue())
		}
	},
		Entry("the original deployment", "current"),
		Entry("a deployment owned by a previous incarnation", "previous"),
		Entry("stale and current same-name owners", "mixed"),
	)

	DescribeTable(
		"validates control-plane references before using provider inputs",
		func(ctx SpecContext, variant string) {
			_, _, cluster, cp := agentMachineRecoveryInputs()

			switch variant {
			case "default-namespace":
				cluster.Spec.ControlPlaneRef.Namespace = ""
			case "foreign-namespace":
				cluster.Spec.ControlPlaneRef.Namespace = "other"
			case "version":
				cluster.Spec.ControlPlaneRef.APIVersion = "other/v1"
			case "kind":
				cluster.Spec.ControlPlaneRef.Kind = "Other"
			case "missing-controller":
				cp.OwnerReferences = nil
			case "replaced":
				cluster.UID = "replacement"
			case "owner-name":
				cp.OwnerReferences[0].Name = "other"
			case "owner-version":
				cp.OwnerReferences[0].APIVersion = "other/v1"
			}

			c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(cluster, cp).Build()

			_, observed, err := controlPlane(ctx, c, cluster.Namespace, cluster.Name)
			if variant == "default-namespace" {
				Expect(err).NotTo(HaveOccurred())
				Expect(observed).NotTo(BeNil())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(observed).To(BeNil())
			}
		},
		Entry("an omitted namespace defaults to the Cluster namespace", "default-namespace"),
		Entry("a foreign namespace", "foreign-namespace"),
		Entry("a different API version", "version"),
		Entry("a different kind", "kind"),
		Entry("a missing controller", "missing-controller"),
		Entry("a replaced Cluster", "replaced"),
		Entry("a controller with another name", "owner-name"),
		Entry("a controller with another API version", "owner-version"),
	)
})
