// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capi

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = Describe("MachineSet workers", func() {
	DescribeTable("updates readiness when replica reconciliation fails", func(ctx SpecContext, variant string) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}

		set := &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers", UID: "set"},
			Spec: capiv1.MachineSetSpec{
				ClusterName: cluster.Name, Replicas: new(int32(2)),
				Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{InfrastructureRef: corev1.ObjectReference{
					APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachineTemplate", Name: "template",
				}}},
			},
		}
		if variant == "delete" {
			set.Spec.Replicas = new(int32(0))
		}

		for _, kind := range []capiv1.ConditionType{capiv1.MachinesCreatedCondition, capiv1.ReadyCondition} {
			conditions.MarkTrue(set, kind)
		}

		machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers-0", UID: "machine"}}
		Expect(controllerutil.SetControllerReference(set, machine, capiScheme())).To(Succeed())
		conditions.MarkTrue(machine, capiv1.ReadyCondition)

		template := &agentv1.AgentMachineTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "template"}}
		failure, refuse := errors.New("replica write failed"), true

		builder := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(cluster, set, machine).
			WithStatusSubresource(set).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, object client.Object, options ...client.CreateOption) error {
				if _, worker := object.(*capiv1.Machine); worker && refuse && variant == "create" {
					return failure
				}

				return c.Create(ctx, object, options...)
			},
			Delete: func(ctx context.Context, c client.WithWatch, object client.Object, options ...client.DeleteOption) error {
				if _, worker := object.(*capiv1.Machine); worker && refuse && variant == "delete" {
					return failure
				}

				return c.Delete(ctx, object, options...)
			},
		})
		if variant != "template" {
			builder.WithObjects(template)
		}

		c := builder.Build()
		r := &MachineSetReconciler{Client: c, APIReader: c}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}
		_, err := r.Reconcile(ctx, request)
		Expect(err).To(HaveOccurred())

		if variant != "template" {
			Expect(errors.Is(err, failure)).To(BeTrue())
		}

		Expect(c.Get(ctx, request.NamespacedName, set)).To(Succeed())
		Expect(conditions.IsTrue(set, capiv1.MachinesCreatedCondition)).To(Equal(variant == "delete"))
		Expect(conditions.IsFalse(set, capiv1.ReadyCondition)).To(BeTrue())

		refuse = false

		if variant == "template" {
			Expect(c.Create(ctx, template)).To(Succeed())
		}

		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, set)).To(Succeed())
		Expect(conditions.IsTrue(set, capiv1.MachinesCreatedCondition)).To(BeTrue())
	}, Entry("template lookup", "template"), Entry("Machine creation", "create"), Entry("scale-down deletion", "delete"))

	DescribeTable("reads the referenced template namespace and clones into the worker namespace",
		func(ctx SpecContext, namespace string) {
			set := &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers", UID: "original-set"},
				Spec: capiv1.MachineSetSpec{
					ClusterName: "cluster",
					Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{InfrastructureRef: corev1.ObjectReference{
						APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachineTemplate", Namespace: namespace, Name: "template",
					}}},
				},
			}
			template := &agentv1.AgentMachineTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "template"}}

			template.Spec.Template.Spec.AgentLabelSelector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"selected": "yes"},
			}
			objects := []client.Object{set, template}
			if namespace != "" {
				template.Namespace = namespace
				local := template.DeepCopy()
				local.Namespace = set.Namespace
				local.Spec.Template.Spec.AgentLabelSelector.MatchLabels["selected"] = "wrong"
				objects = append(objects, local)
			}

			c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(objects...).Build()
			r := &MachineSetReconciler{Client: c, APIReader: c}
			Expect(r.createMachine(ctx, set, "workers-0")).To(Succeed())

			clone := &agentv1.AgentMachine{}
			Expect(c.Get(ctx, client.ObjectKey{Namespace: set.Namespace, Name: "workers-0"}, clone)).To(Succeed())
			Expect(clone.Spec).To(Equal(template.Spec.Template.Spec))
			Expect(kube.MatchesOwner(clone, set, capiv1.GroupVersion.WithKind("MachineSet"))).To(BeTrue())
		}, Entry("default namespace", ""), Entry("explicit remote namespace wins over a local namesake", "templates"))

	DescribeTable("handles replica inputs without silently scaling live nil replicas",
		func(ctx SpecContext, replicas *int32, deleting bool) {
			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}

			set := &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "cp",
					Name:       "workers",
					UID:        "set",
					Finalizers: []string{capiv1.MachineSetFinalizer},
				},
				Spec: capiv1.MachineSetSpec{
					ClusterName: cluster.Name, Replicas: replicas,
					Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{InfrastructureRef: corev1.ObjectReference{
						APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachineTemplate", Name: "template",
					}}},
				},
			}
			if deleting {
				set.DeletionTimestamp = new(metav1.Now())
			}

			machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "workers-0", UID: "machine", Finalizers: []string{machineFinalizer},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(set, capiv1.GroupVersion.WithKind("MachineSet"))},
			}}
			c := fake.NewClientBuilder().
				WithScheme(capiScheme()).
				WithObjects(cluster, set, machine).
				WithStatusSubresource(set).
				Build()
			r := &MachineSetReconciler{Client: c, APIReader: c}

			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
			if replicas == nil && !deleting {
				Expect(err).To(MatchError("MachineSet spec.replicas is required for scaling"))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(machine.UID).To(BeEquivalentTo("machine"))
			Expect(machine.DeletionTimestamp != nil).To(Equal(replicas != nil || deleting))
		},
		Entry("live nil replicas preserve Machines", nil, false),
		Entry("explicit zero deletes excess Machines", new(int32(0)), false),
		Entry("negative replicas bound deletion at zero", new(int32(-1)), false),
		Entry("terminating nil replicas still clean up", nil, true),
	)

	It("converges sparse worker names after double-digit scale-down and subsequent growth", func(ctx SpecContext) {
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}
		set := &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers", UID: "original-set"},
			Spec: capiv1.MachineSetSpec{
				ClusterName: cluster.Name, Replicas: new(int32(3)),
				Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
					ClusterName: cluster.Name,
					InfrastructureRef: corev1.ObjectReference{
						APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachineTemplate", Name: "workers",
					},
				}},
			},
		}
		template := &agentv1.AgentMachineTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers"}}
		objects := []client.Object{cluster, set, template}

		for i := range 11 {
			objects = append(objects, &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: fmt.Sprintf("workers-%d", i), UID: types.UID(fmt.Sprintf("machine-%d", i)),
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(set, capiv1.GroupVersion.WithKind("MachineSet"))},
			}})
		}

		c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(objects...).WithStatusSubresource(set).Build()
		r := &MachineSetReconciler{Client: c, APIReader: c}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}

		By("retaining exactly three original Machines despite gaps in their ordinals")

		for range 3 {
			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			machines, err := children(ctx, c, set)
			Expect(err).NotTo(HaveOccurred())
			Expect(machines).To(ConsistOf(HaveField("UID", types.UID("machine-0")),
				HaveField("UID", types.UID("machine-1")), HaveField("UID", types.UID("machine-10"))))
		}

		By("creating only the missing replica when demand grows")

		Expect(c.Get(ctx, request.NamespacedName, set)).To(Succeed())
		set.Spec.Replicas = new(int32(4))
		Expect(c.Update(ctx, set)).To(Succeed())

		for range 2 {
			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			machines, err := children(ctx, c, set)
			Expect(err).NotTo(HaveOccurred())
			Expect(machines).To(ConsistOf(HaveField("Name", "workers-0"), HaveField("Name", "workers-1"),
				HaveField("Name", "workers-10"), HaveField("Name", "workers-2")))
		}
	})

	DescribeTable(
		"checks existing Machines before reading templates or creating clones",
		func(ctx SpecContext, original bool) {
			set := &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers", UID: "original-set"}}
			machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "workers-0", UID: "original-machine",
				Annotations:     map[string]string{"foreign.example/retained": "yes"},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(set, capiv1.GroupVersion.WithKind("MachineSet"))},
			}, Spec: capiv1.MachineSpec{ProviderID: new("agent://installed")}}
			if !original {
				machine.OwnerReferences[0].UID = "another-set"
			}

			c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(machine).Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			before := machine.DeepCopy()
			r := &MachineSetReconciler{Client: c, APIReader: c}

			err := r.createMachine(ctx, set, machine.Name)
			if original {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(MatchError(ContainSubstring("another MachineSet incarnation")))
			}

			Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(machine).To(Equal(before))

			var clones agentv1.AgentMachineList

			Expect(c.List(ctx, &clones)).To(Succeed())
			Expect(clones.Items).To(BeEmpty())
		},
		Entry("original Machine", true),
		Entry("foreign Machine", false),
	)

	DescribeTable("finds children only for the current controller", func(ctx SpecContext, variant string) {
		parent := &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{
			Namespace: "control-plane", Name: "workers", UID: "current-parent",
		}}
		current := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
			Namespace: parent.Namespace,
			Name:      "current",
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(parent, capiv1.GroupVersion.WithKind("MachineSet")),
			},
		}}
		old := current.DeepCopy()
		old.Name, old.OwnerReferences[0].UID = "old", "old-parent"
		foreign := current.DeepCopy()
		foreign.Name, foreign.OwnerReferences[0].Controller = "foreign", new(false)

		switch variant {
		case "version":
			current.OwnerReferences[0].APIVersion = "cluster.x-k8s.io/v1beta2"
		case "uid":
			parent.UID, current.OwnerReferences[0].UID = "", ""
		}

		c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(parent, current, old, foreign).Build()
		found, err := children(ctx, c, parent)
		Expect(err).NotTo(HaveOccurred())

		if variant == "current" {
			Expect(found).To(HaveLen(1))
			Expect(found[0].Name).To(Equal(current.Name))
		} else {
			Expect(found).To(BeEmpty())
		}
	},
		Entry("matching controller", "current"),
		Entry("another controller API version", "version"),
		Entry("missing parent UID", "uid"),
	)

	DescribeTable("recovers a pending clone after interrupted Machine creation", func(ctx SpecContext, changed bool) {
		By("interrupting the Machine create after its infrastructure clone is persisted")

		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"},
		}
		set := &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp",
				Name:      "workers",
				UID:       "original-set",
			},
			Spec: capiv1.MachineSetSpec{
				ClusterName: "cluster", Replicas: new(int32(2)),
				Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
					ClusterName: "incorrect-template-cluster",
					InfrastructureRef: corev1.ObjectReference{
						APIVersion: agentv1.GroupVersion.String(), Kind: "AgentMachineTemplate", Namespace: "cp", Name: "workers",
					},
					Bootstrap: capiv1.Bootstrap{DataSecretName: new("bootstrap")},
				}},
			},
		}
		template := &agentv1.AgentMachineTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers"}}
		failCreate := true
		failure := errors.New("interrupted Machine create")
		created := 0
		c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(set, template, cluster).
			WithStatusSubresource(set).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption) error {
					if _, machine := object.(*capiv1.Machine); machine && failCreate {
						return failure
					}

					// Give each persisted incarnation its own identity, including same-name replacements.
					created++
					object.SetUID(types.UID(fmt.Sprintf("created-%d", created)))

					return c.Create(ctx, object, opts...)
				},
			}).Build()
		r := &MachineSetReconciler{Client: c, APIReader: c}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}
		_, err := r.Reconcile(ctx, request)
		Expect(errors.Is(err, failure)).To(BeTrue(), "original failure lost: %v", err)
		Expect(c.Get(ctx, request.NamespacedName, set)).To(Succeed())
		Expect(kube.MatchesOwner(set, cluster, capiv1.GroupVersion.WithKind("Cluster"))).To(BeTrue())
		Expect(metav1.GetControllerOf(set)).To(BeNil(), "standalone MachineSets use ordinary Cluster ownership")
		Expect(set.Finalizers).To(ContainElement(capiv1.MachineSetFinalizer))
		Expect(set.Finalizers).NotTo(ContainElement("machineset.cluster.x-k8s.io"))

		pending := &agentv1.AgentMachine{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: "cp", Name: "workers-0"}, pending)).To(Succeed())
		Expect(pending.UID).NotTo(BeEmpty())
		Expect(metav1.GetControllerOf(pending)).To(BeNil())
		Expect(pending.OwnerReferences).NotTo(BeEmpty())
		Expect(pending.OwnerReferences[0].UID).To(Equal(set.UID))

		By("refusing to reuse the pending clone for a replacement MachineSet")

		missing := &capiv1.Machine{}
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(pending), missing))).To(BeTrue())
		beforeClone := pending.DeepCopy()
		replacement := set.DeepCopy()
		replacement.UID = "replacement-set"
		restarted := &MachineSetReconciler{Client: c, APIReader: c}
		err = restarted.createMachine(ctx, replacement, pending.Name)
		Expect(err).To(MatchError(ContainSubstring("AgentMachine workers-0 belongs to another MachineSet incarnation")))

		current := &agentv1.AgentMachine{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(pending), current)).To(Succeed())
		Expect(current.UID).To(Equal(beforeClone.UID))
		Expect(current.OwnerReferences).To(Equal(beforeClone.OwnerReferences))

		By("recovering from native state through a fresh reconciler")

		set.Labels[capiv1.ClusterNameLabel] = "stale"
		set.Spec.Selector.MatchLabels[capiv1.ClusterNameLabel] = "stale"
		set.Spec.Template.Labels[capiv1.ClusterNameLabel] = "stale"
		set.Spec.Selector.MatchLabels["retained"] = "yes"
		set.Spec.Template.Labels["retained"] = "yes"
		Expect(c.Update(ctx, set)).To(Succeed())

		if changed {
			By("retiring the orphaned clone after its template changes")

			template.Spec.Template.Spec.AgentLabelSelector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"generation": "new"},
			}
			Expect(c.Update(ctx, template)).To(Succeed())
			_, err = restarted.Reconcile(ctx, request)
			Expect(err).To(MatchError(ContainSubstring("stale AgentMachine workers-0 is being deleted")))
			Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(pending), &agentv1.AgentMachine{}))).To(BeTrue())
		}

		failCreate = false
		_, err = restarted.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, set)).To(Succeed())
		Expect(set.Labels).To(HaveKeyWithValue(capiv1.ClusterNameLabel, cluster.Name))
		Expect(
			set.Spec.Selector.MatchLabels,
		).To(Equal(map[string]string{capiv1.ClusterNameLabel: cluster.Name, "retained": "yes"}))
		Expect(set.Spec.Template.Labels).To(Equal(set.Spec.Selector.MatchLabels))

		var machines capiv1.MachineList

		Expect(c.List(ctx, &machines, client.InNamespace("cp"))).To(Succeed())
		Expect(machines.Items).To(HaveLen(2))

		for i := range machines.Items {
			Expect(machines.Items[i].Spec.ClusterName).To(Equal(cluster.Name))
			Expect(machines.Items[i].Labels).To(HaveKeyWithValue(capiv1.ClusterNameLabel, cluster.Name))
			Expect(machines.Items[i].Spec.InfrastructureRef.Kind).To(Equal("AgentMachine"))
			Expect(machines.Items[i].Spec.InfrastructureRef.Name).To(Equal(machines.Items[i].Name))
		}

		var clones agentv1.AgentMachineList

		Expect(c.List(ctx, &clones, client.InNamespace("cp"))).To(Succeed())
		Expect(clones.Items).To(HaveLen(2))

		recovered := &agentv1.AgentMachine{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(pending), recovered)).To(Succeed())

		if changed {
			Expect(recovered.UID).NotTo(Equal(pending.UID))
		} else {
			Expect(recovered.UID).To(Equal(pending.UID))
		}

		Expect(recovered.Spec).To(Equal(template.Spec.Template.Spec))
	},
		Entry("unchanged template reuses the original clone", false),
		Entry("changed template recreates only the pending clone", true),
	)
})
