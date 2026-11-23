// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// hypershiftScheme registers provider inputs and status records used by these tests.
func hypershiftScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, hypershiftv1.AddToScheme, capiv1.AddToScheme, routev1.AddToScheme,
		agentv1.AddToScheme, hivev1.AddToScheme, installv1.AddToScheme,
	} {
		Expect(add(scheme)).To(Succeed())
	}

	return scheme
}

// agentHostedCluster supplies a served cluster with an explicit infrastructure identity.
func agentHostedCluster() *hypershiftv1.HostedCluster {
	return &hypershiftv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "cluster", UID: "original-cluster"},
		Spec: hypershiftv1.HostedClusterSpec{
			InfraID:    "cluster",
			PullSecret: corev1.LocalObjectReference{Name: "source-pull-secret"},
			DNS:        hypershiftv1.DNSSpec{BaseDomain: "example.invalid"},
			Services: []hypershiftv1.ServicePublishingStrategyMapping{
				{
					Service: hypershiftv1.Ignition,
					ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{
						Type: hypershiftv1.Route, Route: &hypershiftv1.RoutePublishingStrategy{Hostname: "ignition.example.invalid"},
					},
				},
			},
			Platform: hypershiftv1.PlatformSpec{
				Type:  hypershiftv1.AgentPlatform,
				Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
			},
		},
	}
}

