// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cpo

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = Describe("Control-plane ignition Route publication", func() {
	var (
		cp *hypershiftv1.HostedControlPlane
		c  client.WithWatch
		r  *HostedControlPlaneReconciler
	)

	BeforeEach(func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(hypershiftv1.AddToScheme(scheme)).To(Succeed())
		Expect(routev1.Install(scheme)).To(Succeed())

		cp = &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: "control-plane", Name: "cluster", UID: "original-hcp"},
			Spec: hypershiftv1.HostedControlPlaneSpec{Services: []hypershiftv1.ServicePublishingStrategyMapping{{
				Service:                   hypershiftv1.Ignition,
				ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{Type: hypershiftv1.Route},
			}}},
		}
		c = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&routev1.Route{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption) error {
					if route, ok := object.(*routev1.Route); ok && route.UID == "" {
						route.UID = "original-route"
					}

					return c.Create(ctx, object, opts...)
				},
			}).Build()
		r = &HostedControlPlaneReconciler{Client: c, APIReader: c, DefaultIngressDomain: "apps.example.invalid"}
	})

	DescribeTable(
		"publishes native fields and waits for external ingress observation",
		func(ctx SpecContext, explicit bool) {
			if explicit {
				cp.Spec.Services[0].Route = &hypershiftv1.RoutePublishingStrategy{Hostname: "ignition.example.invalid"}
			}

			Expect(r.ensureIgnition(ctx, cp)).To(Succeed())

			route := &routev1.Route{}
			key := client.ObjectKey{Namespace: cp.Namespace, Name: "ignition-server"}
			Expect(c.Get(ctx, key, route)).To(Succeed())
			Expect(kube.MatchesController(route, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane"))).To(BeTrue())
			Expect(route.Spec.To).To(Equal(routev1.RouteTargetReference{
				Kind: "Service", Name: "ignition-server-proxy", Weight: new(int32(100)),
			}))
			Expect(route.Spec.TLS.Termination).To(Equal(routev1.TLSTerminationPassthrough))
			Expect(route.Spec.TLS.InsecureEdgeTerminationPolicy).To(Equal(routev1.InsecureEdgeTerminationPolicyNone))
			Expect(route.Status.Ingress).To(BeEmpty())

			if explicit {
				Expect(route.Spec.Host).To(Equal("ignition.example.invalid"))
			} else {
				Expect(route.Spec.Host).To(BeEmpty(), "ordinary hostnames belong to OpenShift API defaulting")
			}

			for _, name := range []string{"ignition-server", "ignition-server-proxy"} {
				service := &corev1.Service{}
				Expect(c.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: name}, service)).To(Succeed())
				Expect(service.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
				Expect(service.Spec.Selector).To(Equal(map[string]string{"app": name}))
			}

			ca := &corev1.Secret{}
			caKey := client.ObjectKey{Namespace: cp.Namespace, Name: "ignition-server-ca-cert"}
			Expect(apierrors.IsNotFound(c.Get(ctx, caKey, ca))).To(BeTrue())

			By("supplying external defaulting and ingress observations without an admission-condition shortcut")

			Expect(route.UID).To(BeEquivalentTo("original-route"))

			route.Annotations = map[string]string{"unrelated": "retained", hypershiftv1.ExternalDNSHostnameAnnotation: "stale"}
			if !explicit {
				route.Spec.Host = "defaulted.apps.example.invalid"
			}

			Expect(c.Update(ctx, route)).To(Succeed())
			route.Status.Ingress = []routev1.RouteIngress{{Host: route.Spec.Host}}
			Expect(c.Status().Update(ctx, route)).To(Succeed())
			before := route.DeepCopy()
			restarted := &HostedControlPlaneReconciler{Client: c, APIReader: c}
			Expect(restarted.ensureIgnition(ctx, cp)).To(Succeed())
			Expect(c.Get(ctx, key, route)).To(Succeed())
			Expect(route.UID).To(Equal(before.UID))
			Expect(route.Spec.Host).To(Equal(before.Spec.Host))
			Expect(route.Status).To(Equal(before.Status))
			Expect(route.Annotations).To(Equal(map[string]string{"unrelated": "retained"}))
			Expect(c.Get(ctx, caKey, ca)).To(Succeed())
		},
		Entry("explicit hostname", true),
		Entry("API-assigned hostname", false),
	)

	It("requires a publishing strategy and leaves externally managed PKI alone", func(ctx SpecContext) {
		services := cp.Spec.Services
		cp.Spec.Services = nil
		Expect(r.ensureIgnition(ctx, cp)).To(MatchError(ContainSubstring("ignition publishing strategy")))

		ca := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: "ignition-server-ca-cert"}}
		Expect(c.Create(ctx, ca)).To(Succeed())
		before := ca.DeepCopy()
		cp.Spec.Services = services
		cp.Spec.Services[0].ServicePublishingStrategy = hypershiftv1.ServicePublishingStrategy{
			Type: hypershiftv1.NodePort, NodePort: &hypershiftv1.NodePortPublishingStrategy{},
		}
		cp.Annotations = map[string]string{hypershiftv1.DisablePKIReconciliationAnnotation: ""}
		Expect(r.ensureIgnition(ctx, cp)).To(Succeed())
		proxyKey := client.ObjectKey{Namespace: cp.Namespace, Name: "ignition-server-proxy"}
		Expect(c.Get(ctx, proxyKey, &corev1.Service{})).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(ca), ca)).To(Succeed())
		Expect(ca).To(Equal(before))
	})

	It("repairs a disabled Route backend and skips the next patch", func(ctx SpecContext) {
		route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{
			Namespace: cp.Namespace, Name: "ignition-server", UID: "original-route",
			Labels: map[string]string{"retained": "yes"}, Annotations: map[string]string{"retained": "yes"},
		}}
		Expect(controllerutil.SetControllerReference(cp, route, c.Scheme())).To(Succeed())
		route.Spec.Host = "assigned.apps.example.invalid"
		route.Spec.To = routev1.RouteTargetReference{Kind: "Service", Name: "previous-backend", Weight: new(int32(0))}
		route.Spec.WildcardPolicy = routev1.WildcardPolicyNone
		Expect(c.Create(ctx, route)).To(Succeed())
		route.Status.Ingress = []routev1.RouteIngress{{Host: route.Spec.Host}}
		Expect(c.Status().Update(ctx, route)).To(Succeed())
		before := route.DeepCopy()
		patches := 0
		r.Client = interceptor.NewClient(c, interceptor.Funcs{Patch: func(
			ctx context.Context, c client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption,
		) error {
			if _, ok := object.(*routev1.Route); ok {
				patches++
			}

			return c.Patch(ctx, object, patch, opts...)
		}})

		_, err := r.ensureIgnitionRoute(ctx, cp, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(patches).To(Equal(1))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(route), route)).To(Succeed())
		Expect(route.Spec.To).To(Equal(routev1.RouteTargetReference{
			Kind: "Service", Name: "ignition-server-proxy", Weight: new(int32(100)),
		}))
		Expect(route.Spec.Host).To(Equal(before.Spec.Host))
		Expect(route.Spec.WildcardPolicy).To(Equal(before.Spec.WildcardPolicy))
		Expect(route.UID).To(Equal(before.UID))
		Expect(route.OwnerReferences).To(Equal(before.OwnerReferences))
		Expect(route.Labels).To(Equal(before.Labels))
		Expect(route.Annotations).To(Equal(before.Annotations))
		Expect(route.Status).To(Equal(before.Status))
		before = route.DeepCopy()

		_, err = r.ensureIgnitionRoute(ctx, cp, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(patches).To(Equal(1), "stable Route reconciliation must skip patches")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(route), route)).To(Succeed())
		Expect(route).To(Equal(before))
	})

	DescribeTable("preserves conflicting Routes", func(ctx SpecContext, field string) {
		owner := metav1.NewControllerRef(cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane"))

		switch field {
		case "version":
			owner.APIVersion = "other.example/v1"
		case "kind":
			owner.Kind = "OtherControlPlane"
		case "name":
			owner.Name = "other"
		case "UID":
			owner.UID = "previous-hcp"
		}

		route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{
			Namespace: cp.Namespace, Name: "ignition-server", UID: "foreign-route",
			OwnerReferences: []metav1.OwnerReference{*owner}, Annotations: map[string]string{"unrelated": "retained"},
		}}
		Expect(c.Create(ctx, route)).To(Succeed())
		before := route.DeepCopy()

		Expect(r.ensureIgnition(ctx, cp)).To(MatchError(ContainSubstring("another control plane")))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(route), route)).To(Succeed())
		Expect(route).To(Equal(before))
	}, Entry("API version", "version"), Entry("kind", "kind"), Entry("name", "name"), Entry("incarnation", "UID"))

	It("preserves an already assigned host while an explicit native hostname takes precedence", func(ctx SpecContext) {
		route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: "ignition-server"}}
		Expect(controllerutil.SetControllerReference(cp, route, c.Scheme())).To(Succeed())
		route.Spec.Host = "already-assigned.apps.example.invalid"
		Expect(c.Create(ctx, route)).To(Succeed())
		observed, err := r.ensureIgnitionRoute(ctx, cp, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(observed.Spec.Host).To(Equal(route.Spec.Host))
		observed, err = r.ensureIgnitionRoute(
			ctx,
			cp,
			&hypershiftv1.RoutePublishingStrategy{Hostname: "explicit.example.invalid"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(observed.Spec.Host).To(Equal("explicit.example.invalid"))
	})

	DescribeTable("uses native shortening only for overlong names with a management ingress domain",
		func(namespace, domain, expected string) {
			Expect(shortenedRouteHostname("ignition-server", namespace, domain)).To(Equal(expected))
		},
		Entry("ordinary names", "control-plane", "apps.example.invalid", ""),
		Entry("missing domain", strings.Repeat("a", 55), "", ""),
		Entry("long name", strings.Repeat("a", 55), "apps.example.invalid",
			"ignition-server-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-cbaa6de2.apps.example.invalid"),
	)
})
