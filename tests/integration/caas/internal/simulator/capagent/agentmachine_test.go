// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capagent

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// agentMachineRecoveryInputs supplies the original Machine, provider and Cluster
// with a control plane that identifies the served host namespace.
func agentMachineRecoveryInputs() (
	*capiv1.Machine, *agentv1.AgentMachine, *capiv1.Cluster, *hypershiftv1.HostedControlPlane,
) {
	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"},
		Spec: capiv1.ClusterSpec{ControlPlaneRef: &corev1.ObjectReference{
			APIVersion: hypershiftv1.GroupVersion.String(), Kind: "HostedControlPlane", Namespace: "cp", Name: "control-plane",
		}},
	}
	cp := &hypershiftv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "control-plane",
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(cluster, capiv1.GroupVersion.WithKind("Cluster"))},
		},
		Spec: hypershiftv1.HostedControlPlaneSpec{Platform: hypershiftv1.PlatformSpec{
			Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
		}},
	}
	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "cp",
			Name:       "worker",
			UID:        "machine",
			Finalizers: []string{"machine.cluster.x-k8s.io"},
		},
		Spec: capiv1.MachineSpec{ClusterName: cluster.Name},
	}
	consumer := &agentv1.AgentMachine{ObjectMeta: metav1.ObjectMeta{
		Namespace: "cp", Name: "worker", UID: "consumer",
		Labels:          map[string]string{kube.ManagedByLabel: kube.ManagerName},
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(machine, capiv1.GroupVersion.WithKind("Machine"))},
	}}

	return machine, consumer, cluster, cp
}

// discoveredAgent supplies an approved and eligible host with installation pending.
func discoveredAgent() *assistedv1.Agent {
	agent := &assistedv1.Agent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "host", UID: "original-agent"},
		Spec:       assistedv1.AgentSpec{Approved: true},
	}
	for kind, status := range map[conditionsv1.ConditionType]corev1.ConditionStatus{
		"Connected": corev1.ConditionTrue, "Validated": corev1.ConditionTrue,
		"SpecSynced": corev1.ConditionTrue, "RequirementsMet": corev1.ConditionTrue,
		"Bound": corev1.ConditionFalse, "Installed": corev1.ConditionFalse,
	} {
		conditionsv1.SetStatusConditionNoHeartbeat(&agent.Status.Conditions, conditionsv1.Condition{
			Type: kind, Status: status, Reason: "Observed",
		})
	}

	agent.Status.DebugInfo.State = "known-unbound"

	return agent
}

