// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assisted

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// agentScheme registers the discovery and installation records used by these tests.
func agentScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(assistedv1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(installv1.AddToScheme(scheme)).To(Succeed())
	Expect(hivev1.AddToScheme(scheme)).To(Succeed())

	return scheme
}

// installingAgent supplies an approved host with inventory and an installed deployment.
func installingAgent() (*assistedv1.Agent, client.WithWatch) {
	infra := &assistedv1.InfraEnv{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "infra", UID: "original-infraenv"}}
	agent := &assistedv1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "hub",
			Name:      "worker",
			UID:       "original-agent",
			Labels:    map[string]string{kube.InfraEnvLabel: infra.Name},
			OwnerReferences: []metav1.OwnerReference{
				{APIVersion: assistedv1.GroupVersion.String(), Kind: "InfraEnv", Name: infra.Name, UID: infra.UID},
			},
		},
		Spec: assistedv1.AgentSpec{
			Approved:              true,
			ClusterDeploymentName: &assistedv1.ClusterReference{Namespace: "control-plane", Name: "deployment"},
		},
	}
	agent.Status.DebugInfo.State = "installing"
	agent.Status.Inventory.Interfaces = []assistedv1.HostInterface{{Name: "eth0"}}
	deployment := &hivev1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "control-plane", Name: "deployment", UID: "original-deployment"},
		Spec: hivev1.ClusterDeploymentSpec{
			Installed: true,
			ClusterInstallRef: &hivev1.ClusterInstallLocalReference{
				Group:   installv1.GroupVersion.Group,
				Version: installv1.GroupVersion.Version,
				Kind:    "AgentClusterInstall",
				Name:    "installation",
			},
			PullSecretRef:   &corev1.LocalObjectReference{Name: "pull-secret"},
			ClusterMetadata: &hivev1.ClusterMetadata{AdminKubeconfigSecretRef: corev1.LocalObjectReference{Name: "access"}},
		},
	}
	installation := &installv1.AgentClusterInstall{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "control-plane",
			Name:      "installation",
			UID:       "original-installation",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: hivev1.SchemeGroupVersion.String(),
					Kind:       "ClusterDeployment",
					Name:       deployment.Name,
					UID:        deployment.UID,
				},
			},
		},
		Spec: installv1.AgentClusterInstallSpec{
			ClusterDeploymentRef: corev1.LocalObjectReference{Name: deployment.Name},
		},
	}
	c := fake.NewClientBuilder().WithScheme(agentScheme()).
		WithObjects(infra, agent, deployment, installation, discoveryPullSecret(deployment.Namespace),
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: deployment.Namespace, Name: "access"},
				Data:       map[string][]byte{"kubeconfig": []byte("synthetic guest access")},
			}).
		WithStatusSubresource(agent).Build()

	return agent, c
}

