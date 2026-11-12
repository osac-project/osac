// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cpo

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

	DescribeTable("follows native Boolean and timestamp pause semantics",
		func(ctx SpecContext, value *string, paused bool) {
			cp := &hypershiftv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original"},
				Spec: hypershiftv1.HostedControlPlaneSpec{
					PausedUntil: value,
					Platform: hypershiftv1.PlatformSpec{
						Type: hypershiftv1.NonePlatform, Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
					},
				},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).WithStatusSubresource(cp).Build()
			observed := false
			r := &HostedControlPlaneReconciler{
				Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
				Availability: func(context.Context, *hypershiftv1.HostedControlPlane) (bool, bool, error) {
					observed = true

					return false, false, nil
				},
			}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cp)})
			Expect(err).NotTo(HaveOccurred())
			Expect(observed).To(Equal(!paused))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(cp), cp)).To(Succeed())
			Expect(meta.IsStatusConditionFalse(cp.Status.Conditions, "ReconciliationActive")).To(Equal(paused))
		},
		Entry("unset", nil, false), Entry("false", new("false"), false), Entry("true", new("true"), true),
		Entry("native Boolean alternative", new("TRUE"), true),
		Entry("future timestamp", new(time.Now().Add(time.Hour).Format(time.RFC3339)), true),
		Entry("past timestamp", new(time.Now().Add(-time.Hour).Format(time.RFC3339)), false),
		Entry("invalid input remains unpaused", new("invalid"), false),
	)

	It("keeps a valid signing pair across retries and recovery", func(ctx SpecContext) {
		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original"},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).Build()
		r := &HostedControlPlaneReconciler{Client: c, APIReader: c}
		Expect(r.ensureIgnitionCA(ctx, cp)).To(Succeed())

		ca := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: "ignition-server-ca-cert"}, ca)).To(Succeed())
		_, err := tls.X509KeyPair(ca.Data[corev1.TLSCertKey], ca.Data[corev1.TLSPrivateKeyKey])
		Expect(err).NotTo(HaveOccurred())

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

	It("publishes a parseable placeholder kubeconfig without guest credentials", func() {
		config, err := clientcmd.Load([]byte(placeholderGuestKubeconfig))
		Expect(err).NotTo(HaveOccurred())
		Expect(clientcmd.Validate(*config)).To(Succeed())
		current := config.Contexts[config.CurrentContext]
		Expect(current).NotTo(BeNil())
		Expect(config.Clusters[current.Cluster].Server).To(Equal("https://guest.caas-it.invalid:6443"))
		data, err := json.Marshal(config.AuthInfos[current.AuthInfo])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("{}"))
	})

	It(
		"recovers availability on the original HCP while retaining worker inputs and guest observations",
		func(ctx SpecContext) {
			By("preparing a held control plane with an independent guest condition")

			controlPlane := &hypershiftv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "control-plane", Name: "cluster", UID: "original-control-plane", Generation: 7,
				},
				Spec: hypershiftv1.HostedControlPlaneSpec{
					Services: []hypershiftv1.ServicePublishingStrategyMapping{
						{
							Service:                   hypershiftv1.APIServer,
							ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{Type: hypershiftv1.LoadBalancer},
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
				Availability: func(_ context.Context, observed *hypershiftv1.HostedControlPlane) (bool, bool, error) {
					Expect(observed.UID).To(Equal(controlPlane.UID))

					return available, true, readError
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
			Expect(held.Status.KubeConfig).NotTo(BeNil())
			Expect(held.Status.KubeConfig.Name).NotTo(BeEmpty())

			access := &corev1.Secret{}
			Expect(
				c.Get(ctx, client.ObjectKey{Namespace: held.Namespace, Name: held.Status.KubeConfig.Name}, access),
			).To(Succeed())
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

			readError = errors.New("settings unavailable")
			_, err = r.Reconcile(ctx, request)
			Expect(errors.Is(err, readError)).To(BeTrue(), "original failure lost: %v", err)
		},
	)

	It("keeps an older control plane's Service and Secret unchanged", func(ctx SpecContext) {
		current := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "new"},
		}
		previous := current.DeepCopy()
		previous.UID = "old"

		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "kube-apiserver"}}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster-admin-kubeconfig"}}

		Expect(controllerutil.SetControllerReference(previous, service, scheme)).To(Succeed())
		Expect(controllerutil.SetControllerReference(previous, secret, scheme)).To(Succeed())
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service, secret).Build()
		r := &HostedControlPlaneReconciler{Client: c, APIReader: c}

		_, err := r.ensureAPIService(ctx, current)
		Expect(err).To(MatchError(ContainSubstring("another control plane")))
		_, err = r.ensureKubeconfig(ctx, current)
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
			observed, err := r.ensureAPIService(ctx, cp)
			Expect(err).NotTo(HaveOccurred())
			Expect(observed.Spec.ClusterIP).To(Equal("10.0.0.10"))
			Expect(observed.Spec.ClusterIPs).To(ConsistOf("10.0.0.10"))
			Expect(observed.Spec.Ports[0].NodePort).To(Equal(int32(31443)))
			Expect(observed.Spec.Ports[0].Port).To(Equal(int32(6443)))
			Expect(observed.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			Expect(observed.Spec.Ports[0].TargetPort.StrVal).To(Equal("client"))
			Expect(observed.Finalizers).To(ConsistOf("foreign.example/cleanup"))

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
		backend := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Namespace: cp.Namespace, Name: "ignition-server", UID: "original-backend",
			Annotations: map[string]string{"retained": "yes"},
		}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, backend).WithStatusSubresource(cp).Build()
		r := &HostedControlPlaneReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}},
			Availability: func(context.Context, *hypershiftv1.HostedControlPlane) (bool, bool, error) {
				return false, true, nil
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
				Labels:     map[string]string{kube.ManagedByLabel: kube.ManagerName},
				Finalizers: []string{"foreign.example/cleanup"},
			},
			Spec: hypershiftv1.HostedControlPlaneSpec{
				Platform: hypershiftv1.PlatformSpec{
					Type:  hypershiftv1.NonePlatform,
					Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).Build()
		reads := 0
		r := &HostedControlPlaneReconciler{
			Client:    c,
			APIReader: c,
			Scope:     kube.Scope{AgentNamespaces: []string{"hub"}},
			Availability: func(context.Context, *hypershiftv1.HostedControlPlane) (bool, bool, error) {
				reads++
				return false, false, nil
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
