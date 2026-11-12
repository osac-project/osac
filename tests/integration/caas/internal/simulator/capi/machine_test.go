// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	//nolint:staticcheck // CAPI v1.9.5 uses this failure reason in its v1beta1 status.
	capierrors "sigs.k8s.io/cluster-api/errors"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = DescribeTable("Machine infrastructure adoption", func(ctx SpecContext, variant string) {
	By("preparing a pending clone with its MachineSet and an independent owner")

	scheme := capiScheme()
	set := &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers", UID: "current-set"}}
	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "worker", UID: "current-machine",
		},
		Spec: capiv1.MachineSpec{InfrastructureRef: corev1.ObjectReference{
			APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachine", Namespace: "cp", Name: "worker",
		}},
	}
	Expect(controllerutil.SetControllerReference(set, machine, scheme)).To(Succeed())
	clone := &agentv1.AgentMachine{ObjectMeta: metav1.ObjectMeta{
		Namespace: "cp", Name: "worker",
		OwnerReferences: []metav1.OwnerReference{
			{APIVersion: capiv1.GroupVersion.String(), Kind: "MachineSet", Name: set.Name, UID: set.UID},
			{APIVersion: "v1", Kind: "Secret", Name: "foreign", UID: "preserve-me"},
		},
	}}

	switch variant {
	case "foreign", "previous-machine":
		owner := machine.DeepCopy()
		owner.UID = "old-machine"
		if variant == "foreign" {
			owner.Name, owner.UID = "other", "foreign"
		}

		Expect(controllerutil.SetControllerReference(owner, clone, scheme)).To(Succeed())
	case "adopted":
		Expect(controllerutil.SetControllerReference(machine, clone, scheme)).To(Succeed())
	case "previous-set":
		clone.OwnerReferences[0].UID = "old-set"
	case "template":
		machine.Spec.InfrastructureRef.Kind = "AgentMachineTemplate"
		machine.Spec.InfrastructureRef.Name = "template"
	}

	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: machine.Namespace, Name: "cluster", UID: "cluster"},
	}
	machine.Spec.ClusterName = cluster.Name

	beforeOwners := append([]metav1.OwnerReference{}, clone.OwnerReferences...)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, machine, clone).
		WithStatusSubresource(machine).
		Build()
	r := &MachineReconciler{Client: c, APIReader: c}

	By("adopting only concrete infrastructure from the current MachineSet")

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(machine)})
	if variant == "current" || variant == "adopted" {
		Expect(err).NotTo(HaveOccurred())
	} else {
		Expect(err).To(HaveOccurred())
	}

	Expect(c.Get(ctx, client.ObjectKeyFromObject(clone), clone)).To(Succeed())

	if variant == "current" || variant == "adopted" {
		Expect(clone.OwnerReferences).To(ConsistOf(
			beforeOwners[1], *metav1.NewControllerRef(machine, capiv1.GroupVersion.WithKind("Machine")),
		))

		before := clone.DeepCopy()
		_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(machine)})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(clone), clone)).To(Succeed())
		Expect(clone).To(Equal(before))
	} else {
		Expect(clone.OwnerReferences).To(Equal(beforeOwners))
	}

	By("preserving the Machine's infrastructure reference")

	current := &capiv1.Machine{}
	Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), current)).To(Succeed())
	Expect(current.Spec).To(Equal(machine.Spec))
},
	Entry("hands off a pending clone while preserving unrelated owners", "current"),
	Entry("removes the temporary owner from already adopted infrastructure", "adopted"),
	Entry("rejects another controller's clone", "foreign"),
	Entry("rejects a previous Machine's clone", "previous-machine"),
	Entry("rejects a previous MachineSet's clone", "previous-set"),
	Entry("rejects a template reference", "template"),
)

var _ = DescribeTable("Machine infrastructure controller ownership", func(variant string) {
	machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "worker", UID: "current-machine"}}
	infra := &agentv1.AgentMachine{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "worker"}}
	Expect(controllerutil.SetControllerReference(machine, infra, capiScheme())).To(Succeed())

	switch variant {
	case "namespace":
		infra.Namespace = "other"
	case "missing-uid":
		machine.UID = ""
		infra.OwnerReferences[0].UID = ""
	}

	err := validateInfrastructureOwner(machine, infra)
	if variant == "current" {
		Expect(err).NotTo(HaveOccurred())
	} else {
		Expect(err).To(HaveOccurred())
	}
},
	Entry("accepts the current Machine's clone", "current"),
	Entry("rejects a clone in another namespace", "namespace"),
	Entry("rejects a missing Machine UID", "missing-uid"),
)

