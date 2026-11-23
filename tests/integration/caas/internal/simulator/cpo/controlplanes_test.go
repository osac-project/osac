// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cpo

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = Describe("Control-plane responses", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(hypershiftv1.AddToScheme(scheme)).To(Succeed())
	})

	const future = "2099-01-01T00:00:00Z"

	DescribeTable(
		"follows native Boolean and timestamp pause semantics",
		func(ctx SpecContext, value *string, paused bool, reason, message string) {
			cp := &hypershiftv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original", Generation: 7},
				Spec: hypershiftv1.HostedControlPlaneSpec{
					PausedUntil: value,
					Services: []hypershiftv1.ServicePublishingStrategyMapping{
						{
							Service:                   hypershiftv1.APIServer,
							ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{Type: hypershiftv1.LoadBalancer},
						},
						{
							Service: hypershiftv1.Ignition, ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{
								Type: hypershiftv1.NodePort, NodePort: &hypershiftv1.NodePortPublishingStrategy{},
							},
						},
					},
					Platform: hypershiftv1.PlatformSpec{
						Type: hypershiftv1.NonePlatform, Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
					},
				},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).WithStatusSubresource(cp).Build()
			r := &HostedControlPlaneReconciler{
				Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
				Availability: func(context.Context, *hypershiftv1.HostedControlPlane) (bool, error) {
					return false, nil
				},
			}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cp)})
			Expect(err).NotTo(HaveOccurred())

			service := &corev1.Service{}
			err = c.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: "kube-apiserver"}, service)

			if paused {
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(c.Get(ctx, client.ObjectKeyFromObject(cp), cp)).To(Succeed())
			Expect(meta.IsStatusConditionFalse(cp.Status.Conditions, "ReconciliationActive")).To(Equal(paused))
			condition := meta.FindStatusCondition(cp.Status.Conditions, "ReconciliationActive")
			Expect(condition).NotTo(BeNil())
			Expect(condition.Reason).To(Equal(reason))
			Expect(condition.Message).To(Equal(message))
			Expect(condition.ObservedGeneration).To(Equal(int64(7)))
		},
		Entry("unset", nil, false, hypershiftv1.AsExpectedReason, "Reconciliation active on resource"),
		Entry("false", new("false"), false, hypershiftv1.AsExpectedReason, "Reconciliation active on resource"),
		Entry(
			"true",
			new("true"),
			true,
			hypershiftv1.ReconciliationPausedConditionReason,
			"Reconciliation paused until field removed",
		),
		Entry("native Boolean alternative", new("TRUE"), true, hypershiftv1.ReconciliationPausedConditionReason,
			"Reconciliation paused until field removed"),
		Entry("future timestamp", new(future), true, hypershiftv1.ReconciliationPausedConditionReason,
			"Reconciliation paused until: "+future),
		Entry("past timestamp", new(time.Now().Add(-time.Hour).Format(time.RFC3339)), false,
			hypershiftv1.AsExpectedReason, "Reconciliation active on resource"),
		Entry("invalid input remains unpaused", new("invalid"), false,
			hypershiftv1.ReconciliationInvalidPausedUntilConditionReason, "Invalid value provided for PausedUntil field"),
	)

	DescribeTable("refreshes pause diagnostics before an output ownership failure",
		func(ctx SpecContext, value *string, reason, message string) {
			cp := &hypershiftv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original", Generation: 7},
				Spec: hypershiftv1.HostedControlPlaneSpec{
					PausedUntil: new("true"),
					Platform: hypershiftv1.PlatformSpec{
						Type: hypershiftv1.NonePlatform, Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
					},
				},
			}
			cp.Status.Initialized = true
			cp.Status.KubeConfig = &hypershiftv1.KubeconfigSecretRef{Name: "retained", Key: "kubeconfig"}
			meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
				Type: "Available", Status: metav1.ConditionTrue, Reason: "Retained", ObservedGeneration: 6,
				LastTransitionTime: metav1.NewTime(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.Local)),
			})
			retained := *meta.FindStatusCondition(cp.Status.Conditions, "Available")
			service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Namespace: cp.Namespace, Name: "kube-apiserver", UID: "foreign-service",
			}}
			patches, observations := 0, 0
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, service).WithStatusSubresource(cp).
				WithInterceptorFuncs(interceptor.Funcs{SubResourcePatch: func(
					ctx context.Context, c client.Client, subresource string, object client.Object,
					patch client.Patch, opts ...client.SubResourcePatchOption,
				) error {
					patches++

					return c.SubResource(subresource).Patch(ctx, object, patch, opts...)
				}}).Build()
			Expect(c.Get(ctx, client.ObjectKeyFromObject(service), service)).To(Succeed())
			before := service.DeepCopy()
			r := &HostedControlPlaneReconciler{
				Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
				Availability: func(context.Context, *hypershiftv1.HostedControlPlane) (bool, error) {
					observations++

					return false, nil
				},
			}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cp)}
			_, err := r.Reconcile(ctx, request)
			Expect(err).To(MatchError("service cp/kube-apiserver belongs to another control plane"))
			Expect(c.Get(ctx, request.NamespacedName, cp)).To(Succeed())
			Expect(meta.IsStatusConditionFalse(cp.Status.Conditions, "ReconciliationActive")).To(BeTrue())

			cp.Spec.PausedUntil, cp.Generation = value, 8
			Expect(c.Update(ctx, cp)).To(Succeed())

			for range 2 {
				_, err = r.Reconcile(ctx, request)
				Expect(err).To(MatchError("service cp/kube-apiserver belongs to another control plane"))
			}

			Expect(c.Get(ctx, request.NamespacedName, cp)).To(Succeed())
			condition := meta.FindStatusCondition(cp.Status.Conditions, "ReconciliationActive")
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal(reason))
			Expect(condition.Message).To(Equal(message))
			Expect(condition.ObservedGeneration).To(Equal(int64(8)))
			Expect(cp.UID).To(Equal(types.UID("original")))
			Expect(cp.Status.Initialized).To(BeTrue())
			Expect(cp.Status.KubeConfig).To(Equal(&hypershiftv1.KubeconfigSecretRef{Name: "retained", Key: "kubeconfig"}))
			Expect(*meta.FindStatusCondition(cp.Status.Conditions, "Available")).To(Equal(retained))
			Expect(patches).To(Equal(2), "unchanged diagnostics must skip status patches")
			Expect(observations).To(BeZero())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(service), service)).To(Succeed())
			Expect(service).To(Equal(before))
		},
		Entry("pause removed", nil, hypershiftv1.AsExpectedReason, "Reconciliation active on resource"),
		Entry("malformed pause", new("invalid"), hypershiftv1.ReconciliationInvalidPausedUntilConditionReason,
			"Invalid value provided for PausedUntil field"),
	)

	It("returns a diagnostic status conflict before creating outputs", func(ctx SpecContext) {
		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "cluster", UID: "original", Finalizers: []string{controlPlaneFinalizer},
			},
			Spec: hypershiftv1.HostedControlPlaneSpec{Platform: hypershiftv1.PlatformSpec{
				Type: hypershiftv1.NonePlatform, Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
			}},
		}
		writes := 0
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).WithStatusSubresource(cp).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption) error {
					writes++

					return c.Create(ctx, object, opts...)
				},
				Patch: func(ctx context.Context, c client.WithWatch, object client.Object,
					patch client.Patch, opts ...client.PatchOption,
				) error {
					writes++

					return c.Patch(ctx, object, patch, opts...)
				},
				SubResourcePatch: func(ctx context.Context, c client.Client, subresource string,
					object client.Object, patch client.Patch, opts ...client.SubResourcePatchOption,
				) error {
					current := &hypershiftv1.HostedControlPlane{}
					Expect(c.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
					metav1.SetMetaDataAnnotation(&current.ObjectMeta, "concurrent", "retained")
					Expect(c.Update(ctx, current)).To(Succeed())

					return c.SubResource(subresource).Patch(ctx, object, patch, opts...)
				},
			}).Build()
		r := &HostedControlPlaneReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cp)})
		Expect(apierrors.IsConflict(err)).To(BeTrue(), "optimistic status write must detect the concurrent change: %v", err)
		Expect(writes).To(BeZero())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(cp), cp)).To(Succeed())
		Expect(cp.Annotations).To(HaveKeyWithValue("concurrent", "retained"))
		Expect(cp.Status.Conditions).To(BeEmpty())
	})

	It("keeps a valid signing pair across retries and recovery", func(ctx SpecContext) {
		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original"},
		}
		ca := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: cp.Namespace, Name: "ignition-server-ca-cert", UID: "original-ca",
			Labels:      map[string]string{"native.example/retained": "yes"},
			Annotations: map[string]string{"native.example/retained": "yes"},
		}, Data: map[string][]byte{"retained": []byte("unrelated")}}
		Expect(controllerutil.SetControllerReference(cp, ca, scheme)).To(Succeed())
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, ca).Build()
		r := &HostedControlPlaneReconciler{Client: c, APIReader: c}
		Expect(r.ensureIgnitionCA(ctx, cp)).To(Succeed())

		Expect(c.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: "ignition-server-ca-cert"}, ca)).To(Succeed())
		pair, err := tls.X509KeyPair(ca.Data[corev1.TLSCertKey], ca.Data[corev1.TLSPrivateKeyKey])
		Expect(err).NotTo(HaveOccurred())

		key, ok := pair.PrivateKey.(*rsa.PrivateKey)
		Expect(ok).To(BeTrue())
		Expect(key.N.BitLen()).To(Equal(2048))

		certificate, err := x509.ParseCertificate(pair.Certificate[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(certificate.Subject.CommonName).To(Equal("ignition-root-ca"))
		Expect(certificate.Subject.OrganizationalUnit).To(Equal([]string{"openshift"}))
		Expect(certificate.IsCA).To(BeTrue())
		Expect(certificate.BasicConstraintsValid).To(BeTrue())
		Expect(
			certificate.KeyUsage,
		).To(Equal(x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign))
		Expect(certificate.SubjectKeyId).NotTo(BeEmpty())
		Expect(certificate.NotAfter.Sub(certificate.NotBefore)).To(Equal(10 * 365 * 24 * time.Hour))
		Expect(certificate.CheckSignatureFrom(certificate)).To(Succeed())
		Expect(ca.UID).To(Equal(types.UID("original-ca")))
		Expect(ca.Type).To(Equal(corev1.SecretTypeTLS))
		Expect(ca.Data["retained"]).To(Equal([]byte("unrelated")))
		Expect(ca.Labels).To(HaveKeyWithValue("native.example/retained", "yes"))
		Expect(ca.Annotations).To(HaveKeyWithValue("native.example/retained", "yes"))

		before := ca.DeepCopy()
		restarted := &HostedControlPlaneReconciler{Client: c, APIReader: c}
		Expect(restarted.ensureIgnitionCA(ctx, cp)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(ca), ca)).To(Succeed())
		Expect(ca).To(Equal(before))

		cp.UID = "replacement"
		Expect(restarted.ensureIgnitionCA(ctx, cp)).NotTo(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(ca), ca)).To(Succeed())
		Expect(ca).To(Equal(before))
	})

	DescribeTable(
		"follows the pinned CA helper's entry-presence behavior",
		func(ctx SpecContext, data map[string][]byte, generate bool) {
			cp := &hypershiftv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "cluster", UID: "original",
			}}
			ca := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Namespace: cp.Namespace, Name: "ignition-server-ca-cert", UID: "original-ca",
			}, Data: data}
			Expect(controllerutil.SetControllerReference(cp, ca, scheme)).To(Succeed())
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, ca).Build()
			r := &HostedControlPlaneReconciler{Client: c, APIReader: c}

			Expect(r.ensureIgnitionCA(ctx, cp)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(ca), ca)).To(Succeed())
			Expect(ca.UID).To(Equal(types.UID("original-ca")))
			Expect(ca.Type).To(Equal(corev1.SecretTypeTLS))

			if generate {
				_, err := tls.X509KeyPair(ca.Data[corev1.TLSCertKey], ca.Data[corev1.TLSPrivateKeyKey])
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(ca.Data).To(Equal(data))
			}

			before := ca.DeepCopy()

			Expect(r.ensureIgnitionCA(ctx, cp)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(ca), ca)).To(Succeed())
			Expect(ca).To(Equal(before))
		},
		Entry("absent data generates a pair", nil, true),
		Entry("empty data generates a pair", map[string][]byte{}, true),
		Entry("certificate without a key generates a pair", map[string][]byte{corev1.TLSCertKey: []byte("existing")}, true),
		Entry(
			"key without a certificate stays unchanged",
			map[string][]byte{corev1.TLSPrivateKeyKey: []byte("existing")},
			false,
		),
		Entry("empty key stays unchanged", map[string][]byte{corev1.TLSPrivateKeyKey: {}}, false),
		Entry("empty certificate with a key stays unchanged", map[string][]byte{
			corev1.TLSPrivateKeyKey: []byte("existing"), corev1.TLSCertKey: {},
		}, false),
		Entry("empty pair stays unchanged", map[string][]byte{corev1.TLSPrivateKeyKey: {}, corev1.TLSCertKey: {}}, false),
		Entry("existing pair stays unchanged", map[string][]byte{
			corev1.TLSPrivateKeyKey: []byte("existing-key"), corev1.TLSCertKey: []byte("existing-certificate"),
		}, false),
	)

	It(
		"recovers availability on the original HCP while retaining worker inputs and guest observations",
		func(ctx SpecContext) {
			By("preparing a held control plane with an independent guest condition")

			controlPlane := &hypershiftv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "control-plane", Name: "cluster", UID: "original-control-plane", Generation: 7,
				},
				Spec: hypershiftv1.HostedControlPlaneSpec{
					KubeConfig: &hypershiftv1.KubeconfigSecretRef{Name: "access", Key: "client-config"},
					Services: []hypershiftv1.ServicePublishingStrategyMapping{
						{
							Service:                   hypershiftv1.APIServer,
							ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{Type: hypershiftv1.LoadBalancer},
						},
						{
							Service: hypershiftv1.Ignition, ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{
								Type: hypershiftv1.NodePort, NodePort: &hypershiftv1.NodePortPublishingStrategy{},
							},
						},
					},
					Platform: hypershiftv1.PlatformSpec{
						Type:  hypershiftv1.NonePlatform,
						Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
					},
				},
			}
			meta.SetStatusCondition(&controlPlane.Status.Conditions, metav1.Condition{
				Type: "Degraded", Status: metav1.ConditionTrue, Reason: "UnavailableReplicas",
			})
			meta.SetStatusCondition(&controlPlane.Status.Conditions, metav1.Condition{
				Type: "ClusterVersionFailing", Status: metav1.ConditionFalse, Reason: "GuestObserved",
				Message: "Simulated provider observation", ObservedGeneration: 1,
			})
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(controlPlane).
				WithStatusSubresource(controlPlane, &corev1.Service{}).Build()
			available := false

			var readError error

			r := &HostedControlPlaneReconciler{
				Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
				Availability: func(_ context.Context, observed *hypershiftv1.HostedControlPlane) (bool, error) {
					Expect(observed.UID).To(Equal(controlPlane.UID))

					return available, readError
				},
			}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(controlPlane)}

			By("creating the API Service while leaving its load-balancer status to its owner")

			result, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Second))

			service := &corev1.Service{}
			Expect(
				c.Get(ctx, client.ObjectKey{Namespace: controlPlane.Namespace, Name: "kube-apiserver"}, service),
			).To(Succeed())
			Expect(service.Status.LoadBalancer.Ingress).To(BeEmpty())

			ca := &corev1.Secret{}
			Expect(
				c.Get(ctx, client.ObjectKey{Namespace: controlPlane.Namespace, Name: "ignition-server-ca-cert"}, ca),
			).To(Succeed())
			Expect(
				kube.MatchesController(ca, controlPlane, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")),
			).To(BeTrue())

			By("observing the load balancer and publishing worker prerequisites before availability")

			nextService := service.DeepCopy()
			nextService.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "192.0.2.10"}}
			Expect(c.Status().Patch(ctx, nextService,
				client.MergeFromWithOptions(service, client.MergeFromWithOptimisticLock{}))).To(Succeed())
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			held := &hypershiftv1.HostedControlPlane{}
			Expect(c.Get(ctx, request.NamespacedName, held)).To(Succeed())
			Expect(held.Status.Ready).To(BeFalse())
			degraded := meta.FindStatusCondition(held.Status.Conditions, "Degraded")
			Expect(degraded).NotTo(BeNil())
			Expect(degraded.Status).To(Equal(metav1.ConditionFalse))
			Expect(degraded.Reason).To(Equal(hypershiftv1.AsExpectedReason))
			Expect(degraded.ObservedGeneration).To(Equal(controlPlane.Generation))
			Expect(held.Status.Initialized).To(BeTrue())
			Expect(held.Status.ControlPlaneEndpoint.Host).NotTo(BeEmpty())
			Expect(held.Status.KubeConfig).To(Equal(controlPlane.Spec.KubeConfig))

			access := &corev1.Secret{}
			Expect(
				c.Get(ctx, client.ObjectKey{Namespace: held.Namespace, Name: held.Status.KubeConfig.Name}, access),
			).To(Succeed())
			Expect(access.Data).To(HaveKey("client-config"))
			Expect(access.Data).NotTo(HaveKey("kubeconfig"))
			access.Data["foreign"] = []byte("retained")
			Expect(c.Update(ctx, access)).To(Succeed())

			By("releasing availability without replacing the HCP or losing the guest condition")

			available = true
			result, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Second))

			ready := &hypershiftv1.HostedControlPlane{}
			Expect(c.Get(ctx, request.NamespacedName, ready)).To(Succeed())
			Expect(ready.UID).To(Equal(controlPlane.UID))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(access), access)).To(Succeed())
			Expect(access.Data["foreign"]).To(Equal([]byte("retained")))
			Expect(meta.IsStatusConditionTrue(ready.Status.Conditions, "Available")).To(BeTrue())
			Expect(meta.IsStatusConditionFalse(ready.Status.Conditions, "Degraded")).To(BeTrue())
			Expect(meta.IsStatusConditionFalse(ready.Status.Conditions, "ClusterVersionFailing")).To(BeTrue())

			By("withholding readiness while the new hostname is unapplied")

			ready.Spec.PausedUntil = new("true")
			ready.Spec.Services[0].LoadBalancer = &hypershiftv1.LoadBalancerPublishingStrategy{Hostname: "new.example.invalid"}
			Expect(c.Update(ctx, ready)).To(Succeed())
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, request.NamespacedName, ready)).To(Succeed())
			Expect(ready.Status.Ready).To(BeFalse())
			Expect(ready.Status.ControlPlaneEndpoint.Host).To(Equal("192.0.2.10"))

			unchangedAccess := &corev1.Secret{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(access), unchangedAccess)).To(Succeed())
			Expect(unchangedAccess).To(Equal(access))

			By("observing ingress loss for the published endpoint while paused")

			ready.Spec.Services[0].LoadBalancer = nil
			Expect(c.Update(ctx, ready)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(service), service)).To(Succeed())
			delete(service.Labels, "app")
			Expect(c.Update(ctx, service)).To(Succeed())

			service.Status.LoadBalancer.Ingress = nil
			Expect(c.Status().Update(ctx, service)).To(Succeed())
			beforeService, beforeAccess := service.DeepCopy(), access.DeepCopy()
			result, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(c.Get(ctx, request.NamespacedName, ready)).To(Succeed())
			Expect(ready.Status.Ready).To(BeFalse())
			Expect(meta.IsStatusConditionFalse(ready.Status.Conditions, "InfrastructureReady")).To(BeTrue())
			Expect(ready.Status.ControlPlaneEndpoint.Host).To(Equal("192.0.2.10"))
			Expect(ready.Status.KubeConfig.Name).To(Equal(access.Name))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(service), service)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(access), access)).To(Succeed())
			Expect(service).To(Equal(beforeService))
			Expect(access).To(Equal(beforeAccess))

			ready.Spec.PausedUntil = nil
			Expect(c.Update(ctx, ready)).To(Succeed())

			service.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "192.0.2.10"}}
			Expect(c.Status().Update(ctx, service)).To(Succeed())

			By("applying a later availability hold to the ready control plane")

			available = false
			result, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Second))

			held = &hypershiftv1.HostedControlPlane{}
			Expect(c.Get(ctx, request.NamespacedName, held)).To(Succeed())
			Expect(held.UID).To(Equal(controlPlane.UID))
			Expect(held.Status.Initialized).To(BeTrue())
			Expect(meta.IsStatusConditionFalse(held.Status.Conditions, "Available")).To(BeTrue())
			Expect(meta.IsStatusConditionFalse(held.Status.Conditions, "Degraded")).To(BeTrue())
			Expect(meta.IsStatusConditionFalse(held.Status.Conditions, "ClusterVersionFailing")).To(BeTrue())

			By("surfacing failures to read the live availability response")

			available = true
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, request.NamespacedName, held)).To(Succeed())
			Expect(held.Status.Ready).To(BeTrue())

			readError = errors.New("settings unavailable")
			_, err = r.Reconcile(ctx, request)
			Expect(errors.Is(err, readError)).To(BeTrue(), "original failure lost: %v", err)
			Expect(c.Get(ctx, request.NamespacedName, held)).To(Succeed())
			Expect(held.Status.Ready).To(BeFalse())
			Expect(meta.IsStatusConditionTrue(held.Status.Conditions, "Degraded")).To(BeTrue())
			Expect(held.Status.Initialized).To(BeTrue())
			Expect(held.Status.KubeConfig.Name).To(Equal(access.Name))

			condition := meta.FindStatusCondition(held.Status.Conditions, "Degraded")
			condition.LastTransitionTime = metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))

			Expect(c.Status().Update(ctx, held)).To(Succeed())
			beforeErrorRetry := held.DeepCopy()
			_, err = r.Reconcile(ctx, request)
			Expect(err).To(MatchError(readError))
			Expect(c.Get(ctx, request.NamespacedName, held)).To(Succeed())
			Expect(held).To(Equal(beforeErrorRetry))

			By("retaining both the backend error and a status publication error")

			readError = errors.New("settings still unavailable")
			patchError := errors.New("status publication failed")
			r.Client = interceptor.NewClient(c, interceptor.Funcs{SubResourcePatch: func(
				context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption,
			) error {
				return patchError
			}})
			_, err = r.Reconcile(ctx, request)
			Expect(errors.Is(err, readError)).To(BeTrue())
			Expect(errors.Is(err, patchError)).To(BeTrue())

			By("recovering through ordinary reconciliation without replacing access data")

			r.Client, readError = c, nil
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, request.NamespacedName, held)).To(Succeed())
			Expect(held.Status.Ready).To(BeTrue())
			Expect(meta.IsStatusConditionFalse(held.Status.Conditions, "Degraded")).To(BeTrue())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(access), access)).To(Succeed())
			Expect(access.Data["foreign"]).To(Equal([]byte("retained")))

			By("protecting the ignition CA from a colliding kubeconfig reference")

			beforeCA := &corev1.Secret{}
			Expect(
				c.Get(ctx, client.ObjectKey{Namespace: held.Namespace, Name: "ignition-server-ca-cert"}, beforeCA),
			).To(Succeed())
			held.Spec.KubeConfig = &hypershiftv1.KubeconfigSecretRef{Name: beforeCA.Name, Key: corev1.TLSCertKey}
			Expect(c.Update(ctx, held)).To(Succeed())
			_, err = r.Reconcile(ctx, request)
			Expect(err).To(MatchError(ContainSubstring("conflicts with ignition CA Secret")))

			afterCA := &corev1.Secret{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(beforeCA), afterCA)).To(Succeed())
			Expect(afterCA).To(Equal(beforeCA))
		},
	)

	It("keeps an older control plane's Service and Secret unchanged", func(ctx SpecContext) {
		current := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "new"},
		}
		previous := current.DeepCopy()
		previous.UID = "old"

		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "kube-apiserver"}}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "admin-kubeconfig"}}

		Expect(controllerutil.SetControllerReference(previous, service, scheme)).To(Succeed())
		Expect(controllerutil.SetControllerReference(previous, secret, scheme)).To(Succeed())
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service, secret).Build()
		r := &HostedControlPlaneReconciler{Client: c, APIReader: c}

		_, err := r.readAPIService(ctx, current)
		Expect(err).To(MatchError(ContainSubstring("another control plane")))

		current.Spec.Services = []hypershiftv1.ServicePublishingStrategyMapping{{
			Service: hypershiftv1.APIServer, ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{
				Type: hypershiftv1.NodePort, NodePort: &hypershiftv1.NodePortPublishingStrategy{Address: "192.0.2.10"},
			},
		}}
		endpointService := service.DeepCopy()
		endpointService.Spec.Ports = []corev1.ServicePort{{Port: 6443, NodePort: 31443}}
		_, err = r.ensureKubeconfig(ctx, current, endpointService)
		Expect(err).To(MatchError(ContainSubstring("another control plane")))

		afterService := &corev1.Service{}
		afterSecret := &corev1.Secret{}

		Expect(c.Get(ctx, client.ObjectKeyFromObject(service), afterService)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(secret), afterSecret)).To(Succeed())
		Expect(afterService).To(Equal(service))
		Expect(afterSecret).To(Equal(secret))
	})

	DescribeTable(
		"converges native API publication while retaining Service allocations",
		func(ctx SpecContext, kind hypershiftv1.PublishingStrategyType) {
			cp := &hypershiftv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original"},
			}

			strategy := hypershiftv1.ServicePublishingStrategy{Type: kind}
			if kind == hypershiftv1.NodePort {
				strategy.NodePort = &hypershiftv1.NodePortPublishingStrategy{Address: "node.example.invalid", Port: 32000}
			} else {
				strategy.LoadBalancer = &hypershiftv1.LoadBalancerPublishingStrategy{Hostname: "configured.example.invalid"}
			}

			cp.Spec.Networking.APIServer = &hypershiftv1.APIServerNetworking{Port: new(int32(7443))}
			cp.Spec.Services = []hypershiftv1.ServicePublishingStrategyMapping{
				{Service: hypershiftv1.APIServer, ServicePublishingStrategy: strategy},
			}
			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "cp",
					Name:       "kube-apiserver",
					UID:        "original-service",
					Finalizers: []string{"foreign.example/cleanup"},
				},
				Spec: corev1.ServiceSpec{
					ClusterIP:  "10.0.0.10",
					ClusterIPs: []string{"10.0.0.10"},
					Ports:      []corev1.ServicePort{{Name: "api", NodePort: 31443, Port: 443, Protocol: corev1.ProtocolUDP}},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{{Hostname: "observed.example.invalid", IP: "192.0.2.10"}},
					},
				},
			}
			Expect(controllerutil.SetControllerReference(cp, service, scheme)).To(Succeed())
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).WithStatusSubresource(service).Build()
			r := &HostedControlPlaneReconciler{Client: c, APIReader: c}
			service, err := r.readAPIService(ctx, cp)
			Expect(err).NotTo(HaveOccurred())
			observed, err := r.ensureAPIService(ctx, cp, service)
			Expect(err).NotTo(HaveOccurred())
			Expect(observed.Spec.ClusterIP).To(Equal("10.0.0.10"))
			Expect(observed.Spec.ClusterIPs).To(ConsistOf("10.0.0.10"))
			Expect(observed.Spec.Ports[0].NodePort).To(Equal(int32(31443)))
			Expect(observed.Spec.Ports[0].Port).To(Equal(int32(6443)))
			Expect(observed.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			Expect(observed.Spec.Ports[0].TargetPort.StrVal).To(Equal("client"))
			Expect(observed.Finalizers).To(ConsistOf("foreign.example/cleanup"))

			By("publishing the native port even when the observed Service port has drifted")

			observed.Spec.Ports[0].Port = 7443
			host, port := apiEndpoint(cp, observed)
			if kind == hypershiftv1.NodePort {
				Expect(observed.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
				Expect(host).To(Equal("node.example.invalid"))
				Expect(port).To(Equal(int32(31443)))
			} else {
				Expect(observed.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))
				Expect(host).To(Equal("configured.example.invalid"))
				Expect(port).To(Equal(int32(6443)))

				strategy := &cp.Spec.Services[0].ServicePublishingStrategy
				strategy.LoadBalancer.Hostname = ""
				host, _ = apiEndpoint(cp, observed)
				Expect(host).To(Equal("observed.example.invalid"))

				observed.Status.LoadBalancer.Ingress[0].Hostname = ""
				host, _ = apiEndpoint(cp, observed)
				Expect(host).To(Equal("192.0.2.10"))
			}
		},
		Entry("load balancer", hypershiftv1.LoadBalancer),
		Entry("node port", hypershiftv1.NodePort),
	)

	DescribeTable("publishes HCP-owned ignition Services and retains allocation", func(ctx SpecContext, requested int32) {
		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp", Name: "cluster", UID: "original",
			},
			Spec: hypershiftv1.HostedControlPlaneSpec{
				Platform: hypershiftv1.PlatformSpec{
					Type:  hypershiftv1.NonePlatform,
					Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
				},
				Services: []hypershiftv1.ServicePublishingStrategyMapping{
					{
						Service:                   hypershiftv1.APIServer,
						ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{Type: hypershiftv1.LoadBalancer},
					},
					{
						Service: hypershiftv1.Ignition,
						ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{
							Type:     hypershiftv1.NodePort,
							NodePort: &hypershiftv1.NodePortPublishingStrategy{Address: "192.0.2.5", Port: requested},
						},
					},
				},
			},
		}
		backend := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cp.Namespace, Name: "ignition-server", UID: "original-backend",
				Annotations: map[string]string{"retained": "yes"},
			},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "metrics", Port: 9091}, {Name: "https", Port: 443}}},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, backend).WithStatusSubresource(cp).Build()
		r := &HostedControlPlaneReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			Availability: func(context.Context, *hypershiftv1.HostedControlPlane) (bool, error) {
				return false, nil
			},
		}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cp)}
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		Expect(c.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: "ignition-server"}, backend)).To(Succeed())
		Expect(backend.UID).To(Equal(types.UID("original-backend")))
		Expect(backend.Annotations).To(HaveKeyWithValue("retained", "yes"))
		Expect(kube.MatchesController(backend, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane"))).To(BeTrue())
		Expect(backend.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
		Expect(backend.Spec.Ports).To(HaveLen(1))
		Expect(backend.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(9090)))

		service := &corev1.Service{}
		key := client.ObjectKey{Namespace: cp.Namespace, Name: "ignition-server-proxy"}
		Expect(c.Get(ctx, key, service)).To(Succeed())
		Expect(kube.MatchesController(service, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane"))).To(BeTrue())
		Expect(service.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
		Expect(service.Spec.Selector).To(Equal(map[string]string{"app": "ignition-server-proxy"}))
		Expect(service.Spec.Ports[0].TargetPort.StrVal).To(Equal("https"))
		Expect(service.Spec.Ports[0].NodePort).To(Equal(requested))

		By("retaining API-server allocations and unrelated fields through restart")

		allocated := requested
		if allocated == 0 {
			allocated = 32001
		}

		service.Spec.Ports[0].NodePort = allocated
		service.Spec.ClusterIP = "10.0.0.10"
		service.Spec.ClusterIPs = []string{"10.0.0.10"}
		service.Spec.Selector = map[string]string{"drift": "intentional"}
		service.Annotations = map[string]string{"retained": "yes"}
		Expect(c.Update(ctx, service)).To(Succeed())
		Expect(c.Get(ctx, request.NamespacedName, cp)).To(Succeed())
		cp.Spec.Services[1].NodePort.Port = 30444
		Expect(c.Update(ctx, cp)).To(Succeed())

		restarted := &HostedControlPlaneReconciler{
			Client: c, APIReader: c, Scope: r.Scope, Availability: r.Availability,
		}
		_, err = restarted.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, key, service)).To(Succeed())
		Expect(service.Spec.Ports[0].NodePort).To(Equal(allocated))
		Expect(service.Spec.Selector).To(Equal(map[string]string{"app": "ignition-server-proxy"}))
		Expect(service.Spec.ClusterIP).To(Equal("10.0.0.10"))
		Expect(service.Spec.ClusterIPs).To(ConsistOf("10.0.0.10"))
		Expect(service.Annotations).To(HaveKeyWithValue("retained", "yes"))
		Expect(c.Delete(ctx, service)).To(Succeed())
		_, err = restarted.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, key, service)).To(Succeed())
		Expect(service.Spec.Ports[0].NodePort).To(Equal(int32(30444)))
	}, Entry("an omitted port", int32(0)), Entry("an explicit port", int32(30443)))

	DescribeTable("preserves conflicting ignition Services", func(ctx SpecContext, name, owner string) {
		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "current"},
			Spec: hypershiftv1.HostedControlPlaneSpec{Services: []hypershiftv1.ServicePublishingStrategyMapping{
				{Service: hypershiftv1.Ignition, ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{
					Type: hypershiftv1.NodePort, NodePort: &hypershiftv1.NodePortPublishingStrategy{Port: 30443},
				}},
			}},
		}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cp.Namespace, Name: name, UID: "original-service",
				Annotations: map[string]string{"retained": "yes"}, Finalizers: []string{"foreign.example/cleanup"},
			},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "foreign"},
				Ports: []corev1.ServicePort{{Name: "foreign", Port: 80, NodePort: 31999}},
			},
		}
		previous := cp.DeepCopy()
		previous.UID = "previous"

		switch owner {
		case "foreign":
			foreign := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: "other", UID: "other"}}
			Expect(controllerutil.SetControllerReference(foreign, service, scheme)).To(Succeed())
		case "previous":
			Expect(controllerutil.SetControllerReference(previous, service, scheme)).To(Succeed())
		case "ordinary":
			Expect(controllerutil.SetOwnerReference(previous, service, scheme)).To(Succeed())
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(service), service)).To(Succeed())
		before := service.DeepCopy()
		r := &HostedControlPlaneReconciler{Client: c, APIReader: c}
		Expect(r.ensureIgnition(ctx, cp)).To(MatchError(ContainSubstring("another control plane")))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(service), service)).To(Succeed())
		Expect(service).To(Equal(before))
	},
		Entry("a foreign backend controller", "ignition-server", "foreign"),
		Entry("a foreign proxy controller", "ignition-server-proxy", "foreign"),
		Entry("a previous backend HCP", "ignition-server", "previous"),
		Entry("a previous proxy HCP", "ignition-server-proxy", "previous"),
		Entry("an ordinary previous backend owner", "ignition-server", "ordinary"),
		Entry("an ordinary previous proxy owner", "ignition-server-proxy", "ordinary"),
	)

	It("releases only its None-platform cleanup barrier after a restart", func(ctx SpecContext) {
		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  "cp",
				Name:       "cluster",
				UID:        "original",
				Finalizers: []string{"foreign.example/cleanup"},
			},
			Spec: hypershiftv1.HostedControlPlaneSpec{
				Services: []hypershiftv1.ServicePublishingStrategyMapping{
					{
						Service:                   hypershiftv1.APIServer,
						ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{Type: hypershiftv1.LoadBalancer},
					},
					{
						Service: hypershiftv1.Ignition, ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{
							Type: hypershiftv1.NodePort, NodePort: &hypershiftv1.NodePortPublishingStrategy{},
						},
					},
				},
				Platform: hypershiftv1.PlatformSpec{
					Type:  hypershiftv1.NonePlatform,
					Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).WithStatusSubresource(cp).Build()
		reads := 0
		r := &HostedControlPlaneReconciler{
			Client:    c,
			APIReader: c,
			Scope:     kube.Scope{AgentNamespaces: []string{"hub"}},
			Availability: func(context.Context, *hypershiftv1.HostedControlPlane) (bool, error) {
				reads++
				return false, nil
			},
		}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cp)}
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, cp)).To(Succeed())
		Expect(cp.Finalizers).To(ContainElement(controlPlaneFinalizer))
		cp.Labels = nil
		Expect(c.Update(ctx, cp)).To(Succeed())
		Expect(c.Delete(ctx, cp)).To(Succeed())
		restarted := &HostedControlPlaneReconciler{Client: c, APIReader: c, Scope: r.Scope, Availability: r.Availability}
		_, err = restarted.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(reads).To(Equal(1), "cleanup must be independent of current case controls")
		Expect(c.Get(ctx, request.NamespacedName, cp)).To(Succeed())
		Expect(cp.Finalizers).To(ConsistOf("foreign.example/cleanup"))
	})
})
