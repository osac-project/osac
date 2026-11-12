// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package loadbalancer

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = DescribeTable(
	"Native load-balancer Service selection",
	func(ctx SpecContext, change func(*corev1.Service), wantIngress bool) {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(hypershiftv1.AddToScheme(scheme)).To(Succeed())

		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "cluster", UID: "current",
				Labels: map[string]string{kube.ManagedByLabel: kube.ManagerName},
			},
			Spec: hypershiftv1.HostedControlPlaneSpec{
				Platform: hypershiftv1.PlatformSpec{
					Type:  hypershiftv1.NonePlatform,
					Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
				},
			},
		}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "kube-apiserver",
				Labels: map[string]string{kube.ManagedByLabel: kube.ManagerName},
			},
			Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
		}
		Expect(controllerutil.SetControllerReference(cp, service, scheme)).To(Succeed())

		if change != nil {
			change(service)
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, service).WithStatusSubresource(service).Build()
		r := &ServiceReconciler{Client: c, APIReader: c}
		before := &corev1.Service{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(service), before)).To(Succeed())

		By("publishing ingress for unclassed LoadBalancer Services")

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(service)})
		Expect(err).NotTo(HaveOccurred())

		after := &corev1.Service{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(service), after)).To(Succeed())

		if wantIngress {
			Expect(after.Spec).To(Equal(before.Spec))
			Expect(after.Status.LoadBalancer.Ingress).To(Equal([]corev1.LoadBalancerIngress{{IP: "192.0.2.10"}}))
		} else {
			Expect(after).To(Equal(before), "ignored Services must remain unchanged")
		}
	},
	Entry("responds to an owned LoadBalancer", nil, true),
	Entry("serves the original Service independently of parent replacement", func(service *corev1.Service) {
		owners := service.GetOwnerReferences()
		owners[0].UID = "old"
		service.SetOwnerReferences(owners)
	}, true),
	Entry("accepts a Service without simulator labels or an HCP owner", func(service *corev1.Service) {
		service.SetLabels(nil)
		service.SetOwnerReferences(nil)
	}, true),
	Entry(
		"leaves classified load balancers to their selected controller",
		func(service *corev1.Service) { service.Spec.LoadBalancerClass = new("example.invalid/provider") },
		false,
	),
	Entry("ignores a ClusterIP Service", func(service *corev1.Service) {
		service.Spec.Type = corev1.ServiceTypeClusterIP
	}, false),
)

var _ = DescribeTable("Load-balancer cleanup", func(ctx SpecContext, change string) {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())

	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Namespace:  "cp",
		Name:       "api",
		UID:        "original-service",
		Labels:     map[string]string{kube.ManagedByLabel: kube.ManagerName},
		Finalizers: []string{"foreign.example/cleanup"},
		OwnerReferences: []metav1.OwnerReference{
			{
				APIVersion: hypershiftv1.GroupVersion.String(),
				Kind:       "HostedControlPlane",
				Name:       "absent-parent",
				UID:        "original-parent",
				Controller: new(true),
			},
		},
	}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).WithStatusSubresource(service).Build()
	r := &ServiceReconciler{Client: c, APIReader: c}
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(service)}
	_, err := r.Reconcile(ctx, request)
	Expect(err).NotTo(HaveOccurred())
	Expect(c.Get(ctx, request.NamespacedName, service)).To(Succeed())
	Expect(service.Finalizers).To(ContainElement(serviceFinalizer))
	Expect(service.Status.LoadBalancer.Ingress).NotTo(BeEmpty())

	switch change {
	case "label":
		service.SetLabels(nil)
		Expect(c.Update(ctx, service)).To(Succeed())
		Expect(c.Delete(ctx, service)).To(Succeed())
	case "delete":
		Expect(c.Delete(ctx, service)).To(Succeed())
	case "type":
		service.Spec.Type = corev1.ServiceTypeClusterIP
		Expect(c.Update(ctx, service)).To(Succeed())
	case "class":
		service.Spec.LoadBalancerClass = new("example.invalid/provider")
		Expect(c.Update(ctx, service)).To(Succeed())
	}

	_, err = r.Reconcile(ctx, request)
	Expect(err).NotTo(HaveOccurred())
	Expect(c.Get(ctx, request.NamespacedName, service)).To(Succeed())
	Expect(service.Status.LoadBalancer.Ingress).To(BeEmpty())
	Expect(service.Finalizers).To(ContainElement(serviceFinalizer), "cleanup status must precede finalizer removal")

	restarted := &ServiceReconciler{Client: c, APIReader: c}
	_, err = restarted.Reconcile(ctx, request)
	Expect(err).NotTo(HaveOccurred())
	Expect(c.Get(ctx, request.NamespacedName, service)).To(Succeed())
	Expect(service.Finalizers).To(ConsistOf("foreign.example/cleanup"))
	Expect(service.UID).To(BeEquivalentTo("original-service"))
	Expect(service.OwnerReferences[0].UID).To(BeEquivalentTo("original-parent"))
}, Entry("deletion", "delete"), Entry("marker loss before deletion", "label"),
	Entry("type change", "type"), Entry("class selection change", "class"))
