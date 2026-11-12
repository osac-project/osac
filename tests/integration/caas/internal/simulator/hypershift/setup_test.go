// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
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

var _ = Describe("HyperShift input watches", func() {
	DescribeTable("maps annotated native parents after HostedCluster disappearance",
		func(ctx SpecContext, kind, annotation string, matches bool) {
			metadata := metav1.ObjectMeta{
				Namespace: "hub-cluster", Name: "cluster",
				Annotations: map[string]string{kube.HostedClusterAnnotation: annotation},
			}

			var source client.Object = &hypershiftv1.HostedControlPlane{ObjectMeta: metadata}

			if kind == "Cluster" {
				source = &capiv1.Cluster{ObjectMeta: metadata}
			}

			pool := &hypershiftv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers"},
				Spec:       hypershiftv1.NodePoolSpec{ClusterName: "cluster"},
			}
			foreign := pool.DeepCopy()
			foreign.Namespace = "other"
			sibling := pool.DeepCopy()
			sibling.Name, sibling.Spec.ClusterName = "sibling", "sibling"
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(pool, foreign, sibling).Build()
			r := &NodePoolReconciler{Client: c, APIReader: c}

			requests := r.poolsForCluster(ctx, source)
			if matches {
				Expect(requests).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)}))
			} else {
				Expect(requests).To(BeEmpty())
			}
		},
		Entry("HCP association", "HCP", "hub/cluster", true),
		Entry("CAPI association", "Cluster", "hub/cluster", true),
		Entry("no association", "HCP", "", false),
		Entry("empty namespace", "Cluster", "/cluster", false),
		Entry("empty name", "HCP", "hub/", false),
		Entry("another parent", "Cluster", "hub/something-else", false),
	)

	DescribeTable("maps Machine events through their native pool annotation", func(ctx SpecContext, annotation string) {
		machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
			Namespace: "hub-cluster", Name: "worker", UID: "worker",
			Annotations: map[string]string{kube.NodePoolAnnotation: annotation},
		}}
		requests := nodePoolForChild(ctx, machine)
		if annotation == "hub/workers" {
			Expect(requests).To(ConsistOf(ctrl.Request{
				NamespacedName: client.ObjectKey{Namespace: "hub", Name: "workers"},
			}))
		} else {
			Expect(requests).To(BeEmpty())
		}
	}, Entry("MachineSet already absent", "hub/workers"), Entry("annotation missing", ""),
		Entry("annotation malformed", "workers"))

	DescribeTable("maps Routes through their original native HCP", func(ctx SpecContext, variant string) {
		cp := &hypershiftv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "cluster", UID: "original-hcp",
			Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
		}}
		route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: "ignition-server"}}
		Expect(controllerutil.SetControllerReference(cp, route, hypershiftScheme())).To(Succeed())

		switch variant {
		case "UID":
			route.OwnerReferences[0].UID = "previous-hcp"
		case "version":
			route.OwnerReferences[0].APIVersion = "example.com/v1"
		case "kind":
			route.OwnerReferences[0].Kind = "OtherControlPlane"
		case "namespace":
			route.Namespace = "foreign"
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(cp).Build()
		r := &HostedClusterReconciler{Client: c, APIReader: fake.NewClientBuilder().WithScheme(hypershiftScheme()).Build()}

		requests := r.hostedClusterForChild(ctx, route)
		if variant == "current" {
			Expect(requests).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "hub", Name: "cluster"}}))
		} else {
			Expect(requests).To(BeEmpty())
		}
	}, Entry("current", "current"), Entry("stale UID", "UID"), Entry("foreign version", "version"),
		Entry("foreign kind", "kind"), Entry("foreign namespace", "namespace"))

	DescribeTable("maps optional control-plane Secret references", func(ctx SpecContext, name string) {
		hc := agentHostedCluster()
		controlPlaneSecrets(hc)
		hc.Spec.ServiceAccountSigningKey = &corev1.LocalObjectReference{Name: "signing"}
		other := agentHostedCluster()
		other.Name, other.UID = "other", "other-cluster"
		foreign := hc.DeepCopy()
		foreign.Namespace, foreign.UID = "other-hub", "foreign-cluster"
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc, other, foreign).Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: name}}
		Expect(r.hostedClustersForSecret(ctx, secret)).To(ConsistOf(
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)}))
	}, Entry("active encryption key", "active"), Entry("backup encryption key", "backup"),
		Entry("audit webhook", "audit"), Entry("unmanaged etcd TLS", "etcd"), Entry("signing key", "signing"))

	DescribeTable("converges credential changes through cached Secret associations", func(ctx SpecContext, ssh bool) {
		hc := agentHostedCluster()
		hc.Spec.SSHKey.Name = "source-ssh-key"
		other := hc.DeepCopy()
		other.Name, other.UID = "other", "other-cluster"
		other.Spec.PullSecret.Name, other.Spec.SSHKey.Name = "different-pull-secret", "different-ssh-key"
		foreign := hc.DeepCopy()
		foreign.Namespace, foreign.UID = "other-hub", "foreign-cluster"
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name},
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{"first.invalid":{}}}`),
			},
		}
		sshKey := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.SSHKey.Name},
			Data:       map[string][]byte{"id_rsa.pub": []byte("public key")},
		}
		secret, destination, field := pullSecret, "pull-secret", corev1.DockerConfigJsonKey
		values := []string{`{"auths":{"first.invalid":{}}}`, `{"auths":{"second.invalid":{}}}`}

		if ssh {
			secret, destination, field = sshKey, "ssh-key", "id_rsa.pub"
			values = []string{"first public key", "second public key"}
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc, other, foreign, pullSecret, sshKey).
			WithStatusSubresource(hc, &hypershiftv1.HostedControlPlane{}).
			WithInterceptorFuncs(interceptor.Funcs{Create: func(
				ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption,
			) error {
				object.SetUID(types.UID(fmt.Sprintf("%T-%s", object, object.GetName())))
				return c.Create(ctx, object, opts...)
			}}).Build()
		reader := interceptor.NewClient(c, interceptor.Funcs{List: func(
			context.Context, client.WithWatch, client.ObjectList, ...client.ListOption,
		) error {
			return fmt.Errorf("authoritative list unavailable")
		}})
		r := &HostedClusterReconciler{
			Client: c, APIReader: reader, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
		}

		for _, value := range values {
			By("mapping only the referencing cluster while authoritative lists fail")

			secret.Data = map[string][]byte{field: []byte(value)}
			Expect(c.Update(ctx, secret)).To(Succeed())
			requests := r.hostedClustersForSecret(ctx, secret)
			Expect(requests).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)}))
			_, err := r.Reconcile(ctx, requests[0])
			Expect(err).NotTo(HaveOccurred())

			copied := &corev1.Secret{}
			Expect(c.Get(ctx, client.ObjectKey{Namespace: "hub-cluster", Name: destination}, copied)).To(Succeed())
			Expect(copied.Data).To(HaveKeyWithValue(field, []byte(value)))
		}
	}, Entry("pull secret", false), Entry("SSH key", true))

	DescribeTable("maps the original shared CA to its HostedCluster's pools", func(ctx SpecContext, variant string) {
		pool := &hypershiftv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers", UID: "pool"}}
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}
		objects := ignitionInputs(pool, cluster)
		ca := objects[2].(*corev1.Secret)
		other := pool.DeepCopy()
		other.Name, other.UID, other.Spec.ClusterName = "other", "other-pool", "different-cluster"
		foreign := pool.DeepCopy()
		foreign.Namespace, foreign.UID = "other-hub", "foreign-pool"
		c := fake.NewClientBuilder().
			WithScheme(hypershiftScheme()).
			WithObjects(append(objects, pool, other, foreign)...).
			Build()

		switch variant {
		case "UID":
			ca.OwnerReferences[0].UID = "previous-control-plane"
		case "name":
			ca.OwnerReferences[0].Name = "another-control-plane"
		case "version":
			ca.OwnerReferences[0].APIVersion = "example.com/v1"
		case "unrelated":
			ca.Name = "another-secret"
		}

		r := &NodePoolReconciler{
			Client: c, APIReader: fake.NewClientBuilder().WithScheme(hypershiftScheme()).Build(),
		}

		requests := r.poolsForSecret(ctx, ca)
		if variant == "current" {
			Expect(requests).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pool)}))

			h := &HostedClusterReconciler{Client: c, APIReader: r.APIReader}
			Expect(h.hostedClustersForSecret(ctx, ca)).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKey{
				Namespace: "hub", Name: "cluster",
			}}))
		} else {
			Expect(requests).To(BeEmpty())
		}
	}, Entry("current CA", "current"), Entry("old owner UID", "UID"), Entry("other owner name", "name"),
		Entry("other owner version", "version"), Entry("unrelated Secret", "unrelated"))

	It("recovers a CA event when its HCP reaches the cache later", func(ctx SpecContext) {
		pool := &hypershiftv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers", UID: "pool"}}
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "cluster"}}
		objects := ignitionInputs(pool, cluster)
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(objects[0], objects[2], pool).Build()
		r := &NodePoolReconciler{
			Client: c, APIReader: fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(objects...).Build(),
		}

		By("observing the Secret before its control plane is cached")

		Expect(r.poolsForSecret(ctx, objects[2])).To(BeEmpty())

		By("mapping the later control-plane event without another Secret update")

		objects[1].SetResourceVersion("")
		Expect(c.Create(ctx, objects[1])).To(Succeed())
		Expect(r.poolsForCluster(ctx, objects[1])).To(ConsistOf(ctrl.Request{
			NamespacedName: client.ObjectKeyFromObject(pool),
		}))
	})

	DescribeTable("maps ignition Services through their original HCP", func(ctx SpecContext, variant string) {
		cp := &hypershiftv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "cluster", UID: "original",
			Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
		}}
		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: "ignition-server-proxy"}}
		Expect(controllerutil.SetControllerReference(cp, service, hypershiftScheme())).To(Succeed())

		switch variant {
		case "UID":
			service.OwnerReferences[0].UID = "previous"
		case "name":
			service.OwnerReferences[0].Name = "other"
		case "version":
			service.OwnerReferences[0].APIVersion = "example.com/v1"
		case "namespace":
			service.Namespace = "other"
		case "ownerless":
			service.OwnerReferences = nil
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(cp).Build()
		r := &HostedClusterReconciler{
			Client: c, APIReader: fake.NewClientBuilder().WithScheme(hypershiftScheme()).Build(),
		}

		requests := r.hostedClusterForChild(ctx, service)
		if variant == "current" {
			Expect(requests).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "hub", Name: "cluster"}}))
		} else {
			Expect(requests).To(BeEmpty())
		}
	}, Entry("current owner", "current"), Entry("previous UID", "UID"), Entry("another owner", "name"),
		Entry("another API version", "version"), Entry("another namespace", "namespace"), Entry("no owner", "ownerless"))

	It("maps a deleted NodePool from the event without a live API object", func(ctx SpecContext) {
		pool := &hypershiftv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "workers"},
			Spec:       hypershiftv1.NodePoolSpec{ClusterName: "cluster"},
		}
		Expect(hostedClusterForPool(ctx, pool)).To(ConsistOf(ctrl.Request{
			NamespacedName: client.ObjectKey{Namespace: "hub", Name: "cluster"},
		}))
	})
})
