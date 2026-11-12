// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assisted

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// deploymentInputs represents CAP-Agent's independently persisted creation inputs.
func deploymentInputs() (*hivev1.ClusterDeployment, *installv1.AgentClusterInstall) {
	deployment := &hivev1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp",
			Name:      "deployment",
			UID:       "original-deployment",
			Labels:    map[string]string{kube.ManagedByLabel: kube.ManagerName},
		},
		Spec: hivev1.ClusterDeploymentSpec{
			Installed: true,
			ClusterInstallRef: &hivev1.ClusterInstallLocalReference{
				Group:   installv1.GroupVersion.Group,
				Version: installv1.GroupVersion.Version,
				Kind:    "AgentClusterInstall",
				Name:    "installation",
			},
		},
	}
	installation := &installv1.AgentClusterInstall{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp",
			Name:      "installation",
			UID:       "original-installation",
			Labels:    map[string]string{kube.ManagedByLabel: kube.ManagerName},
		},
		Spec: installv1.AgentClusterInstallSpec{
			ClusterDeploymentRef: corev1.LocalObjectReference{Name: deployment.Name},
		},
	}

	return deployment, installation
}

var _ = Describe("Assisted installation lifecycle", func() {
	DescribeTable(
		"uses the InfraEnv's native delete-versus-unbind policy",
		func(ctx SpecContext, variant string, remove bool) {
			deployment, _ := deploymentInputs()

			deployment.Spec.PreserveOnDelete = variant == "preserved pool" || variant == "unreadable InfraEnv"
			infra := &assistedv1.InfraEnv{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "infra", UID: "original-infra"}}
			if variant == "dedicated" || variant == "another deployment" {
				infra.Spec.ClusterRef = &assistedv1.ClusterReference{Namespace: deployment.Namespace, Name: deployment.Name}
				if variant == "another deployment" {
					infra.Spec.ClusterRef.Name = "other"
				}
			}

			agent := &assistedv1.Agent{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "hub", Name: "host", UID: "original-agent", Finalizers: []string{"agent.example/cleanup"},
					Labels: map[string]string{assistedv1.InfraEnvNameLabel: infra.Name, "agentMachineRef": "worker"},
				},
				Spec: assistedv1.AgentSpec{
					ClusterDeploymentName: &assistedv1.ClusterReference{Namespace: deployment.Namespace, Name: deployment.Name},
					MachineConfigPool:     "ignition",
				},
			}
			if variant == "missing label" {
				delete(agent.Labels, assistedv1.InfraEnvNameLabel)
			}

			if variant == "missing InfraEnv" {
				infra.Name = "other"
			}

			foreign := agent.DeepCopy()
			foreign.Namespace, foreign.UID = "other-hub", "foreign-agent"
			c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(deployment, infra, agent, foreign).
				WithInterceptorFuncs(interceptor.Funcs{Get: func(
					ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption,
				) error {
					if _, discovery := object.(*assistedv1.InfraEnv); discovery && variant == "unreadable InfraEnv" {
						return errors.New("InfraEnv lookup unavailable")
					}

					return c.Get(ctx, key, object, options...)
				}}).Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
			beforeForeign := foreign.DeepCopy()
			r := &DeploymentReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}

			for range 2 {
				Expect(r.unbindAgents(ctx, deployment)).To(Succeed())
				Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
				Expect(agent.UID).To(BeEquivalentTo("original-agent"))
				Expect(agent.DeletionTimestamp != nil).To(Equal(remove))
				Expect(agent.Spec.ClusterDeploymentName == nil).To(Equal(!remove))
				Expect(agent.Spec.MachineConfigPool).To(Equal("ignition"))
				Expect(agent.Labels).To(HaveKeyWithValue("agentMachineRef", "worker"))
				Expect(c.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
				Expect(foreign).To(Equal(beforeForeign))
			}
		},
		Entry("deployment-specific discovery", "dedicated", true),
		Entry("a discovery environment for another deployment", "another deployment", false),
		Entry("late binding with preservation", "preserved pool", true),
		Entry("reusable late-binding host", "pool", false),
		Entry("missing InfraEnv label", "missing label", false),
		Entry("missing InfraEnv", "missing InfraEnv", false),
		Entry("failed InfraEnv lookup follows native unbind behavior", "unreadable InfraEnv", false),
	)

	It("preserves Agents when only a foreign installation finalizer remains", func(ctx SpecContext) {
		deployment, installation := deploymentInputs()
		installation.Finalizers = []string{"foreign.example/cleanup"}
		installation.DeletionTimestamp = new(metav1.Now())
		agent := &assistedv1.Agent{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "host", UID: "original-agent"},
			Spec: assistedv1.AgentSpec{ClusterDeploymentName: &assistedv1.ClusterReference{
				Namespace: deployment.Namespace, Name: deployment.Name,
			}},
		}
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(deployment, installation, agent).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		beforeInstall, beforeAgent := installation.DeepCopy(), agent.DeepCopy()
		r := &DeploymentReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(deployment)})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(installation).To(Equal(beforeInstall))
		Expect(agent).To(Equal(beforeAgent))
	})

	It("adopts ordinary installation ownership and recovers an interrupted finalizer write", func(ctx SpecContext) {
		deployment, installation := deploymentInputs()
		deployment.Labels, installation.Labels = nil, nil
		failure, interrupted := errors.New("interrupted finalizer write"), true
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(deployment, installation).
			WithInterceptorFuncs(interceptor.Funcs{Patch: func(
				ctx context.Context,
				c client.WithWatch,
				object client.Object,
				patch client.Patch,
				opts ...client.PatchOption,
			) error {
				if _, install := object.(*installv1.AgentClusterInstall); install && interrupted &&
					controllerutil.ContainsFinalizer(object, installationFinalizer) {
					interrupted = false
					return failure
				}

				return c.Patch(ctx, object, patch, opts...)
			}}).
			Build()
		r := &DeploymentReconciler{Client: c, APIReader: c}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(deployment)}
		_, err := r.Reconcile(ctx, request)
		Expect(errors.Is(err, failure)).To(BeTrue())
		Expect(c.Get(ctx, request.NamespacedName, deployment)).To(Succeed())
		Expect(deployment.Finalizers).NotTo(ContainElement(deploymentFinalizer))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(installation.Finalizers).NotTo(ContainElement(installationFinalizer))
		Expect(installation.OwnerReferences).To(BeEmpty())

		restarted := &DeploymentReconciler{Client: c, APIReader: c}
		_, err = restarted.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(
			kube.MatchesOwner(installation, deployment, hivev1.SchemeGroupVersion.WithKind("ClusterDeployment")),
		).To(BeTrue())
		Expect(metav1.GetControllerOf(installation)).To(BeNil())
		Expect(installation.Finalizers).To(ContainElement(installationFinalizer))
		before := installation.DeepCopy()
		_, err = restarted.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(installation).To(Equal(before))
		Expect(deploymentForInstallation(ctx, installation)).To(ConsistOf(request))
	})

	It("unbinds scoped Agents before removing installation and deployment barriers", func(ctx SpecContext) {
		deployment, installation := deploymentInputs()
		deployment.Labels, installation.Labels = nil, nil
		deployment.Finalizers = []string{"foreign.example/cleanup"}
		agent := &assistedv1.Agent{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub",
				Name:      "host",
				UID:       "original-host",
				Labels:    map[string]string{"agentMachineRef": "worker"},
			},
			Spec: assistedv1.AgentSpec{
				ClusterDeploymentName: &assistedv1.ClusterReference{Namespace: deployment.Namespace, Name: deployment.Name},
				MachineConfigPool:     "ignition",
			},
		}
		foreign := agent.DeepCopy()
		foreign.Namespace, foreign.UID = "other-hub", "foreign-host"
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(deployment, installation, agent, foreign).Build()
		r := &DeploymentReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(deployment)}
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, deployment)).To(Succeed())
		Expect(c.Delete(ctx, deployment)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(installation.DeletionTimestamp).NotTo(BeNil())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.Spec.ClusterDeploymentName).NotTo(BeNil())

		for range 2 {
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.Spec.ClusterDeploymentName).To(BeNil())
		Expect(agent.Spec.MachineConfigPool).To(Equal("ignition"))
		Expect(agent.Labels).To(HaveKeyWithValue("agentMachineRef", "worker"))
		Expect(agent.UID).To(Equal(metav1.ObjectMeta{UID: "original-host"}.UID))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
		Expect(foreign.Spec.ClusterDeploymentName).NotTo(BeNil())
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(installation), installation))).To(BeTrue())
		Expect(c.Get(ctx, request.NamespacedName, deployment)).To(Succeed())
		Expect(deployment.Finalizers).To(ConsistOf("foreign.example/cleanup"))
	})

	DescribeTable("ignores unsupported installation references", func(ctx SpecContext, variant string) {
		deployment, installation := deploymentInputs()

		switch variant {
		case "missing":
			deployment.Spec.ClusterInstallRef = nil
		case "group":
			deployment.Spec.ClusterInstallRef.Group = "other.example"
		case "kind":
			deployment.Spec.ClusterInstallRef.Kind = "OtherInstall"
		}

		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(deployment, installation).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		beforeDeployment, beforeInstallation := deployment.DeepCopy(), installation.DeepCopy()
		r := &DeploymentReconciler{Client: c, APIReader: c}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(deployment)})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(deployment).To(Equal(beforeDeployment))
		Expect(installation).To(Equal(beforeInstallation))
	},
		Entry("absent reference", "missing"),
		Entry("another API group", "group"),
		Entry("another install kind", "kind"),
	)

	It("cleans up after ownership registration is interrupted and the deployment starts deleting", func(ctx SpecContext) {
		deployment, installation := deploymentInputs()
		deployment.Finalizers = []string{"foreign.example/cleanup"}
		agent := &assistedv1.Agent{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub", Name: "host", Labels: map[string]string{"agentMachineRef": "worker"},
			},
			Spec: assistedv1.AgentSpec{
				ClusterDeploymentName: &assistedv1.ClusterReference{Namespace: deployment.Namespace, Name: deployment.Name},
				MachineConfigPool:     "ignition",
			},
		}
		foreign := agent.DeepCopy()
		foreign.Namespace = "other-hub"
		failure := errors.New("interrupted owner write")
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(deployment, installation, agent, foreign).
			WithInterceptorFuncs(interceptor.Funcs{Patch: func(
				ctx context.Context, c client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption,
			) error {
				if _, install := object.(*installv1.AgentClusterInstall); install && len(object.GetOwnerReferences()) > 0 {
					return failure
				}

				return c.Patch(ctx, object, patch, opts...)
			}}).Build()
		r := &DeploymentReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(deployment)}

		By("retaining both cleanup barriers when the ownership write fails")

		_, err := r.Reconcile(ctx, request)
		Expect(err).To(MatchError(failure))
		Expect(c.Get(ctx, request.NamespacedName, deployment)).To(Succeed())
		Expect(deployment.Finalizers).To(ConsistOf(deploymentFinalizer, "foreign.example/cleanup"))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(installation.Finalizers).To(ContainElement(installationFinalizer))
		Expect(installation.OwnerReferences).To(BeEmpty())

		By("deleting the installation through its protected cleanup path after restart")

		Expect(c.Delete(ctx, deployment)).To(Succeed())
		restarted := &DeploymentReconciler{Client: c, APIReader: c, Scope: r.Scope}
		_, err = restarted.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(installation.DeletionTimestamp).NotTo(BeNil())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.Spec.ClusterDeploymentName).NotTo(BeNil())

		for range 2 {
			_, err = restarted.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.Spec.ClusterDeploymentName).To(BeNil())
		Expect(agent.Spec.MachineConfigPool).To(Equal("ignition"))
		Expect(agent.Labels).To(HaveKeyWithValue("agentMachineRef", "worker"))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
		Expect(foreign.Spec.ClusterDeploymentName).NotTo(BeNil())
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(installation), installation))).To(BeTrue())
		Expect(c.Get(ctx, request.NamespacedName, deployment)).To(Succeed())
		Expect(deployment.Finalizers).To(ConsistOf("foreign.example/cleanup"))
	})

	It("rejects an installation owned by another deployment incarnation", func(ctx SpecContext) {
		deployment, installation := deploymentInputs()
		old := deployment.DeepCopy()
		old.UID = "previous-deployment"
		Expect(controllerutil.SetOwnerReference(old, installation, agentScheme())).To(Succeed())
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(deployment, installation).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		before := installation.DeepCopy()
		r := &DeploymentReconciler{Client: c, APIReader: c}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(deployment)})
		Expect(err).To(MatchError(ContainSubstring("another deployment incarnation")))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(installation).To(Equal(before))
	})
})