var _ = Describe("HostedCluster observations", func() {
	DescribeTable("copies DNS inputs exactly", func(ctx SpecContext, domain string) {
		hc := agentHostedCluster()
		hc.Spec.DNS.BaseDomain = domain
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hub-cluster", Name: "cluster", UID: "original-capi"},
		}
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c}
		cp, err := r.ensureControlPlane(ctx, hc, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(cp.Spec.DNS).To(Equal(hc.Spec.DNS))
	}, Entry("empty DNS input", ""), Entry("native DNS input", "native.example.invalid"))

	DescribeTable(
		"cleans native control-plane associations independently of simulator markers",
		func(ctx SpecContext, marker string) {
			hc := agentHostedCluster()
			hc.Finalizers = []string{hostedClusterFinalizer, "unrelated.example/cleanup"}

			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name: "hub-cluster", UID: "original-namespace",
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			}}
			if marker != "" {
				namespace.Labels = map[string]string{"app.kubernetes.io/managed-by": marker}
			}

			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace.Name, Name: "cluster", UID: "original-capi",
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			}}
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc, namespace, cluster).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			Expect(c.Delete(ctx, hc)).To(Succeed())

			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)}
			for range 3 {
				_, err := r.Reconcile(ctx, request)
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster))).To(BeTrue())
			Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(namespace), namespace))).To(BeTrue())
			Expect(c.Get(ctx, request.NamespacedName, hc)).To(Succeed())
			Expect(hc.Finalizers).To(ConsistOf("unrelated.example/cleanup"))
		},
		Entry("absent", ""),
		Entry("simulator", "osac-provider-simulator"),
		Entry("other value", "another-controller"),
	)

	It("retains the control-plane namespace after deleting a pending HCP", func(ctx SpecContext) {
		hc := agentHostedCluster()
		hc.Finalizers = []string{hostedClusterFinalizer, "unrelated.example/cleanup"}
		hc.Annotations = map[string]string{hypershiftv1.SkipControlPlaneNamespaceDeletionAnnotation: "true"}
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: "hub-cluster", UID: "original-namespace",
			Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
		}}
		cp := &hypershiftv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name, Name: hc.Name, UID: "pending-control-plane",
			Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
		}}
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc, namespace, cp).Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		Expect(c.Delete(ctx, hc)).To(Succeed())
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)}

		result, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())
		Expect(c.Get(ctx, request.NamespacedName, hc)).To(Succeed())
		Expect(hc.Finalizers).To(ContainElement(hostedClusterFinalizer))
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(cp), cp))).To(BeTrue())

		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(namespace), namespace)).To(Succeed())
		Expect(c.Get(ctx, request.NamespacedName, hc)).To(Succeed())
		Expect(hc.Finalizers).To(ConsistOf("unrelated.example/cleanup"))
	})

	const future = "2099-01-01T00:00:00Z"

	DescribeTable("propagates native pause values without creating control-plane inputs",
		func(ctx SpecContext, value, message string) {
			hc := agentHostedCluster()
			hc.Generation = 7
			hc.Spec.PausedUntil = new(value)
			metadata := metav1.ObjectMeta{
				Namespace: "hub-cluster", Name: "cluster", UID: "original-child",
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			}
			cluster := &capiv1.Cluster{ObjectMeta: *metadata.DeepCopy()}
			cp := &hypershiftv1.HostedControlPlane{ObjectMeta: *metadata.DeepCopy()}
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc, cluster, cp).
				WithStatusSubresource(hc).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
			Expect(cluster.Spec.Paused).To(BeTrue())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(cp), cp)).To(Succeed())
			Expect(cp.Spec.PausedUntil).To(Equal(new(value)))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), hc)).To(Succeed())
			Expect(meta.IsStatusConditionFalse(hc.Status.Conditions, "ReconciliationActive")).To(BeTrue())
			condition := meta.FindStatusCondition(hc.Status.Conditions, "ReconciliationActive")
			Expect(condition).NotTo(BeNil())
			Expect(condition.Reason).To(Equal(hypershiftv1.ReconciliationPausedConditionReason))
			Expect(condition.Message).To(Equal(message))
			Expect(condition.ObservedGeneration).To(Equal(int64(7)))
		},
		Entry("lowercase Boolean", "true", "Reconciliation paused until field removed"),
		Entry("title-case Boolean", "True", "Reconciliation paused until field removed"),
		Entry("future timestamp", future, "Reconciliation paused until: "+future),
	)

	DescribeTable("reports native active and malformed pause conditions",
		func(ctx SpecContext, value *string, reason, message string) {
			hc := agentHostedCluster()
			hc.Spec.PausedUntil, hc.Generation = value, 7
			hc.Spec.SecretEncryption = &hypershiftv1.SecretEncryptionSpec{Type: hypershiftv1.AESCBC}
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc).WithStatusSubresource(hc).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), hc)).To(Succeed())
			condition := meta.FindStatusCondition(hc.Status.Conditions, "ReconciliationActive")
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal(reason))
			Expect(condition.Message).To(Equal(message))
			Expect(condition.ObservedGeneration).To(Equal(int64(7)))
		},
		Entry("unset", nil, hypershiftv1.AsExpectedReason, "Reconciliation active on resource"),
		Entry("false", new("false"), hypershiftv1.AsExpectedReason, "Reconciliation active on resource"),
		Entry("past timestamp", new(time.Now().Add(-time.Hour).Format(time.RFC3339)),
			hypershiftv1.AsExpectedReason, "Reconciliation active on resource"),
		Entry("invalid value", new("invalid"), hypershiftv1.ReconciliationInvalidPausedUntilConditionReason,
			"Invalid value provided for PausedUntil field"),
	)

	DescribeTable("refreshes pause diagnostics while encryption inputs remain unavailable",
		func(ctx SpecContext, value *string, reason, message string, readFailure bool) {
			hc := agentHostedCluster()
			hc.Generation, hc.Spec.PausedUntil = 7, new("true")
			hc.Spec.SecretEncryption = &hypershiftv1.SecretEncryptionSpec{Type: hypershiftv1.AESCBC}

			if readFailure {
				hc.Spec.SecretEncryption.AESCBC = &hypershiftv1.AESCBCSpec{
					ActiveKey: corev1.LocalObjectReference{Name: "active"},
				}
			}

			hc.Status.IgnitionEndpoint = "retained.example.invalid"
			meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
				Type: "Available", Status: metav1.ConditionFalse, Reason: "Retained", ObservedGeneration: 6,
				LastTransitionTime: metav1.NewTime(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.Local)),
			})
			retained := *meta.FindStatusCondition(hc.Status.Conditions, "Available")
			failure := errors.New("encryption read unavailable")
			patches := 0
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc).WithStatusSubresource(hc).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
						object client.Object, opts ...client.GetOption,
					) error {
						if _, ok := object.(*corev1.Secret); ok && key.Name == "active" {
							return failure
						}

						return c.Get(ctx, key, object, opts...)
					},
					SubResourcePatch: func(ctx context.Context, c client.Client, subresource string,
						object client.Object, patch client.Patch, opts ...client.SubResourcePatchOption,
					) error {
						patches++

						return c.SubResource(subresource).Patch(ctx, object, patch, opts...)
					},
				}).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)}

			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, request.NamespacedName, hc)).To(Succeed())
			Expect(meta.IsStatusConditionFalse(hc.Status.Conditions, "ReconciliationActive")).To(BeTrue())

			hc.Spec.PausedUntil, hc.Generation = value, 8
			Expect(c.Update(ctx, hc)).To(Succeed())

			for range 2 {
				_, err = r.Reconcile(ctx, request)
				if readFailure {
					Expect(errors.Is(err, failure)).To(BeTrue())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
			}

			Expect(c.Get(ctx, request.NamespacedName, hc)).To(Succeed())
			condition := meta.FindStatusCondition(hc.Status.Conditions, "ReconciliationActive")
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal(reason))
			Expect(condition.Message).To(Equal(message))
			Expect(condition.ObservedGeneration).To(Equal(int64(8)))
			Expect(hc.UID).To(Equal(types.UID("original-cluster")))
			Expect(hc.Status.IgnitionEndpoint).To(Equal("retained.example.invalid"))
			Expect(*meta.FindStatusCondition(hc.Status.Conditions, "Available")).To(Equal(retained))
			Expect(patches).To(Equal(2), "unchanged diagnostics must skip status patches")

			var planes hypershiftv1.HostedControlPlaneList

			Expect(c.List(ctx, &planes)).To(Succeed())
			Expect(planes.Items).To(BeEmpty())
		},
		Entry("pause removed with pending inputs", nil, hypershiftv1.AsExpectedReason,
			"Reconciliation active on resource", false),
		Entry("malformed pause with pending inputs", new("invalid"),
			hypershiftv1.ReconciliationInvalidPausedUntilConditionReason, "Invalid value provided for PausedUntil field", false),
		Entry("pause removed with an input API error", nil, hypershiftv1.AsExpectedReason,
			"Reconciliation active on resource", true),
	)

	It("returns a diagnostic status conflict before mutating children", func(ctx SpecContext) {
		hc := agentHostedCluster()
		hc.Finalizers = []string{hostedClusterFinalizer}
		writes := 0
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc).WithStatusSubresource(hc).
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
					current := &hypershiftv1.HostedCluster{}
					Expect(c.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
					metav1.SetMetaDataAnnotation(&current.ObjectMeta, "concurrent", "retained")
					Expect(c.Update(ctx, current)).To(Succeed())

					return c.SubResource(subresource).Patch(ctx, object, patch, opts...)
				},
			}).Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		Expect(apierrors.IsConflict(err)).To(BeTrue(), "optimistic status write must detect the concurrent change: %v", err)
		Expect(writes).To(BeZero())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		Expect(hc.Annotations).To(HaveKeyWithValue("concurrent", "retained"))
		Expect(hc.Status.Conditions).To(BeEmpty())
	})

	DescribeTable("rejects unsupported references before publishing a control plane", func(ctx SpecContext, input string) {
		hc := agentHostedCluster()
		if input == "additional trust bundles" {
			hc.Spec.AdditionalTrustBundle = &corev1.LocalObjectReference{Name: "custom-ca"}
		} else {
			hc.Spec.Configuration = &hypershiftv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{ClientCA: configv1.ConfigMapNameReference{Name: "custom-client-ca"}},
			}
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc).WithStatusSubresource(hc).Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		Expect(err).To(MatchError(ContainSubstring(input)))

		var planes hypershiftv1.HostedControlPlaneList

		Expect(c.List(ctx, &planes)).To(Succeed())
		Expect(planes.Items).To(BeEmpty())
	},
		Entry("trust bundle", "additional trust bundles"),
		Entry("API client CA", "configuration Secret and ConfigMap references"),
	)

	It("waits for independent control-plane outputs and follows their published values", func(ctx SpecContext) {
		By("creating inputs without producing CPO status or access credentials")

		cluster := agentHostedCluster()
		cluster.Spec.InfraID = ""
		cluster.Generation = 7
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Namespace, Name: cluster.Spec.PullSecret.Name},
		}
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(cluster, pullSecret).
			WithStatusSubresource(cluster, &hypershiftv1.HostedControlPlane{}).
			WithInterceptorFuncs(interceptor.Funcs{Create: func(
				ctx context.Context,
				c client.WithWatch,
				object client.Object,
				opts ...client.CreateOption,
			) error {
				if _, ok := object.(*capiv1.Cluster); ok {
					stored := &hypershiftv1.HostedCluster{}
					Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), stored)).To(Succeed())
					Expect(stored.Spec.InfraID).To(HavePrefix(cluster.Name + "-"))
					Expect(object.GetName()).To(Equal(stored.Spec.InfraID))
				}

				object.SetUID(types.UID(fmt.Sprintf("%T-%s", object, object.GetName())))

				return c.Create(ctx, object, opts...)
			}}).
			Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
		cp := &hypershiftv1.HostedControlPlane{}
		cpKey := client.ObjectKey{Namespace: "hub-cluster", Name: "cluster"}
		_, err := r.Reconcile(ctx, request)
		Expect(err).To(MatchError(ContainSubstring(corev1.DockerConfigJsonKey)))
		Expect(apierrors.IsNotFound(c.Get(ctx, cpKey, cp))).To(BeTrue())

		pullSecret.Data = map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}
		Expect(c.Update(ctx, pullSecret)).To(Succeed())
		result, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())

		Expect(c.Get(ctx, cpKey, cp)).To(Succeed())
		Expect(cp.Status).To(Equal(hypershiftv1.HostedControlPlaneStatus{}))
		Expect(cp.Annotations).To(Equal(map[string]string{kube.HostedClusterAnnotation: "hub/cluster"}))
		Expect(cp.Spec.Platform.Type).To(Equal(hypershiftv1.NonePlatform))
		Expect(cp.OwnerReferences).To(BeEmpty())

		namespace := &corev1.Namespace{}
		Expect(c.Get(ctx, client.ObjectKey{Name: cp.Namespace}, namespace)).To(Succeed())
		Expect(namespace.Annotations).To(Equal(map[string]string{kube.HostedClusterAnnotation: "hub/cluster"}))

		Expect(c.Get(ctx, request.NamespacedName, cluster)).To(Succeed())
		Expect(cp.Spec.InfraID).To(Equal(cluster.Spec.InfraID))

		native := &capiv1.Cluster{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: cluster.Spec.InfraID}, native)).To(Succeed())
		Expect(native.Annotations).To(Equal(map[string]string{kube.HostedClusterAnnotation: "hub/cluster"}))

		By("recovering native associations while provider adoption is pending")

		for _, object := range []client.Object{namespace, native, cp} {
			object.SetLabels(nil)
			Expect(c.Update(ctx, object)).To(Succeed())
		}

		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		current := &hypershiftv1.HostedControlPlane{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(cp), current)).To(Succeed())
		Expect(current.UID).To(Equal(cp.UID))
		Expect(current.Status).To(Equal(cp.Status))
		cp = current
		delete(cp.Labels, "app.kubernetes.io/managed-by")

		By("observing independent CAPI adoption of the original provider")
		Expect(controllerutil.SetControllerReference(native, cp, c.Scheme())).To(Succeed())
		Expect(c.Update(ctx, cp)).To(Succeed())

		for _, object := range []client.Object{
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: "kube-apiserver"}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: "admin-kubeconfig"}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: "ignition-server-ca-cert"}},
		} {
			err := c.Get(ctx, client.ObjectKeyFromObject(object), object)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "HostedCluster must leave %T creation to CPO", object)
		}

		By("supplying an independent access Secret with a declared source key")

		source := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: cp.Namespace, Name: "published-access"},
			Data:       map[string][]byte{"published": []byte("published")},
		}
		Expect(controllerutil.SetControllerReference(cp, source, c.Scheme())).To(Succeed())
		Expect(c.Create(ctx, source)).To(Succeed())

		for _, step := range []struct {
			description string
			available   metav1.ConditionStatus
			degraded    metav1.ConditionStatus
		}{
			{"observing worker prerequisites while control-plane availability is held", metav1.ConditionFalse, ""},
			{"observing an available but degraded control plane", metav1.ConditionTrue, metav1.ConditionTrue},
			{"observing recovery on the same HostedCluster", metav1.ConditionTrue, metav1.ConditionFalse},
		} {
			By(step.description)

			Expect(c.Get(ctx, client.ObjectKeyFromObject(cp), cp)).To(Succeed())

			cp.Status.KubeConfig = &hypershiftv1.KubeconfigSecretRef{Name: source.Name, Key: "published"}
			cp.Status.ControlPlaneEndpoint = hypershiftv1.APIEndpoint{Host: "192.0.2.99", Port: 6443}
			for _, kind := range []string{"Available", "KubeAPIServerAvailable"} {
				meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
					Type: kind, Status: step.available, Reason: "CPOObserved", ObservedGeneration: 1,
				})
			}

			meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
				Type: "ClusterVersionFailing", Status: metav1.ConditionFalse, Reason: "GuestObserved", ObservedGeneration: 1,
			})

			if step.degraded != "" {
				meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
					Type: "Degraded", Status: step.degraded, Reason: "CPOObserved", Message: "CPO health observation",
					ObservedGeneration: 1,
				})
			}

			Expect(c.Status().Update(ctx, cp)).To(Succeed())
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			observed := &hypershiftv1.HostedCluster{}
			Expect(c.Get(ctx, request.NamespacedName, observed)).To(Succeed())
			Expect(observed.UID).To(Equal(cluster.UID))
			degraded := meta.FindStatusCondition(observed.Status.Conditions, "Degraded")
			Expect(degraded).NotTo(BeNil())
			Expect(degraded.ObservedGeneration).To(Equal(cluster.Generation))

			if step.degraded == "" {
				Expect(degraded.Status).To(Equal(metav1.ConditionUnknown))
			} else {
				Expect(degraded.Status).To(Equal(step.degraded))
				Expect(degraded.Reason).To(Equal("CPOObserved"))
				Expect(degraded.Message).To(Equal("CPO health observation"))
			}

			Expect(meta.IsStatusConditionPresentAndEqual(observed.Status.Conditions, "Available", step.available)).To(BeTrue())
			Expect(meta.IsStatusConditionTrue(observed.Status.Conditions, "ClusterVersionSucceeding")).To(BeTrue())
			Expect(observed.Status.ControlPlaneEndpoint.Host).To(Equal("192.0.2.99"))

			copied := &corev1.Secret{}
			Expect(
				c.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: "cluster-admin-kubeconfig"}, copied),
			).To(Succeed())
			Expect(copied.Data["kubeconfig"]).To(Equal([]byte("published")))

			infra := &agentv1.AgentCluster{}
			Expect(c.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: cp.Name}, infra)).To(Succeed())
		}

		By("restoring the copied kubeconfig through its native Secret deletion event after convergence")

		result, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		copied := &corev1.Secret{}
		Expect(
			c.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: "cluster-admin-kubeconfig"}, copied),
		).To(Succeed())
		Expect(copied.Annotations[kube.HostedClusterAnnotation]).To(Equal("hub/cluster"))
		Expect(c.Delete(ctx, copied)).To(Succeed())
		requests := r.hostedClustersForSecret(ctx, copied)
		Expect(requests).To(ConsistOf(request))
		_, err = r.Reconcile(ctx, requests[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(copied), copied)).To(Succeed())
		Expect(copied.Data["kubeconfig"]).To(Equal([]byte("published")))

		By("waiting for published access when the source Secret disappears")

		Expect(c.Delete(ctx, source)).To(Succeed())
		result, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())

		observed := &hypershiftv1.HostedCluster{}
		Expect(c.Get(ctx, request.NamespacedName, observed)).To(Succeed())
		Expect(observed.Status.KubeConfig).To(BeNil())
		Expect(
			meta.IsStatusConditionTrue(observed.Status.Conditions, "Available"),
		).To(BeTrue(), "HC availability follows HCP availability independently of the access copy")

		By("mapping a source Secret change back to its HostedCluster")

		requests = r.hostedClusterForChild(ctx, source)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].NamespacedName).To(Equal(request.NamespacedName))

		By("deleting the recovered native inputs before retiring the HostedCluster")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(namespace), namespace)).To(Succeed())
		Expect(namespace.Labels).NotTo(HaveKey("app.kubernetes.io/managed-by"))
		Expect(c.Get(ctx, request.NamespacedName, cluster)).To(Succeed())
		Expect(c.Delete(ctx, cluster)).To(Succeed())
		result, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(native), native))).To(BeTrue())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(namespace), namespace)).To(Succeed())

		for range 2 {
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(namespace), namespace))).To(BeTrue())
		Expect(apierrors.IsNotFound(c.Get(ctx, request.NamespacedName, cluster))).To(BeTrue())
	})

	It("waits for a terminating namespace without directly removing provider records", func(ctx SpecContext) {
		cluster := agentHostedCluster()
		cluster.Finalizers, cluster.DeletionTimestamp = []string{hostedClusterFinalizer}, new(metav1.Now())
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: "hub-cluster", UID: "control-plane-namespace", Finalizers: []string{"foreign.example/cleanup"},
			Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
		}}
		deployment := &hivev1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Namespace: namespace.Name, Name: "cluster"}}
		installation := &installv1.AgentClusterInstall{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace.Name, Name: "cluster"},
		}
		c := fake.NewClientBuilder().
			WithScheme(hypershiftScheme()).
			WithObjects(cluster, namespace, deployment, installation).
			Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())

		for _, object := range []client.Object{cluster, deployment, installation} {
			Expect(
				c.Get(ctx, client.ObjectKeyFromObject(object), object),
			).To(Succeed(), "HostedCluster must wait for owning controllers")
		}
	})
})