var _ = Describe("Assisted Agent installation", func() {
	DescribeTable("reports a missing deployment without releasing the Agent",
		func(ctx SpecContext, restoreDeployment bool) {
			agent, c := installingAgent()
			agent.Status.DebugInfo.State = "known-unbound"
			conditionsv1.SetStatusConditionNoHeartbeat(&agent.Status.Conditions, conditionsv1.Condition{
				Type: "ExternalObservation", Status: corev1.ConditionTrue, Reason: "Preserved", Message: "keep this observation",
			})
			Expect(c.Status().Update(ctx, agent)).To(Succeed())
			before := agent.DeepCopy()
			ref := agent.Spec.ClusterDeploymentName
			deployment := &hivev1.ClusterDeployment{}
			key := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
			Expect(c.Get(ctx, key, deployment)).To(Succeed())
			Expect(c.Delete(ctx, deployment)).To(Succeed())
			lookupErr := c.Get(ctx, key, &hivev1.ClusterDeployment{})
			Expect(apierrors.IsNotFound(lookupErr)).To(BeTrue())

			r := &AgentReconciler{
				Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
				Installation: func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
					Fail("a missing deployment must not select an installation outcome")
					return "", nil
				},
			}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)}
			for range 2 {
				result, err := r.Reconcile(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(10 * time.Second))
				Expect(c.Get(ctx, request.NamespacedName, agent)).To(Succeed())
				Expect(agent.Spec).To(Equal(before.Spec))
				Expect(agent.UID).To(Equal(before.UID))
				Expect(agent.Labels).To(Equal(before.Labels))
				Expect(agent.OwnerReferences).To(Equal(before.OwnerReferences))
				Expect(agent.Status.Inventory).To(Equal(before.Status.Inventory))
				Expect(agent.Status.DebugInfo.State).To(Equal("known-unbound"))
				sync := conditionsv1.FindStatusCondition(agent.Status.Conditions, assistedv1.SpecSyncedCondition)
				Expect(sync.Status).To(Equal(corev1.ConditionFalse))
				Expect(sync.Reason).To(Equal(assistedv1.BackendErrorReason))
				Expect(sync.Message).To(Equal(assistedv1.BackendErrorMsg + " " + fmt.Sprintf(
					"failed to get clusterDeployment with name %s in namespace %s: %s", ref.Name, ref.Namespace, lookupErr,
				)))
				Expect(conditionsv1.IsStatusConditionTrue(agent.Status.Conditions, assistedv1.ConnectedCondition)).To(BeTrue())
				Expect(conditionsv1.FindStatusCondition(agent.Status.Conditions, "ExternalObservation")).
					To(Equal(conditionsv1.FindStatusCondition(before.Status.Conditions, "ExternalObservation")))
			}

			stable := agent.DeepCopy()
			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, request.NamespacedName, agent)).To(Succeed())
			Expect(agent).To(Equal(stable))

			if restoreDeployment {
				deployment.ResourceVersion = ""
				deployment.UID = "replacement-deployment"
				Expect(c.Create(ctx, deployment)).To(Succeed())

				installation := &installv1.AgentClusterInstall{}
				Expect(c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: "installation"}, installation)).To(Succeed())
				installation.OwnerReferences[0].UID = deployment.UID
				Expect(c.Update(ctx, installation)).To(Succeed())
			} else {
				agent.Spec.ClusterDeploymentName = nil
				Expect(c.Update(ctx, agent)).To(Succeed())
			}

			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, request.NamespacedName, agent)).To(Succeed())
			Expect(conditionsv1.IsStatusConditionTrue(agent.Status.Conditions, assistedv1.SpecSyncedCondition)).To(BeTrue())

			if restoreDeployment {
				Expect(agent.Status.DebugInfo.State).To(Equal("binding"))
			} else {
				Expect(agent.Status.DebugInfo.State).To(Equal("known-unbound"))
				Expect(agent.Spec.ClusterDeploymentName).To(BeNil())
			}
		},
		Entry("until the deployment appears", true),
		Entry("until its caller clears the reference", false),
	)

	DescribeTable("classifies deployment lookup failures as backend errors", func(ctx SpecContext, failure error) {
		agent, c := installingAgent()
		reader := interceptor.NewClient(c, interceptor.Funcs{Get: func(
			ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption,
		) error {
			if _, deployment := obj.(*hivev1.ClusterDeployment); deployment {
				return failure
			}

			return c.Get(ctx, key, obj, opts...)
		}})
		r := &AgentReconciler{Client: c, APIReader: reader, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(10 * time.Second))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		sync := conditionsv1.FindStatusCondition(agent.Status.Conditions, assistedv1.SpecSyncedCondition)
		Expect(sync.Status).To(Equal(corev1.ConditionFalse))
		Expect(sync.Reason).To(Equal(assistedv1.BackendErrorReason))
		Expect(sync.Message).To(HavePrefix(assistedv1.BackendErrorMsg + " failed to get clusterDeployment"))
		Expect(sync.Message).To(HaveSuffix(failure.Error()))
		Expect(agent.Status.DebugInfo.State).To(Equal("installing"))
		Expect(agent.Spec.ClusterDeploymentName).NotTo(BeNil())
	}, Entry("forbidden", apierrors.NewForbidden(
		schema.GroupResource{Group: hivev1.SchemeGroupVersion.Group, Resource: "clusterdeployments"},
		"deployment", errors.New("denied"))),
		Entry("unavailable", apierrors.NewServiceUnavailable("deployment read unavailable")))

	It("advances native discovery and binding without consulting installation settings", func(ctx SpecContext) {
		agent, c := installingAgent()
		agent.Status.DebugInfo.State = ""
		Expect(c.Status().Update(ctx, agent)).To(Succeed())

		r := &AgentReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			Installation: func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
				Fail("discovery and binding must not consult installation observations")
				return "", nil
			},
		}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)}
		for _, state := range []string{"binding", "known", "installing"} {
			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, request.NamespacedName, agent)).To(Succeed())
			Expect(agent.Status.DebugInfo.State).To(Equal(state))
			Expect(conditionsv1.IsStatusConditionTrue(agent.Status.Conditions, assistedv1.ConnectedCondition)).To(BeTrue())
		}

		failure := errors.New("installation backend unavailable")
		r.Installation = func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
			return "", failure
		}
		_, err := r.Reconcile(ctx, request)
		Expect(errors.Is(err, failure)).To(BeTrue())
		Expect(c.Get(ctx, request.NamespacedName, agent)).To(Succeed())
		Expect(agent.Status.DebugInfo.State).To(Equal("installing"))
		Expect(conditionsv1.IsStatusConditionFalse(agent.Status.Conditions, assistedv1.SpecSyncedCondition)).To(BeTrue())
	})

	DescribeTable("keeps pre-installation unbinding recoverable", func(ctx SpecContext, initial string, recoverable bool) {
		agent, c := installingAgent()
		agent.Spec.Approved = false
		agent.Spec.ClusterDeploymentName = nil
		Expect(c.Update(ctx, agent)).To(Succeed())
		agent.Status.DebugInfo.State = initial
		Expect(c.Status().Update(ctx, agent)).To(Succeed())

		r := &AgentReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			Installation: func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
				return InstallationHeld, nil
			},
		}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)}
		for range 2 {
			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(c.Get(ctx, request.NamespacedName, agent)).To(Succeed())

		if recoverable {
			Expect(agent.Status.DebugInfo.State).To(Equal("known-unbound"))
		} else {
			Expect(agent.Status.DebugInfo.State).To(Equal("unbinding-pending-user-action"))
		}

		agent.Spec.ClusterDeploymentName = &assistedv1.ClusterReference{Namespace: "control-plane", Name: "deployment"}
		Expect(c.Update(ctx, agent)).To(Succeed())

		for range 2 {
			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(c.Get(ctx, request.NamespacedName, agent)).To(Succeed())

		if recoverable {
			Expect(agent.Status.DebugInfo.State).To(Equal("known"))
		} else {
			Expect(agent.Status.DebugInfo.State).To(Equal("unbinding-pending-user-action"))
		}
	}, Entry("known Agent", "known", true), Entry("binding Agent", "binding", true),
		Entry("installing Agent", "installing", false), Entry("installed Agent", "installed", false))

	DescribeTable("selects the native installation GroupKind without restricting its version",
		func(ctx SpecContext, variant string, ready bool) {
			agent, c := installingAgent()
			deployment := &hivev1.ClusterDeployment{}
			ref := agent.Spec.ClusterDeploymentName
			Expect(c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, deployment)).To(Succeed())

			switch variant {
			case "group":
				deployment.Spec.ClusterInstallRef.Group = "other.example"
			case "kind":
				deployment.Spec.ClusterInstallRef.Kind = "OtherInstall"
			case "version":
				deployment.Spec.ClusterInstallRef.Version = "v1alpha1"
			}

			Expect(c.Update(ctx, deployment)).To(Succeed())
			r := &AgentReconciler{Client: c, APIReader: c}
			observed, err := r.deploymentReady(ctx, agent)
			Expect(err).NotTo(HaveOccurred())
			Expect(observed).To(Equal(ready))
		},
		Entry("another group", "group", false),
		Entry("another kind", "kind", false),
		Entry("another version of the native GroupKind", "version", true),
	)

	DescribeTable("uses live installation responses without losing terminal errors",
		func(ctx SpecContext, initial InstallationResponse, initialState, finalState string) {
			By("preparing one approved Agent with its installation records")

			agent, c := installingAgent()
			selected := initial

			var readError error

			r := &AgentReconciler{
				Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
				Installation: func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
					return selected, readError
				},
			}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)}

			By("publishing the held or failed installation response")

			result, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			current := &assistedv1.Agent{}
			Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
			Expect(current.Status.DebugInfo.State).To(Equal(initialState))

			if initial == InstallationHeld {
				Expect(result.RequeueAfter).To(Equal(time.Second))
			}

			By("releasing the response while preserving the Agent's identity and terminal state")

			selected = InstallationComplete
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
			Expect(current.UID).To(Equal(agent.UID))
			Expect(current.Status.DebugInfo.State).To(Equal(finalState))

			By("leaving terminal installation observations independent of backend settings")

			readError = errors.New("settings unavailable")
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		},
		Entry("completes a held installation", InstallationHeld, "installing", "installed"),
		Entry("retains an installation failure", InstallationFailed, "error", "error"),
	)

	Describe("auto-approval", func() {
		var (
			agent *assistedv1.Agent
			c     client.WithWatch
			infra *assistedv1.InfraEnv
			r     *AgentReconciler
			req   ctrl.Request
		)

		BeforeEach(func(ctx SpecContext) {
			agent, c = installingAgent()
			agent.Spec.Approved = false
			agent.Finalizers = []string{agentFinalizer}
			Expect(c.Update(ctx, agent)).To(Succeed())
			agent.Status.DebugInfo.State = "known"
			Expect(c.Status().Update(ctx, agent)).To(Succeed())

			infra = &assistedv1.InfraEnv{}
			infraKey := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Labels[kube.InfraEnvLabel]}
			Expect(c.Get(ctx, infraKey, infra)).To(Succeed())
			r = &AgentReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{agent.Namespace}}}
			req = ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)}
		})

		DescribeTable("honors the InfraEnv setting for a ready deployment",
			func(ctx SpecContext, approval *assistedv1.AgentApproval, approved bool, state string) {
				infra.Spec.AgentApproval = approval
				Expect(c.Update(ctx, infra)).To(Succeed())

				_, err := r.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(c.Get(ctx, req.NamespacedName, agent)).To(Succeed())
				Expect(agent.Spec.Approved).To(Equal(approved))
				Expect(agent.Status.DebugInfo.State).To(Equal(state))
			},
			Entry("enabled", &assistedv1.AgentApproval{AutoApprove: true}, true, "installing"),
			Entry("disabled", &assistedv1.AgentApproval{}, false, "known"),
			Entry("absent", nil, false, "known"),
		)

		Context("when enabled", func() {
			BeforeEach(func(ctx SpecContext) {
				infra.Spec.AgentApproval = &assistedv1.AgentApproval{AutoApprove: true}
				Expect(c.Update(ctx, infra)).To(Succeed())
			})

			It("approves an unbound Agent without starting installation", func(ctx SpecContext) {
				agent.Spec.ClusterDeploymentName = nil
				Expect(c.Update(ctx, agent)).To(Succeed())
				agent.Status.DebugInfo.State = "known-unbound"
				Expect(c.Status().Update(ctx, agent)).To(Succeed())

				_, err := r.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(c.Get(ctx, req.NamespacedName, agent)).To(Succeed())
				Expect(agent.Spec.Approved).To(BeTrue())
				Expect(agent.Status.DebugInfo.State).To(Equal("known-unbound"))
			})

			It("does not approve an Agent with a missing deployment", func(ctx SpecContext) {
				ref := agent.Spec.ClusterDeploymentName
				deployment := &hivev1.ClusterDeployment{}
				Expect(c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, deployment)).To(Succeed())
				Expect(c.Delete(ctx, deployment)).To(Succeed())

				result, err := r.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				Expect(c.Get(ctx, req.NamespacedName, agent)).To(Succeed())
				Expect(agent.Spec.Approved).To(BeFalse())
				Expect(agent.Status.DebugInfo.State).To(Equal("known"))
			})

			It("preserves concurrent Agent changes on conflict", func(ctx SpecContext) {
				r.Client = interceptor.NewClient(c, interceptor.Funcs{Patch: func(
					ctx context.Context, c client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption,
				) error {
					latest := &assistedv1.Agent{}
					Expect(c.Get(ctx, req.NamespacedName, latest)).To(Succeed())
					latest.Labels["example.com/concurrent"] = "kept"
					Expect(c.Update(ctx, latest)).To(Succeed())

					return c.Patch(ctx, object, patch, opts...)
				}})
				_, err := r.Reconcile(ctx, req)
				Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected approval to conflict, got %v", err)
				Expect(c.Get(ctx, req.NamespacedName, agent)).To(Succeed())
				Expect(agent.Spec.Approved).To(BeFalse())
				Expect(agent.Status.DebugInfo.State).To(Equal("known"))
				Expect(agent.Labels).To(HaveKeyWithValue("example.com/concurrent", "kept"))
			})
		})
	})

	It("advances discovery when inventory arrives after registration", func(ctx SpecContext) {
		agent, c := installingAgent()
		key := client.ObjectKeyFromObject(agent)
		Expect(c.Get(ctx, key, agent)).To(Succeed())
		agent.Status.DebugInfo.State = ""
		agent.Status.Inventory.Interfaces = nil
		Expect(c.Status().Update(ctx, agent)).To(Succeed())
		r := &AgentReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			Installation: func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
				return InstallationComplete, nil
			},
		}
		request := ctrl.Request{NamespacedName: key}
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, key, agent)).To(Succeed())
		Expect(agent.Status.DebugInfo.State).To(Equal("discovering-unbound"))

		agent.Status.Inventory.Interfaces = []assistedv1.HostInterface{{Name: "eth0"}}
		Expect(c.Status().Update(ctx, agent)).To(Succeed())

		for _, state := range []string{"binding", "known", "installing", "installed"} {
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, key, agent)).To(Succeed())
			Expect(agent.Status.DebugInfo.State).To(Equal(state))
		}

		Expect(agent.UID).To(BeEquivalentTo("original-agent"))
	})

	It("reports an unknown installation response while retaining native discovery observations", func(ctx SpecContext) {
		agent, c := installingAgent()
		r := &AgentReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			Installation: func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
				return "unexpected", nil
			},
		}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)}
		before := &assistedv1.Agent{}
		Expect(c.Get(ctx, request.NamespacedName, before)).To(Succeed())

		_, err := r.Reconcile(ctx, request)
		Expect(err).To(MatchError(`unsupported installation response "unexpected"`))

		current := &assistedv1.Agent{}
		Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(current.UID).To(Equal(before.UID))
		Expect(current.Spec).To(Equal(before.Spec))
		Expect(current.Status.DebugInfo.State).To(Equal("installing"))
		Expect(conditionsv1.IsStatusConditionTrue(current.Status.Conditions, assistedv1.ConnectedCondition)).To(BeTrue())
		Expect(conditionsv1.IsStatusConditionFalse(current.Status.Conditions, assistedv1.SpecSyncedCondition)).To(BeTrue())
	})

	DescribeTable("preserves registrations outside their active InfraEnv", func(ctx SpecContext, state string) {
		agent := &assistedv1.Agent{ObjectMeta: metav1.ObjectMeta{
			Namespace: "hub", Name: "worker", UID: "agent-uid",
			Labels: map[string]string{kube.InfraEnvLabel: "infra"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: assistedv1.GroupVersion.String(), Kind: "InfraEnv", Name: "infra", UID: "original",
			}},
		}}
		builder := fake.NewClientBuilder().WithScheme(agentScheme())

		if state == "replaced" || state == "deleting" {
			infra := &assistedv1.InfraEnv{ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub", Name: "infra", UID: "replacement",
			}}
			if state == "deleting" {
				infra.UID = "original"
				infra.Finalizers = []string{"foreign.example/cleanup"}
				infra.DeletionTimestamp = new(metav1.Now())
			}

			builder.WithObjects(infra)
		}

		if state == "foreign" {
			builder.WithObjects(
				&assistedv1.InfraEnv{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "infra", UID: "original"}},
			)
			agent.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "example.com/v1", Kind: "ForeignOwner", Name: "foreign", UID: "foreign",
			}}
		}

		c := builder.WithObjects(agent).Build()
		r := &AgentReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			Installation: func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
				Fail("inactive registrations must not consult installation settings")

				return "", nil
			},
		}

		By("reconciling the registration after its InfraEnv becomes unavailable")

		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)}
		before := &assistedv1.Agent{}
		Expect(c.Get(ctx, request.NamespacedName, before)).To(Succeed())
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		current := &assistedv1.Agent{}
		Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(current).To(Equal(before))
	},
		Entry("the InfraEnv is missing", "missing"),
		Entry("the InfraEnv has been replaced", "replaced"),
		Entry("the InfraEnv is deleting", "deleting"),
		Entry("the Agent has a foreign owner", "foreign"),
	)

	DescribeTable(
		"keeps discovery observations independent",
		func(state string, approved bool, interfaces int, connected, validated, eligible corev1.ConditionStatus) {
			agent := &assistedv1.Agent{Spec: assistedv1.AgentSpec{Approved: approved}}
			agent.Status.DebugInfo.State = state
			agent.Status.Inventory.Interfaces = make([]assistedv1.HostInterface, interfaces)
			setAgentConditions(agent, nil)
			Expect(
				conditionsv1.FindStatusCondition(agent.Status.Conditions, assistedv1.ConnectedCondition).Status,
			).To(Equal(connected))
			Expect(
				conditionsv1.FindStatusCondition(agent.Status.Conditions, assistedv1.ValidatedCondition).Status,
			).To(Equal(validated))
			Expect(
				conditionsv1.FindStatusCondition(agent.Status.Conditions, assistedv1.RequirementsMetCondition).Status,
			).To(Equal(eligible))
			Expect(conditionsv1.IsStatusConditionTrue(agent.Status.Conditions, assistedv1.SpecSyncedCondition)).To(BeTrue())
		},
		Entry(
			"approval is separate from validation",
			"known-unbound",
			false,
			1,
			corev1.ConditionTrue,
			corev1.ConditionTrue,
			corev1.ConditionFalse,
		),
		Entry(
			"connected discovery can lack inventory",
			"discovering-unbound",
			false,
			0,
			corev1.ConditionTrue,
			corev1.ConditionUnknown,
			corev1.ConditionFalse,
		),
		Entry(
			"disconnected hosts retain inventory",
			"disconnected-unbound",
			true,
			1,
			corev1.ConditionFalse,
			corev1.ConditionTrue,
			corev1.ConditionFalse,
		),
		Entry(
			"binding is not install eligibility",
			"binding",
			true,
			1,
			corev1.ConditionTrue,
			corev1.ConditionFalse,
			corev1.ConditionFalse,
		),
		Entry(
			"validation failure is distinct",
			"insufficient-unbound",
			true,
			1,
			corev1.ConditionTrue,
			corev1.ConditionFalse,
			corev1.ConditionFalse,
		),
	)

	DescribeTable("rejects installations outside the original deployment", func(ctx SpecContext, variant string) {
		agent, c := installingAgent()
		installation := &installv1.AgentClusterInstall{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: "control-plane", Name: "installation"}, installation)).To(Succeed())

		switch variant {
		case "UID":
			installation.OwnerReferences[0].UID = "previous-deployment"
		case "name":
			installation.OwnerReferences[0].Name = "other-deployment"
		case "version":
			installation.OwnerReferences[0].APIVersion = "example.com/v1"
		}

		Expect(c.Update(ctx, installation)).To(Succeed())
		before := installation.DeepCopy()
		r := &AgentReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			Installation: func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
				return InstallationComplete, nil
			},
		}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)})
		Expect(err).To(MatchError(ContainSubstring("another deployment incarnation")))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.Status.DebugInfo.State).To(Equal("installing"))
		Expect(conditionsv1.IsStatusConditionFalse(agent.Status.Conditions, assistedv1.SpecSyncedCondition)).To(BeTrue())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(installation), installation)).To(Succeed())
		Expect(installation).To(Equal(before))
	}, Entry("owner UID", "UID"), Entry("owner name", "name"), Entry("owner version", "version"))

	DescribeTable("accepts an omitted pull-secret reference for an installed deployment",
		func(ctx SpecContext, empty bool) {
			agent, c := installingAgent()
			deployment := &hivev1.ClusterDeployment{}
			ref := agent.Spec.ClusterDeploymentName
			Expect(c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, deployment)).To(Succeed())

			deployment.Spec.PullSecretRef = nil
			if empty {
				deployment.Spec.PullSecretRef = &corev1.LocalObjectReference{}
			}

			Expect(c.Update(ctx, deployment)).To(Succeed())
			r := &AgentReconciler{Client: c, APIReader: c}
			ready, err := r.deploymentReady(ctx, agent)
			Expect(err).NotTo(HaveOccurred())
			Expect(ready).To(BeTrue())
		}, Entry("nil reference", false), Entry("empty reference", true))

	DescribeTable("selects the native admin kubeconfig Secret",
		func(ctx SpecContext, metadata *hivev1.ClusterMetadata, ready bool) {
			agent, c := installingAgent()
			ref := agent.Spec.ClusterDeploymentName
			deployment := &hivev1.ClusterDeployment{}
			Expect(c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, deployment)).To(Succeed())
			deployment.Spec.ClusterMetadata = metadata
			Expect(c.Update(ctx, deployment)).To(Succeed())
			Expect(c.Delete(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: deployment.Namespace, Name: "access"},
			})).To(Succeed())
			Expect(c.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: deployment.Namespace, Name: deployment.Name + "-admin-kubeconfig"},
				Data:       map[string][]byte{"kubeconfig": []byte("synthetic guest access")},
			})).To(Succeed())

			r := &AgentReconciler{Client: c, APIReader: c}
			observed, err := r.deploymentReady(ctx, agent)

			if ready {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}

			Expect(observed).To(Equal(ready))
		},
		Entry("nil metadata", nil, true),
		Entry("empty reference", &hivev1.ClusterMetadata{}, true),
		Entry("missing explicit Secret", &hivev1.ClusterMetadata{
			AdminKubeconfigSecretRef: corev1.LocalObjectReference{Name: "absent"},
		}, false),
	)

	DescribeTable("reports missing day-two inputs without installing", func(ctx SpecContext, input string) {
		agent, c := installingAgent()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		agent.Status.DebugInfo.State = "known-unbound"
		Expect(c.Status().Update(ctx, agent)).To(Succeed())

		key := client.ObjectKey{Namespace: "control-plane", Name: "pull-secret"}
		if input == "kubeconfig" {
			key.Name = "access"
		}

		switch input {
		case "CA":
			installation := &installv1.AgentClusterInstall{}
			Expect(c.Get(ctx, client.ObjectKey{Namespace: "control-plane", Name: "installation"}, installation)).To(Succeed())
			installation.Spec.IgnitionEndpoint = &installv1.IgnitionEndpoint{
				Url:                    "https://ignition.example.invalid",
				CaCertificateReference: &installv1.CaCertificateReference{Namespace: "control-plane", Name: "missing-ca"},
			}
			Expect(c.Update(ctx, installation)).To(Succeed())
		case "token":
			agent.Spec.IgnitionEndpointTokenReference = &assistedv1.IgnitionEndpointTokenReference{
				Namespace: "control-plane",
				Name:      "missing-token",
			}
			Expect(c.Update(ctx, agent)).To(Succeed())
		default:
			Expect(
				c.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}}),
			).To(Succeed())
		}

		r := &AgentReconciler{
			Client:    c,
			APIReader: c,
			Scope:     kube.Scope{AgentNamespaces: []string{"hub"}},
			Installation: func(context.Context, *assistedv1.InfraEnv) (InstallationResponse, error) {
				return InstallationComplete, nil
			},
		}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(agent)})
		Expect(err).To(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.Status.DebugInfo.State).To(Equal("known-unbound"))
		sync := conditionsv1.FindStatusCondition(agent.Status.Conditions, assistedv1.SpecSyncedCondition)
		Expect(sync.Status).To(Equal(corev1.ConditionFalse))
		Expect(sync.Reason).To(Equal(assistedv1.InputErrorReason))
	},
		Entry("pull-secret", "pull-secret"),
		Entry("kubeconfig", "kubeconfig"),
		Entry("ignition CA", "CA"),
		Entry("ignition token", "token"),
	)
})