var _ = Describe("Machine deletion hooks", func() {
	It("waits for every pre-terminate hook before deleting infrastructure", func(ctx SpecContext) {
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "worker", UID: "original-machine",
				DeletionTimestamp: new(metav1.Now()), Finalizers: []string{machineFinalizer},
				Annotations: map[string]string{
					kube.MachineHook: "", capiv1.PreTerminateDeleteHookAnnotationPrefix + "/external": "",
				},
			},
			Spec: capiv1.MachineSpec{InfrastructureRef: corev1.ObjectReference{
				APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachine", Name: "worker",
			}},
		}
		infra := &agentv1.AgentMachine{ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "worker", UID: "original-provider", Finalizers: []string{"provider.example/cleanup"},
		}}
		Expect(controllerutil.SetControllerReference(machine, infra, capiScheme())).To(Succeed())
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: machine.Namespace, Name: "cluster", UID: "cluster"},
		}
		machine.Spec.ClusterName = cluster.Name
		c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(cluster, machine, infra).
			WithStatusSubresource(machine).Build()
		r := &MachineReconciler{Client: c, APIReader: c}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(machine)}

		for _, hook := range []string{kube.MachineHook, capiv1.PreTerminateDeleteHookAnnotationPrefix + "/external"} {
			By("waiting until the remaining hook owner permits termination")

			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
			Expect(infra.DeletionTimestamp).To(BeNil())
			Expect(c.Get(ctx, request.NamespacedName, machine)).To(Succeed())
			Expect(machine.Finalizers).To(ContainElement(machineFinalizer))
			Expect(conditions.IsFalse(machine, capiv1.PreTerminateDeleteHookSucceededCondition)).To(BeTrue())

			delete(machine.Annotations, hook)
			Expect(c.Update(ctx, machine)).To(Succeed())
		}

		By("requesting provider cleanup after every hook owner has finished")

		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		Expect(infra.UID).To(BeEquivalentTo("original-provider"))
		Expect(infra.DeletionTimestamp).NotTo(BeNil())
	})
})

