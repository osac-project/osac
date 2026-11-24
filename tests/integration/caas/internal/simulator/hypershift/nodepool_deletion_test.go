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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = Describe("NodePool deletion", func() {
	var (
		pool             *hypershiftv1.NodePool
		cluster          *capiv1.Cluster
		set              *capiv1.MachineSet
		template         *agentv1.AgentMachineTemplate
		bootstrap, token *corev1.Secret
	)

	BeforeEach(func() {
		pool = &hypershiftv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub", Name: "workers", UID: "pool", DeletionTimestamp: new(metav1.Now()),
				Finalizers: []string{nodePoolFinalizer, "foreign.example/retain"},
			},
			Spec: hypershiftv1.NodePoolSpec{
				ClusterName: "cluster", Platform: hypershiftv1.NodePoolPlatform{Type: hypershiftv1.AgentPlatform},
			},
		}
		cluster = &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{
			Namespace: "hub-cluster", Name: "cluster", UID: "cluster",
		}}
		set = &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: pool.Name, UID: "set",
			Annotations: map[string]string{kube.NodePoolAnnotation: "hub/workers", capiv1.PausedAnnotation: "true"},
			Finalizers:  []string{"foreign.example/retain"},
		}}
		template = &agentv1.AgentMachineTemplate{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: pool.Name, UID: "template",
			Annotations: map[string]string{kube.NodePoolAnnotation: "hub/workers"},
			Finalizers:  []string{"foreign.example/retain"},
		}}
		bootstrap = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: pool.Name + "-bootstrap", UID: "bootstrap",
			Annotations: map[string]string{kube.NodePoolAnnotation: "hub/workers"},
		}, Data: map[string][]byte{"value": []byte("bootstrap input")}}
		token = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: pool.Name + "-token", UID: "token",
			Annotations: map[string]string{kube.NodePoolAnnotation: "hub/workers"},
		}, Data: map[string][]byte{"token": []byte("worker input")}}
		Expect(controllerutil.SetOwnerReference(cluster, set, hypershiftScheme())).To(Succeed())

		for _, input := range []client.Object{template, bootstrap, token} {
			Expect(controllerutil.SetControllerReference(cluster, input, hypershiftScheme())).To(Succeed())
		}
	})

	DescribeTable("uses surviving native parents to bound orphan cleanup",
		func(ctx SpecContext, variant string, retained bool, wantError string) {
			cluster.Annotations = map[string]string{kube.HostedClusterAnnotation: "hub/cluster"}
			cp := &hypershiftv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
				Namespace: cluster.Namespace, Name: "cluster", UID: "control-plane",
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			}}
			cp.Spec.Platform = hypershiftv1.PlatformSpec{
				Type: hypershiftv1.NonePlatform, Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "foreign"},
			}
			Expect(controllerutil.SetControllerReference(cluster, cp, hypershiftScheme())).To(Succeed())
			cluster.Spec.ControlPlaneRef = &corev1.ObjectReference{
				APIVersion: hypershiftv1.GroupVersion.String(), Kind: "HostedControlPlane",
				Namespace: cp.Namespace, Name: cp.Name, UID: cp.UID,
			}

			switch variant {
			case "served":
				cp.Spec.Platform.Agent.AgentNamespace = "hub"
			case "missing Agent":
				cp.Spec.Platform.Agent = nil
			case "foreign source":
				cp.Annotations[kube.HostedClusterAnnotation] = "other/cluster"
			case "foreign Cluster source":
				cluster.Annotations[kube.HostedClusterAnnotation] = "other/cluster"
			case "foreign HCP namespace":
				cp.Namespace = "other"
			case "empty HCP UID":
				cp.UID = ""
			case "foreign owner version":
				cp.OwnerReferences[0].APIVersion = "foreign.example/v1"
			case "implicit ref namespace":
				cluster.Spec.ControlPlaneRef.Namespace = ""
			case "missing ref":
				cluster.Spec.ControlPlaneRef = nil
			case "unadopted HCP":
				cp.OwnerReferences = nil
			case "old Cluster UID":
				cp.OwnerReferences[0].UID = "previous-cluster"
			case "wrong ref kind":
				cluster.Spec.ControlPlaneRef.Kind = "OtherControlPlane"
			case "wrong ref version":
				cluster.Spec.ControlPlaneRef.APIVersion = "foreign.example/v1"
			case "wrong ref namespace":
				cluster.Spec.ControlPlaneRef.Namespace = "other"
			case "wrong ref name":
				cluster.Spec.ControlPlaneRef.Name = "other"
			case "wrong ref UID":
				cluster.Spec.ControlPlaneRef.UID = "previous-control-plane"
			}

			objects := []client.Object{pool, set, template, bootstrap, token}
			if variant != "missing Cluster" {
				objects = append(objects, cluster)
			}

			if variant != "missing HCP" {
				objects = append(objects, cp)
			}

			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(objects...).Build()
			originals := make([]client.Object, len(objects))

			for i, object := range objects {
				Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
				originals[i] = object.DeepCopyObject().(client.Object)
			}

			reader := interceptor.NewClient(c, interceptor.Funcs{
				Get: func(ctx context.Context, inner client.WithWatch, key client.ObjectKey,
					object client.Object, opts ...client.GetOption,
				) error {
					switch object.(type) {
					case *hypershiftv1.HostedControlPlane:
						if variant == "HCP read" {
							return errors.New("scope read failed")
						}
					case *capiv1.Cluster:
						if variant == "Cluster read" {
							return errors.New("scope read failed")
						}
					}

					return inner.Get(ctx, key, object, opts...)
				},
			})
			r := &NodePoolReconciler{
				Client: c, APIReader: reader, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)}

			_, err := r.Reconcile(ctx, request)
			if wantError != "" {
				Expect(err).To(MatchError(ContainSubstring(wantError)))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(c.Get(ctx, request.NamespacedName, pool)).To(Succeed())

			if retained {
				for i, object := range objects {
					Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
					Expect(object).To(Equal(originals[i]))
				}
			} else {
				Expect(pool.Finalizers).To(ConsistOf("foreign.example/retain"))

				for i, object := range objects {
					switch object.(type) {
					case *capiv1.Cluster, *hypershiftv1.HostedControlPlane:
						Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
						Expect(object).To(Equal(originals[i]))
					}
				}
			}

			if variant == "foreign" || wantError != "" {
				By("resuming cleanup after the surviving native inputs are repaired")

				r.APIReader = c
				cluster.Spec.ControlPlaneRef = &corev1.ObjectReference{
					APIVersion: hypershiftv1.GroupVersion.String(), Kind: "HostedControlPlane",
					Namespace: cp.Namespace, Name: cp.Name, UID: cp.UID,
				}
				Expect(c.Update(ctx, cluster)).To(Succeed())

				cp.Spec.Platform.Agent.AgentNamespace = "hub"
				Expect(c.Update(ctx, cp)).To(Succeed())
				Expect(r.poolsForCluster(ctx, cp)).To(ConsistOf(request))
				_, err = r.Reconcile(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(c.Get(ctx, request.NamespacedName, pool)).To(Succeed())
				Expect(pool.Finalizers).To(ConsistOf("foreign.example/retain"))
			}
		},
		Entry("known foreign Agent namespace", "foreign", true, ""),
		Entry("unsupported Agent settings", "missing Agent", true, ""),
		Entry("served HCP", "served", false, ""),
		Entry("missing HCP", "missing HCP", false, ""),
		Entry("missing Cluster", "missing Cluster", false, ""),
		Entry("unassociated HCP", "foreign source", false, ""),
		Entry("unassociated Cluster", "foreign Cluster source", false, ""),
		Entry("foreign HCP namespace", "foreign HCP namespace", false, ""),
		Entry("missing HCP identity", "empty HCP UID", false, ""),
		Entry("foreign controller API", "foreign owner version", false, ""),
		Entry("implicit native reference namespace", "implicit ref namespace", true, ""),
		Entry("missing native reference", "missing ref", true, "surviving Cluster"),
		Entry("pending HCP adoption", "unadopted HCP", false, ""),
		Entry("replaced Cluster", "old Cluster UID", false, ""),
		Entry("conflicting HCP kind", "wrong ref kind", true, "surviving Cluster"),
		Entry("conflicting HCP version", "wrong ref version", true, "surviving Cluster"),
		Entry("conflicting HCP namespace", "wrong ref namespace", true, "surviving Cluster"),
		Entry("conflicting HCP name", "wrong ref name", true, "surviving Cluster"),
		Entry("conflicting HCP UID", "wrong ref UID", true, "surviving Cluster"),
		Entry("HCP read error", "HCP read", true, "scope read failed"),
		Entry("Cluster read error", "Cluster read", true, "scope read failed"),
	)

	DescribeTable("finalizes with a paused empty MachineSet and pending template deletion",
		func(ctx SpecContext, retainedSecrets bool) {
			if retainedSecrets {
				bootstrap.Finalizers = []string{"foreign.example/retain"}
				token.Finalizers = []string{"foreign.example/retain"}
			}

			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).
				WithObjects(pool, cluster, set, template, bootstrap, token).Build()
			r := &NodePoolReconciler{Client: c, APIReader: c}
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			Expect(pool.Finalizers).To(ConsistOf("foreign.example/retain"))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(set), set)).To(Succeed())
			Expect(set.DeletionTimestamp).NotTo(BeNil())
			Expect(set.Annotations).To(HaveKeyWithValue(capiv1.PausedAnnotation, "true"))
			Expect(set.Finalizers).To(ConsistOf("foreign.example/retain"))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(template), template)).To(Succeed())
			Expect(template.DeletionTimestamp).NotTo(BeNil())
			Expect(template.Finalizers).To(ConsistOf("foreign.example/retain"))

			for _, secret := range []*corev1.Secret{bootstrap, token} {
				err := c.Get(ctx, client.ObjectKeyFromObject(secret), secret)
				if retainedSecrets {
					Expect(err).NotTo(HaveOccurred())
					Expect(secret.DeletionTimestamp).NotTo(BeNil())
					Expect(secret.Finalizers).To(ConsistOf("foreign.example/retain"))
				} else {
					Expect(apierrors.IsNotFound(err)).To(BeTrue())
				}
			}
		}, Entry("ordinary Secrets", false), Entry("Secrets with foreign finalizers", true))

	DescribeTable("retains bootstrap inputs while associated Machines remain", func(ctx SpecContext, variant string) {
		machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
			Namespace: set.Namespace, Name: "worker", UID: "machine",
			Annotations: map[string]string{kube.NodePoolAnnotation: "hub/workers"},
		}}
		Expect(controllerutil.SetControllerReference(set, machine, hypershiftScheme())).To(Succeed())

		var sibling *capiv1.Machine

		objects := []client.Object{pool, cluster, template, bootstrap, token, machine}
		if variant == "two workers" {
			sibling = machine.DeepCopy()
			sibling.Name, sibling.UID = "worker-2", "machine-2"
			objects = append(objects, sibling)
		}

		if variant != "set absent" {
			objects = append(objects, set)
		}

		if variant == "unannotated" {
			machine.Annotations = nil
		}

		if variant == "deleting" {
			machine.DeletionTimestamp = new(metav1.Now())
			machine.Finalizers = []string{capiv1.MachineFinalizer}
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(objects...).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(bootstrap), bootstrap)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
		originalBootstrap, originalToken, originalMachine := bootstrap.DeepCopy(), token.DeepCopy(), machine.DeepCopy()
		r := &NodePoolReconciler{Client: c, APIReader: c}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)}
		result, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Finalizers).To(ContainElement(nodePoolFinalizer))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(bootstrap), bootstrap)).To(Succeed())
		Expect(bootstrap).To(Equal(originalBootstrap))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(token).To(Equal(originalToken))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
		Expect(machine).To(Equal(originalMachine))

		if variant != "deleting" {
			By("observing completed Machine deletion while the paused set can remain")

			Expect(c.Delete(ctx, machine)).To(Succeed())

			if sibling != nil {
				result, err = r.Reconcile(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				Expect(c.Get(ctx, client.ObjectKeyFromObject(bootstrap), bootstrap)).To(Succeed())
				Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
				Expect(c.Delete(ctx, sibling)).To(Succeed())
			}

			result, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
			Expect(pool.Finalizers).To(ConsistOf("foreign.example/retain"))
			Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(bootstrap), bootstrap))).To(BeTrue())
			Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(token), token))).To(BeTrue())
		}
	}, Entry("annotated worker", "annotated"), Entry("all workers must disappear", "two workers"),
		Entry("MachineSet already absent", "set absent"),
		Entry("existing unannotated worker", "unannotated"), Entry("deleting worker", "deleting"))

	DescribeTable("preserves foreign inputs during deletion", func(ctx SpecContext, variant string) {
		var foreign client.Object

		switch variant {
		case "set", "Cluster UID", "foreign controller":
			foreign = set
		case "template":
			foreign = template
		case "bootstrap":
			foreign = bootstrap
		case "token":
			foreign = token
		}

		switch variant {
		case "Cluster UID":
			set.OwnerReferences[0].UID = "previous-cluster"
			bootstrap.OwnerReferences[0].UID = "previous-cluster"
			token.OwnerReferences[0].UID = "previous-cluster"
		case "foreign controller":
			set.OwnerReferences[0].Controller = nil
			parent := &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{
				Namespace: set.Namespace, Name: "deployment", UID: "foreign-deployment",
			}}
			Expect(controllerutil.SetControllerReference(parent, set, hypershiftScheme())).To(Succeed())
		default:
			foreign.SetAnnotations(map[string]string{kube.NodePoolAnnotation: "hub/other"})
		}

		objects := []client.Object{pool, set, template, bootstrap, token}
		if variant != "foreign controller" {
			objects = append(objects, cluster)
		}

		preserved := []client.Object{foreign}
		if variant == "foreign controller" || variant == "Cluster UID" {
			preserved = append(preserved, bootstrap, token)
		}

		if foreign == set {
			machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
				Namespace: set.Namespace, Name: "foreign-worker", UID: "foreign-machine",
				Annotations: map[string]string{kube.NodePoolAnnotation: set.Annotations[kube.NodePoolAnnotation]},
			}}
			machine.Spec.Bootstrap.DataSecretName = new(bootstrap.Name)
			if variant == "foreign controller" {
				machine.Annotations = nil
			}

			Expect(controllerutil.SetControllerReference(set, machine, hypershiftScheme())).To(Succeed())
			objects = append(objects, machine)
			preserved = append(preserved, machine)
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(objects...).Build()
		originals := make([]client.Object, len(preserved))
		for i, object := range preserved {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
			originals[i] = object.DeepCopyObject().(client.Object)
		}

		r := &NodePoolReconciler{Client: c, APIReader: c}
		if variant == "Cluster UID" {
			r.APIReader = interceptor.NewClient(c, interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					// The replacement disappears after the MachineSet lookup, before Secret cleanup.
					if _, machines := list.(*capiv1.MachineList); machines {
						Expect(c.Delete(ctx, cluster)).To(Succeed())
					}

					return c.List(ctx, list, opts...)
				},
			})
		}

		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())

		if variant == "foreign controller" {
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(pool.Finalizers).To(ConsistOf(nodePoolFinalizer, "foreign.example/retain"))
		} else {
			Expect(pool.Finalizers).To(ConsistOf("foreign.example/retain"))
		}

		for i, object := range preserved {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
			Expect(object).To(Equal(originals[i]))
		}
	}, Entry("foreign MachineSet", "set"), Entry("foreign template", "template"),
		Entry("foreign bootstrap", "bootstrap"), Entry("foreign token", "token"),
		Entry("MachineSet and Secrets from a previous Cluster even if the replacement disappears", "Cluster UID"),
		Entry("pool-linked MachineSet with a foreign controller and unannotated worker", "foreign controller"))

	DescribeTable("leaves unrelated Machines untouched", func(ctx SpecContext, variant string) {
		machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
			Namespace: set.Namespace, Name: "foreign-worker", UID: "foreign-machine",
		}}
		Expect(controllerutil.SetControllerReference(set, machine, hypershiftScheme())).To(Succeed())

		switch variant {
		case "annotation":
			machine.OwnerReferences = nil
			machine.Annotations = map[string]string{kube.NodePoolAnnotation: "hub/other"}
		case "namespace":
			machine.Namespace = "other-cluster"
			machine.Annotations = map[string]string{kube.NodePoolAnnotation: "hub/workers"}
		case "UID":
			machine.OwnerReferences[0].UID = "previous-set"
		case "name":
			machine.OwnerReferences[0].Name = "other-set"
		case "version":
			machine.OwnerReferences[0].APIVersion = "foreign.example/v1"
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).
			WithObjects(pool, cluster, set, template, bootstrap, token, machine).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
		before := machine.DeepCopy()
		r := &NodePoolReconciler{Client: c, APIReader: c}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Finalizers).To(ConsistOf("foreign.example/retain"))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
		Expect(machine).To(Equal(before))
	}, Entry("another pool", "annotation"), Entry("another namespace", "namespace"),
		Entry("previous set UID", "UID"), Entry("other set name", "name"), Entry("other owner API", "version"))

	DescribeTable("propagates deletion errors and recovers on the next pass", func(ctx SpecContext, stage string) {
		failure, refuse := errors.New("input operation failed"), true
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).
			WithObjects(pool, cluster, set, template, bootstrap, token).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
					object client.Object, opts ...client.GetOption,
				) error {
					_, workerSet := object.(*capiv1.MachineSet)
					if refuse && ((stage == "set read" && workerSet) ||
						(stage == "Cluster read" && key == client.ObjectKeyFromObject(cluster))) {
						return failure
					}

					return c.Get(ctx, key, object, opts...)
				},
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if refuse && stage == "Machine list" {
						return failure
					}

					return c.List(ctx, list, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
					_, workerSet := object.(*capiv1.MachineSet)
					if refuse && ((stage == "MachineSet delete" && workerSet) || stage == object.GetName()+" delete") {
						return failure
					}

					return c.Delete(ctx, object, opts...)
				},
				Patch: func(ctx context.Context, c client.WithWatch, object client.Object,
					patch client.Patch, opts ...client.PatchOption,
				) error {
					if refuse && stage == "finalizer patch" {
						return apierrors.NewConflict(hypershiftv1.GroupVersion.WithResource("nodepools").GroupResource(),
							pool.Name, failure)
					}

					return c.Patch(ctx, object, patch, opts...)
				},
			}).Build()
		r := &NodePoolReconciler{Client: c, APIReader: c}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)}

		_, err := r.Reconcile(ctx, request)
		if stage == "finalizer patch" {
			Expect(apierrors.IsConflict(err)).To(BeTrue())
		} else {
			Expect(errors.Is(err, failure)).To(BeTrue(), "original failure lost: %v", err)
		}

		Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Finalizers).To(ContainElement(nodePoolFinalizer))

		if stage != "workers-token delete" && stage != "finalizer patch" {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(bootstrap), bootstrap)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		}

		refuse = false
		result, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Finalizers).To(ConsistOf("foreign.example/retain"))
	}, Entry("worker input lookup", "set read"), Entry("native Cluster lookup", "Cluster read"),
		Entry("remaining Machine observation", "Machine list"), Entry("template deletion", "workers delete"),
		Entry("MachineSet deletion", "MachineSet delete"),
		Entry("bootstrap deletion", "workers-bootstrap delete"), Entry("token deletion", "workers-token delete"),
		Entry("optimistic finalizer conflict", "finalizer patch"))
})