var _ = Describe("AgentMachine claims and release", func() {
	var (
		machine  *capiv1.Machine
		consumer *agentv1.AgentMachine
		cluster  *capiv1.Cluster
		cp       *hypershiftv1.HostedControlPlane
	)

	BeforeEach(func() {
		machine, consumer, cluster, cp = agentMachineRecoveryInputs()
	})

	It("permits an absent bootstrap reference while preserving native claim recovery", func(ctx SpecContext) {
		consumer.Labels = nil
		cluster.Spec.InfrastructureRef = &corev1.ObjectReference{
			APIVersion: agentv1.GroupVersion.String(),
			Kind:       "AgentCluster",
			Namespace:  cluster.Namespace,
			Name:       "infrastructure",
		}
		infra := &agentv1.AgentCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp",
				Name:      "infrastructure",
				UID:       "original-infrastructure",
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(cluster, capiv1.GroupVersion.WithKind("Cluster")),
				},
			},
			Status: agentv1.AgentClusterStatus{ClusterDeploymentRef: agentv1.ClusterDeploymentReference{
				Name: "deployment", Namespace: "cp",
			}},
		}
		agent := discoveredAgent()
		agent.Spec.IgnitionEndpointTokenReference = &assistedv1.IgnitionEndpointTokenReference{
			Namespace: "previous", Name: "missing-token",
		}
		agent.Spec.IgnitionEndpointHTTPHeaders = map[string]string{"X-Previous": "stale"}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).
			WithObjects(cluster, cp, infra, machine, consumer, agent).WithStatusSubresource(consumer).Build()
		r := &AgentMachineReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}

		By("persisting the native claim without requiring bootstrap data")

		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.Labels["agentMachineRef"]).To(Equal(consumer.Name))
		Expect(agent.Spec.ClusterDeploymentName.Name).To(Equal("deployment"))
		Expect(agent.Spec.IgnitionEndpointTokenReference).To(BeNil())
		Expect(agent.Spec.IgnitionEndpointHTTPHeaders).To(BeEmpty())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
		Expect(consumer.Status.AgentRef).To(Equal(&agentv1.AgentReference{Name: agent.Name, Namespace: agent.Namespace}))
	})

	DescribeTable(
		"rejects foreign acquisition inputs without changing Agents",
		func(ctx SpecContext, variant string) {
			cluster.Spec.InfrastructureRef = &corev1.ObjectReference{
				APIVersion: agentv1.GroupVersion.String(),
				Kind:       "AgentCluster",
				Namespace:  cluster.Namespace,
				Name:       "infrastructure",
			}
			infra := &agentv1.AgentCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: cluster.Namespace,
					Name:      "infrastructure",
					UID:       "infrastructure",
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(cluster, capiv1.GroupVersion.WithKind("Cluster")),
					},
				},
				Status: agentv1.AgentClusterStatus{
					ClusterDeploymentRef: agentv1.ClusterDeploymentReference{Name: "deployment", Namespace: "cp"},
				},
			}

			switch variant {
			case "kind":
				cluster.Spec.InfrastructureRef.Kind = "ForeignCluster"
			case "version":
				cluster.Spec.InfrastructureRef.APIVersion = "example.com/v1"
			case "namespace":
				cluster.Spec.InfrastructureRef.Namespace = "other"
			case "owner UID":
				infra.OwnerReferences[0].UID = "old-cluster"
			case "owner name":
				infra.OwnerReferences[0].Name = "another-cluster"
			case "owner version":
				infra.OwnerReferences[0].APIVersion = "example.com/v1"
			}

			agent := discoveredAgent()
			c := fake.NewClientBuilder().
				WithScheme(capagentScheme()).
				WithObjects(cluster, cp, infra, machine, consumer, agent).
				Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			before := agent.DeepCopy()
			r := &AgentMachineReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)})
			Expect(err).To(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			Expect(agent).To(Equal(before))
		},
		Entry("reference kind", "kind"),
		Entry("reference version", "version"),
		Entry("reference namespace", "namespace"),
		Entry(
			"original owner UID",
			"owner UID",
		),
		Entry("original owner name", "owner name"),
		Entry("original owner version", "owner version"),
	)

	It("retains a held claim and then releases inputs on the original Agent", func(ctx SpecContext) {
		machine.Annotations = map[string]string{kube.MachineHook: ""}
		conditions.Set(machine, &capiv1.Condition{
			Type: capiv1.PreTerminateDeleteHookSucceededCondition, Status: corev1.ConditionFalse, Reason: "WaitingForHook",
		})

		agent := discoveredAgent()
		agent.Labels = map[string]string{"agentMachineRef": consumer.Name}
		agent.Annotations = map[string]string{"agentMachineRefNamespace": consumer.Namespace}
		agent.Spec.ClusterDeploymentName = &assistedv1.ClusterReference{Name: "deployment", Namespace: "cp"}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(machine, consumer, agent).
			WithStatusSubresource(consumer).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())

		hold := true
		r := &AgentMachineReconciler{
			Client: c, APIReader: c,
			HoldDetach: func(context.Context, metav1.Object) (bool, error) { return hold, nil },
		}

		By("holding detach without losing the Agent claim")

		result, err := r.release(ctx, consumer, machine, agent, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))

		current := &assistedv1.Agent{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), current)).To(Succeed())
		Expect(current.Labels).To(HaveKeyWithValue("agentMachineRef", consumer.Name))

		By("releasing the same Agent and clearing its consumer inputs")

		hold = false
		result, err = r.release(ctx, consumer, machine, current, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), current)).To(Succeed())
		Expect(current.UID).To(Equal(agent.UID))
		Expect(current.Labels["agentMachineRef"]).To(BeEmpty())
		Expect(current.Spec.ClusterDeploymentName).To(BeNil())
	})

	It("recovers the persisted claim and readiness without acquiring a second host", func(ctx SpecContext) {
		By("preparing bootstrap input and two eligible hosts")

		cluster.Spec.InfrastructureRef = &corev1.ObjectReference{
			APIVersion: agentv1.GroupVersion.String(),
			Kind:       "AgentCluster",
			Namespace:  cluster.Namespace,
			Name:       "infrastructure",
		}
		machine.Spec.Bootstrap.DataSecretName = new("bootstrap")
		infra := &agentv1.AgentCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp",
				Name:      "infrastructure",
				UID:       "original-infrastructure",
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(cluster, capiv1.GroupVersion.WithKind("Cluster")),
				},
			},
			Status: agentv1.AgentClusterStatus{ClusterDeploymentRef: agentv1.ClusterDeploymentReference{
				Name: "deployment", Namespace: "cp",
			}},
		}
		bootstrap := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "bootstrap", UID: "bootstrap"},
			Data: map[string][]byte{"value": []byte(
				`{"ignition":{"version":"3.2.0","config":{"merge":[` +
					`{"source":"https://ignition.caas-it.invalid/ignition/worker",` +
					`"httpHeaders":[{"name":"Authorization","value":"Bearer test-token"}]}]}}}`,
			)},
		}
		agent := discoveredAgent()
		spare := agent.DeepCopy()
		spare.Name, spare.UID = "spare", "spare-agent"
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).
			WithObjects(machine, consumer, cluster, cp, infra, bootstrap, agent, spare).
			WithStatusSubresource(consumer, agent, spare).Build()
		r := &AgentMachineReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)}

		By("persisting the claim and native reference before installation readiness")

		result, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))

		claimed := &agentv1.AgentMachine{}
		Expect(c.Get(ctx, request.NamespacedName, claimed)).To(Succeed())
		Expect(claimed.Status.AgentRef).To(Equal(&agentv1.AgentReference{Name: agent.Name, Namespace: agent.Namespace}))
		Expect(claimed.Status.Ready).To(BeFalse())

		current := &assistedv1.Agent{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), current)).To(Succeed())
		Expect(agentClaimMatches(current, consumer)).To(BeTrue())
		Expect(current.Spec.ClusterDeploymentName.Name).To(Equal("deployment"))

		By("recovering a deleted token while the unlabelled claim is still pending")

		token := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: machine.Namespace, Name: "agent-bootstrap"}}
		Expect(c.Delete(ctx, token)).To(Succeed())

		claimed.Labels = nil
		Expect(c.Update(ctx, claimed)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(token.Data["ignition-token"]).NotTo(BeEmpty())
		Expect(c.Get(ctx, request.NamespacedName, claimed)).To(Succeed())
		Expect(claimed.Status.Ready).To(BeFalse())
		Expect(claimed.Status.AgentRef.Name).To(Equal(agent.Name))

		By("observing Assisted completion through a fresh reconciler")

		conditionsv1.SetStatusConditionNoHeartbeat(&current.Status.Conditions, conditionsv1.Condition{
			Type: "Bound", Status: corev1.ConditionTrue, Reason: "AssistedObserved",
		})
		conditionsv1.SetStatusConditionNoHeartbeat(&current.Status.Conditions, conditionsv1.Condition{
			Type: "Installed", Status: corev1.ConditionTrue, Reason: "AssistedObserved",
		})
		Expect(c.Status().Update(ctx, current)).To(Succeed())
		restarted := &AgentMachineReconciler{Client: c, APIReader: c, Scope: r.Scope}
		_, err = restarted.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, claimed)).To(Succeed())
		Expect(claimed.Spec.ProviderID).To(Equal(new("agent://host")))
		Expect(claimed.Status.AgentRef.Name).To(Equal("host"))
		Expect(claimed.Status.Ready).To(BeTrue())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(spare), spare)).To(Succeed())
		Expect(agentUnclaimed(spare)).To(BeTrue(), "recovery must not acquire another host")
	})

	DescribeTable(
		"retains Agent identity through deletion immediately after acquisition",
		func(ctx SpecContext, failStatus bool) {
			cluster.Spec.InfrastructureRef = &corev1.ObjectReference{
				APIVersion: agentv1.GroupVersion.String(), Kind: "AgentCluster", Name: "infrastructure",
			}
			infra := &agentv1.AgentCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: cluster.Namespace,
					Name:      "infrastructure",
					UID:       "infrastructure",
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(cluster, capiv1.GroupVersion.WithKind("Cluster")),
					},
				},
				Status: agentv1.AgentClusterStatus{ClusterDeploymentRef: agentv1.ClusterDeploymentReference{
					Name: "deployment", Namespace: "cp",
				}},
			}
			consumer.Finalizers = []string{"foreign.example/cleanup"}
			agent := discoveredAgent()
			failure := errors.New("interrupted Agent reference write")
			c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(cluster, cp, infra, machine, consumer, agent).
				WithStatusSubresource(machine, consumer, agent).
				WithInterceptorFuncs(interceptor.Funcs{SubResourcePatch: func(
					ctx context.Context, c client.Client, name string, object client.Object,
					patch client.Patch, opts ...client.SubResourcePatchOption,
				) error {
					if _, provider := object.(*agentv1.AgentMachine); provider && name == "status" && failStatus {
						return failure
					}

					return c.SubResource(name).Patch(ctx, object, patch, opts...)
				}}).Build()
			r := &AgentMachineReconciler{
				Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
				HoldDetach: func(context.Context, metav1.Object) (bool, error) { return false, nil },
			}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)}

			By("claiming a host before the Machine begins deleting")

			_, err := r.Reconcile(ctx, request)
			if failStatus {
				Expect(err).To(MatchError(failure))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			Expect(agentClaimMatches(agent, consumer)).To(BeTrue())
			agent.Status.DebugInfo.State = "installed"
			Expect(c.Status().Update(ctx, agent)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(c.Delete(ctx, machine)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			conditions.MarkFalse(
				machine,
				capiv1.PreTerminateDeleteHookSucceededCondition,
				"WaitingForHook",
				capiv1.ConditionSeverityInfo,
				"",
			)
			Expect(c.Status().Update(ctx, machine)).To(Succeed())

			if failStatus {
				By("keeping the claim and deletion holds when recovery cannot persist its reference")

				_, err = r.Reconcile(ctx, request)
				Expect(err).To(MatchError(failure))
				Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
				Expect(agentClaimMatches(agent, consumer)).To(BeTrue())
				Expect(c.Get(ctx, request.NamespacedName, consumer)).To(Succeed())
				Expect(consumer.Status.AgentRef).To(BeNil())
				Expect(consumer.Finalizers).To(ContainElement(agentMachineFinalizer))
				Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
				Expect(machine.Annotations).To(HaveKey(kube.MachineHook))
			}

			By("recovering the reference before clearing the Agent-side claim after restart")

			failStatus = false
			restarted := &AgentMachineReconciler{Client: c, APIReader: c, Scope: r.Scope, HoldDetach: r.HoldDetach}
			result, err := restarted.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())
			Expect(c.Get(ctx, request.NamespacedName, consumer)).To(Succeed())
			Expect(consumer.Status.AgentRef).To(Equal(&agentv1.AgentReference{Name: agent.Name, Namespace: agent.Namespace}))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			Expect(agentUnclaimed(agent)).To(BeTrue())
			Expect(agent.Spec.ClusterDeploymentName).To(BeNil())

			By("retaining the deletion barriers until Assisted reports a release-complete state")

			agent.Status.DebugInfo.State = "unbinding"
			Expect(c.Status().Update(ctx, agent)).To(Succeed())
			result, err = restarted.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())
			Expect(c.Get(ctx, request.NamespacedName, consumer)).To(Succeed())
			Expect(consumer.Finalizers).To(ConsistOf(agentMachineFinalizer, "foreign.example/cleanup"))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(machine.Annotations).To(HaveKey(kube.MachineHook))

			agent.Status.DebugInfo.State = "unbinding-pending-user-action"
			Expect(c.Status().Update(ctx, agent)).To(Succeed())
			_, err = restarted.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, request.NamespacedName, consumer)).To(Succeed())
			Expect(consumer.Finalizers).To(ConsistOf("foreign.example/cleanup"))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(machine.Annotations).NotTo(HaveKey(kube.MachineHook))
		},
		Entry("the acquisition reference was saved", false),
		Entry("the acquisition and first recovery status writes failed", true),
	)

	It("follows Agent readiness even when acquisition inputs are unavailable", func(ctx SpecContext) {
		agent := discoveredAgent()
		agent.Labels = map[string]string{"agentMachineRef": consumer.Name}
		agent.Annotations = map[string]string{"agentMachineRefNamespace": consumer.Namespace}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(machine, consumer, cluster, cp, agent).
			WithStatusSubresource(consumer, agent).Build()
		r := &AgentMachineReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)}

		for _, step := range []struct {
			description string
			installed   corev1.ConditionStatus
		}{
			{"observing the installed Agent", corev1.ConditionTrue},
			{"retaining native readiness after installation was observed", corev1.ConditionFalse},
			{"recovering readiness on the same claim", corev1.ConditionTrue},
		} {
			By(step.description)

			Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			conditionsv1.SetStatusConditionNoHeartbeat(&agent.Status.Conditions, conditionsv1.Condition{
				Type: "Installed", Status: step.installed, Reason: "AssistedObserved",
			})
			Expect(c.Status().Update(ctx, agent)).To(Succeed())
			result, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero(), "existing claims must not wait for acquisition inputs")
			Expect(c.Get(ctx, request.NamespacedName, consumer)).To(Succeed())
			Expect(consumer.Status.Ready).To(BeTrue())
			Expect(conditions.Get(consumer, "Installed").Status).To(Equal(corev1.ConditionTrue))
			Expect(consumer.Spec.ProviderID).To(Equal(new("agent://host")))
			Expect(consumer.Status.AgentRef).To(Equal(&agentv1.AgentReference{Name: "host", Namespace: "hub"}))
		}
	})

	DescribeTable(
		"preserves the current consumer when an old Agent reference is stale",
		func(ctx SpecContext, variant string) {
			consumer.Finalizers = []string{agentMachineFinalizer, "foreign.example/cleanup"}
			agent := discoveredAgent()
			agent.Labels = map[string]string{"agentMachineRef": consumer.Name}
			agent.Annotations = map[string]string{"agentMachineRefNamespace": consumer.Namespace}

			switch variant {
			case "name":
				agent.Labels["agentMachineRef"] = "new-consumer"
			case "namespace":
				agent.Annotations["agentMachineRefNamespace"] = "other-control-plane"
			case "host-namespace":
				agent.Namespace = "other-hub"
			}

			agent.Spec.ClusterDeploymentName = &assistedv1.ClusterReference{
				Name:      "new-deployment",
				Namespace: "other-control-plane",
			}
			conditionsv1.SetStatusConditionNoHeartbeat(&agent.Status.Conditions, conditionsv1.Condition{
				Type: "Bound", Status: corev1.ConditionTrue, Reason: "CurrentConsumer",
			})
			consumer.Status.AgentRef = &agentv1.AgentReference{Name: agent.Name, Namespace: agent.Namespace}
			c := fake.NewClientBuilder().
				WithScheme(capagentScheme()).
				WithObjects(machine, consumer, cluster, cp, agent).
				WithStatusSubresource(machine).
				Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			before := agent.DeepCopy()
			r := &AgentMachineReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)}

			By("rejecting the stale reference while the old consumer is live")

			_, err := r.Reconcile(ctx, request)
			Expect(err).To(HaveOccurred())
			By("handling the old consumer at the Machine deletion hook within the served Agent scope")

			machine.Annotations = map[string]string{kube.MachineHook: ""}
			Expect(c.Update(ctx, machine)).To(Succeed())
			Expect(c.Delete(ctx, machine)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			conditions.MarkFalse(
				machine,
				capiv1.PreTerminateDeleteHookSucceededCondition,
				"WaitingForHook",
				capiv1.ConditionSeverityInfo,
				"",
			)
			Expect(c.Status().Update(ctx, machine)).To(Succeed())
			result, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			Expect(agent).To(Equal(before))
			Expect(c.Get(ctx, request.NamespacedName, consumer)).To(Succeed())

			if variant == "host-namespace" {
				Expect(consumer.Finalizers).To(ContainElement(agentMachineFinalizer))
				Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
				Expect(machine.Annotations).To(HaveKey(kube.MachineHook))
			} else {
				Expect(consumer.Finalizers).NotTo(ContainElement(agentMachineFinalizer))
			}

			Expect(consumer.Finalizers).To(ContainElement("foreign.example/cleanup"))
		},
		Entry("another consumer name", "name"),
		Entry("another consumer namespace", "namespace"),
		Entry("an Agent outside the served namespace", "host-namespace"),
	)

	It("completes already-unclaimed release at the Machine hook without mutating the Agent", func(ctx SpecContext) {
		conditions.MarkFalse(
			machine,
			capiv1.PreTerminateDeleteHookSucceededCondition,
			"WaitingForHook",
			capiv1.ConditionSeverityInfo,
			"",
		)

		consumer.Finalizers = []string{agentMachineFinalizer, "foreign.example/cleanup"}
		agent := discoveredAgent()
		consumer.Status.AgentRef = &agentv1.AgentReference{Name: agent.Name, Namespace: agent.Namespace}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(machine, consumer, cluster, cp, agent).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		before := agent.DeepCopy()

		Expect(c.Delete(ctx, machine)).To(Succeed())
		r := &AgentMachineReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			HoldDetach: func(context.Context, metav1.Object) (bool, error) { return true, nil },
		}
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent).To(Equal(before))
	})

	It("conflicts on a concurrent claim and preserves the replacement consumer's inputs", func(ctx SpecContext) {
		conditions.Set(machine, &capiv1.Condition{
			Type: capiv1.PreTerminateDeleteHookSucceededCondition, Status: corev1.ConditionFalse, Reason: "WaitingForHook",
		})

		agent := discoveredAgent()
		agent.Labels = map[string]string{"agentMachineRef": consumer.Name}
		agent.Annotations = map[string]string{"agentMachineRefNamespace": consumer.Namespace}
		agent.Spec.ClusterDeploymentName = &assistedv1.ClusterReference{Name: "original", Namespace: "cp"}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(machine, consumer, agent).
			WithStatusSubresource(consumer).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())

		By("reassigning the Agent after the release observation")

		reassigned := agent.DeepCopy()
		reassigned.Labels = map[string]string{"agentMachineRef": "replacement"}
		reassigned.Spec.ClusterDeploymentName = &assistedv1.ClusterReference{Name: "replacement", Namespace: "cp"}
		Expect(c.Update(ctx, reassigned)).To(Succeed())
		r := &AgentMachineReconciler{
			Client: c, APIReader: c,
			HoldDetach: func(context.Context, metav1.Object) (bool, error) { return false, nil },
		}
		_, err := r.release(ctx, consumer, machine, agent, cluster)
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent).To(Equal(reassigned))
	})

	DescribeTable("recovers the current native claim when status is stale", func(ctx SpecContext, reference string) {
		if reference != "" {
			consumer.Status.AgentRef = &agentv1.AgentReference{Namespace: "hub", Name: reference}
		}

		owned := discoveredAgent()
		owned.Name = "original"
		owned.Labels = map[string]string{"agentMachineRef": consumer.Name}
		owned.Annotations = map[string]string{"agentMachineRefNamespace": consumer.Namespace}
		foreign := owned.DeepCopy()
		foreign.Name, foreign.UID = "foreign", "foreign-agent"
		foreign.Labels = map[string]string{"agentMachineRef": "other-consumer"}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(owned, foreign).Build()
		r := &AgentMachineReconciler{APIReader: c}
		found, err := r.associatedAgent(ctx, consumer, "hub")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).NotTo(BeNil())
		Expect(found.UID).To(Equal(owned.UID))
	},
		Entry("status is absent", ""),
		Entry("status names a missing host", "missing"),
		Entry("status names another consumer's host", "foreign"),
	)

	DescribeTable("recovers deletion after an interrupted setup write", func(ctx SpecContext, stage string) {
		failure := errors.New("interrupted setup write")
		interrupted := true
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(machine, consumer, cluster, cp).
			WithStatusSubresource(machine).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(
					ctx context.Context, c client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption,
				) error {
					_, isMachine := object.(*capiv1.Machine)
					_, isConsumer := object.(*agentv1.AgentMachine)
					if interrupted && ((stage == "Machine" && isMachine) || (stage == "AgentMachine" && isConsumer)) {
						return failure
					}

					return c.Patch(ctx, object, patch, opts...)
				},
			}).Build()
		r := &AgentMachineReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)}

		By("surfacing the setup failure without adding a provider deletion hold")

		_, err := r.Reconcile(ctx, request)
		Expect(errors.Is(err, failure)).To(BeTrue(), "original failure lost: %v", err)
		Expect(c.Get(ctx, request.NamespacedName, consumer)).To(Succeed())
		Expect(consumer.Finalizers).NotTo(ContainElement(agentMachineFinalizer))

		By("restarting after the Machine reaches its deletion hook")
		Expect(c.Delete(ctx, machine)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
		conditions.MarkFalse(
			machine,
			capiv1.PreTerminateDeleteHookSucceededCondition,
			"WaitingForHook",
			capiv1.ConditionSeverityInfo,
			"",
		)
		Expect(c.Status().Update(ctx, machine)).To(Succeed())

		interrupted = false
		restarted := &AgentMachineReconciler{Client: c, APIReader: c, Scope: r.Scope}
		result, err := restarted.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
		Expect(machine.Annotations).NotTo(HaveKey(kube.MachineHook))
	},
		Entry("Machine hook write fails", "Machine"),
		Entry("AgentMachine setup write fails", "AgentMachine"),
	)

	DescribeTable(
		"waits for the Machine controller to publish its deletion-hook condition",
		func(ctx SpecContext, target string) {
			consumer.Finalizers = []string{agentMachineFinalizer, "foreign.example/cleanup"}

			c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(machine, consumer, cluster, cp).Build()

			var terminating client.Object = machine

			if target == "AgentMachine" {
				terminating = consumer
			}

			Expect(c.Delete(ctx, terminating)).To(Succeed())
			restarted := &AgentMachineReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			result, err := restarted.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
			Expect(consumer.Finalizers).To(ConsistOf(agentMachineFinalizer, "foreign.example/cleanup"))
		},
		Entry("the Machine is deleted", "Machine"),
		Entry("the AgentMachine is deleted", "AgentMachine"),
	)

	DescribeTable(
		"holds release until a native terminal or unbound debug state",
		func(ctx SpecContext, state string, complete bool) {
			consumer.Finalizers = []string{agentMachineFinalizer, "foreign.example/cleanup"}
			machine.Annotations = map[string]string{kube.MachineHook: "", "retained": "yes"}
			agent := discoveredAgent()
			agent.Status.DebugInfo.State = state
			c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(machine, consumer, agent).Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
			r := &AgentMachineReconciler{Client: c, APIReader: c}
			result, err := r.release(ctx, consumer, machine, agent, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())

			if complete {
				Expect(result.RequeueAfter).To(BeZero())
				Expect(consumer.Finalizers).To(ConsistOf("foreign.example/cleanup"))
				Expect(machine.Annotations).NotTo(HaveKey(kube.MachineHook))
			} else {
				Expect(result.RequeueAfter).NotTo(BeZero())
				Expect(consumer.Finalizers).To(ContainElement(agentMachineFinalizer))
				Expect(machine.Annotations).To(HaveKey(kube.MachineHook))
			}

			Expect(machine.Annotations).To(HaveKeyWithValue("retained", "yes"))
		},
		Entry("discovering", "discovering-unbound", true),
		Entry("known", "known-unbound", true),
		Entry("disconnected", "disconnected-unbound", true),
		Entry("insufficient", "insufficient-unbound", true),
		Entry("disabled", "disabled-unbound", true),
		Entry("user action", "unbinding-pending-user-action", true),
		Entry("error", "error", true),
		Entry("still unbinding despite Bound=False", "unbinding", false),
		Entry("missing observation despite Bound=False", "", false),
	)

	DescribeTable("finishes deleting providers without their original Machine", func(ctx SpecContext, parent string) {
		consumer.Finalizers = []string{agentMachineFinalizer, "foreign.example/cleanup"}
		if parent == "ownerless" {
			consumer.OwnerReferences = nil
		}

		objects := []client.Object{consumer}

		if parent == "replaced" {
			machine.UID = "replacement-machine"
			objects = append(objects, machine)
		}

		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(objects...).Build()
		Expect(c.Delete(ctx, consumer)).To(Succeed())
		r := &AgentMachineReconciler{Client: c, APIReader: c}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
		Expect(consumer.Finalizers).To(ConsistOf("foreign.example/cleanup"))

		if parent == "replaced" {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(machine.Annotations).To(BeEmpty())
		}
	}, Entry("owner reference missing", "ownerless"), Entry("owner gone", "missing"), Entry("owner replaced", "replaced"))

	It("uses native condition names and propagates installation error severity", func(ctx SpecContext) {
		agent := discoveredAgent()
		conditionsv1.SetStatusConditionNoHeartbeat(
			&agent.Status.Conditions,
			conditionsv1.Condition{
				Type:    assistedv1.InstalledCondition,
				Status:  corev1.ConditionFalse,
				Reason:  assistedv1.InstallationFailedReason,
				Message: "disk installation failed",
			},
		)

		c := fake.NewClientBuilder().
			WithScheme(capagentScheme()).
			WithObjects(consumer).
			WithStatusSubresource(consumer).
			Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
		r := &AgentMachineReconciler{Client: c, APIReader: c}
		Expect(r.reportAgent(ctx, consumer, agent)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
		Expect(consumer.Status.Ready).To(BeFalse())

		for _, kind := range []capiv1.ConditionType{
			agentv1.AgentReservedCondition,
			agentv1.AgentSpecSyncedCondition,
			agentv1.AgentValidatedCondition,
			agentv1.AgentRequirementsMetCondition,
		} {
			Expect(conditions.IsTrue(consumer, kind)).To(BeTrue())
		}

		failure := conditions.Get(consumer, agentv1.InstalledCondition)
		Expect(failure.Reason).To(Equal(assistedv1.InstallationFailedReason))
		Expect(failure.Severity).To(Equal(capiv1.ConditionSeverityError))
		Expect(failure.Message).To(Equal("disk installation failed"))
		Expect(conditions.Get(consumer, capiv1.ReadyCondition).Reason).To(Equal(assistedv1.InstallationFailedReason))
	})
	DescribeTable(
		"uses the Machine deletion-hook observation before touching a claim",
		func(ctx SpecContext, status corev1.ConditionStatus) {
			consumer.Finalizers = []string{agentMachineFinalizer, "foreign.example/cleanup"}

			machine.Annotations = map[string]string{kube.MachineHook: ""}
			if status != "" {
				conditions.Set(machine, &capiv1.Condition{Type: capiv1.PreTerminateDeleteHookSucceededCondition, Status: status})
			}

			if status == corev1.ConditionTrue {
				consumer.Status.AgentRef = &agentv1.AgentReference{Namespace: "other-hub", Name: "outside"}
			}

			agent := discoveredAgent()
			agent.Labels = map[string]string{"agentMachineRef": consumer.Name}
			agent.Annotations = map[string]string{
				"agentMachineRefNamespace": consumer.Namespace,
				"foreign.example/retained": "yes",
			}
			agent.Spec.ClusterDeploymentName = &assistedv1.ClusterReference{Namespace: cluster.Namespace, Name: "deployment"}
			c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(machine, consumer, cluster, agent).
				WithStatusSubresource(consumer).Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			before := agent.DeepCopy()

			Expect(c.Delete(ctx, machine)).To(Succeed())
			r := &AgentMachineReconciler{
				Client:     c,
				APIReader:  c,
				Scope:      kube.Scope{AgentNamespaces: []string{"hub"}},
				HoldDetach: func(context.Context, metav1.Object) (bool, error) { return false, nil },
			}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)})
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())

			if status == corev1.ConditionFalse {
				Expect(agent.Labels).NotTo(HaveKey("agentMachineRef"))
				Expect(agent.Spec.ClusterDeploymentName).To(BeNil())
				Expect(consumer.Finalizers).To(ContainElement(agentMachineFinalizer))
			} else {
				Expect(agent).To(Equal(before))

				if status == corev1.ConditionTrue {
					Expect(consumer.Finalizers).To(ConsistOf("foreign.example/cleanup"))
				} else {
					Expect(consumer.Finalizers).To(ContainElement(agentMachineFinalizer))
				}
			}

			Expect(agent.Annotations).To(HaveKeyWithValue("foreign.example/retained", "yes"))
		},
		Entry("not observed", corev1.ConditionStatus("")),
		Entry("already finished", corev1.ConditionTrue),
		Entry("waiting for provider", corev1.ConditionFalse),
	)

	It("reports native no-suitable-Agent and pending acquisition conditions", func(ctx SpecContext) {
		c := fake.NewClientBuilder().
			WithScheme(capagentScheme()).
			WithObjects(consumer).
			WithStatusSubresource(consumer).
			Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
		r := &AgentMachineReconciler{Client: c, APIReader: c}
		Expect(r.reportAgent(ctx, consumer, nil)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(consumer), consumer)).To(Succeed())
		reserved := conditions.Get(consumer, agentv1.AgentReservedCondition)
		Expect(reserved.Reason).To(Equal(agentv1.NoSuitableAgentsReason))
		Expect(reserved.Severity).To(Equal(capiv1.ConditionSeverityWarning))
		Expect(consumer.Status.AgentRef).To(BeNil())

		for _, kind := range []capiv1.ConditionType{
			agentv1.AgentSpecSyncedCondition, agentv1.AgentValidatedCondition,
			agentv1.AgentRequirementsMetCondition, agentv1.InstalledCondition,
		} {
			condition := conditions.Get(consumer, kind)
			Expect(condition.Status).To(Equal(corev1.ConditionFalse))
			Expect(condition.Reason).To(Equal(agentv1.AgentNotYetFoundReason))
			Expect(condition.Severity).To(Equal(capiv1.ConditionSeverityInfo))
		}
	})
})