var _ = Describe("Machine readiness observations", func() {
	DescribeTable("protects Machines before waiting for dependencies or pause", func(ctx SpecContext, variant string) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "worker", UID: "machine"},
			Spec: capiv1.MachineSpec{ClusterName: cluster.Name, InfrastructureRef: corev1.ObjectReference{
				APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachine", Name: "worker",
			}},
		}
		if variant == "paused" {
			machine.Annotations = map[string]string{capiv1.PausedAnnotation: ""}
		}

		builder := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(machine).WithStatusSubresource(machine)
		if variant != "missing Cluster" {
			builder.WithObjects(cluster)
		}

		c := builder.Build()
		r := &MachineReconciler{Client: c, APIReader: c}

		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(machine)})
		if variant == "missing Cluster" {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		} else {
			Expect(err).NotTo(HaveOccurred())
		}

		if variant == "missing infrastructure" {
			Expect(result.RequeueAfter).NotTo(BeZero())
		}

		Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
		Expect(machine.Finalizers).To(ContainElement(machineFinalizer))
		//nolint:staticcheck // Verify the pinned CAPI v1beta1 failure status.
		Expect(machine.Status.FailureReason).To(BeNil())
	}, Entry("paused", "paused"), Entry("missing Cluster", "missing Cluster"),
		Entry("missing infrastructure", "missing infrastructure"))

	DescribeTable(
		"normalizes Cluster metadata while preserving adoption boundaries",
		func(ctx SpecContext, variant string) {
			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "current-cluster"}}
			machine := &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cp",
					Name:      "worker",
					UID:       "original-machine",
					Labels:    map[string]string{"retained": "yes"},
				},
				Spec: capiv1.MachineSpec{ClusterName: cluster.Name},
			}

			switch variant {
			case "previous Cluster":
				previous := cluster.DeepCopy()
				previous.UID = "previous-cluster"
				Expect(controllerutil.SetOwnerReference(previous, machine, capiScheme())).To(Succeed())
			case "controller":
				set := &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers", UID: "original-set"}}
				Expect(controllerutil.SetControllerReference(set, machine, capiScheme())).To(Succeed())
			case "set label":
				machine.Labels[capiv1.MachineSetNameLabel] = "workers"
			case "pool label":
				machine.Labels[capiv1.MachinePoolNameLabel] = "workers"
			case "control-plane label":
				machine.Labels[capiv1.MachineControlPlaneNameLabel] = "control-plane"
			}

			c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(cluster, machine).Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			before := machine.DeepCopy()
			r := &MachineReconciler{Client: c, APIReader: c}

			err := r.normalizeMachineMetadata(ctx, machine, cluster)
			if variant == "previous Cluster" {
				Expect(err).To(HaveOccurred())
				Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
				Expect(machine).To(Equal(before))
				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(machine.Labels).To(HaveKeyWithValue(capiv1.ClusterNameLabel, cluster.Name))
			Expect(machine.Labels).To(HaveKeyWithValue("retained", "yes"))

			if variant == "standalone" {
				Expect(kube.MatchesOwner(machine, cluster, capiv1.GroupVersion.WithKind("Cluster"))).To(BeTrue())
			} else {
				Expect(machine.OwnerReferences).To(Equal(before.OwnerReferences))
			}

			before = machine.DeepCopy()
			Expect(r.normalizeMachineMetadata(ctx, machine, cluster)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(machine).To(Equal(before))
		},
		Entry("standalone Machine", "standalone"),
		Entry("previous Cluster owner", "previous Cluster"),
		Entry("MachineSet controller", "controller"),
		Entry("pending MachineSet adoption", "set label"),
		Entry("pending MachinePool adoption", "pool label"),
		Entry("pending control-plane adoption", "control-plane label"),
	)

	It("separates current provider conditions from sticky infrastructure and bootstrap readiness", func(ctx SpecContext) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original-cluster"}}
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp",
				Name:      "worker",
				UID:       "original",
				Labels:    map[string]string{kube.ManagedByLabel: kube.ManagerName},
			},
			Spec: capiv1.MachineSpec{
				ClusterName: cluster.Name,
				Bootstrap:   capiv1.Bootstrap{DataSecretName: new("bootstrap")},
				InfrastructureRef: corev1.ObjectReference{
					APIVersion: agentv1.GroupVersion.String(),
					Kind:       "AgentMachine",
					Name:       "worker",
				},
			},
		}
		infra := &agentv1.AgentMachine{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "worker", UID: "original-provider"},
			Spec:       agentv1.AgentMachineSpec{ProviderID: new("agent://host")},
		}
		Expect(controllerutil.SetControllerReference(machine, infra, capiScheme())).To(Succeed())

		c := fake.NewClientBuilder().
			WithScheme(capiScheme()).
			WithObjects(cluster, machine, infra).
			WithStatusSubresource(machine, infra).
			Build()
		r := &MachineReconciler{Client: c, APIReader: c}
		for i, ready := range []bool{false, true, false} {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())

			infra.Status.Ready = ready
			if ready {
				conditions.MarkTrue(infra, capiv1.ReadyCondition)
			} else {
				conditions.MarkFalse(
					infra,
					capiv1.ReadyCondition,
					"ProviderWaiting",
					capiv1.ConditionSeverityInfo,
					"Waiting for provider observation",
				)
			}

			Expect(c.Status().Update(ctx, infra)).To(Succeed())
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(machine)})
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(kube.MatchesOwner(machine, cluster, capiv1.GroupVersion.WithKind("Cluster"))).To(BeTrue())
			Expect(metav1.GetControllerOf(machine)).To(BeNil())
			Expect(machine.Labels).To(HaveKeyWithValue(capiv1.ClusterNameLabel, cluster.Name))
			Expect(machine.Status.BootstrapReady).To(BeTrue())
			Expect(machine.Status.InfrastructureReady).To(Equal(i > 0))
			Expect(conditions.IsTrue(machine, capiv1.InfrastructureReadyCondition)).To(Equal(ready))
			Expect(conditions.IsTrue(machine, capiv1.ReadyCondition)).To(Equal(ready))

			if i > 0 {
				Expect(machine.Spec.ProviderID).To(Equal(new("agent://host")))
			}
		}

		By("reporting infrastructure loss after provisioning")
		Expect(c.Delete(ctx, infra)).To(Succeed())
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(machine)})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
		Expect(machine.Status.InfrastructureReady).To(BeTrue())
		//nolint:staticcheck // Verify the pinned CAPI v1beta1 failure status.
		Expect(machine.Status.FailureReason).To(Equal(new(capierrors.InvalidConfigurationMachineError)))
		//nolint:staticcheck // Verify the pinned CAPI v1beta1 failure status.
		Expect(machine.Status.FailureMessage).To(HaveValue(ContainSubstring("has been deleted after being ready")))
	})
})
