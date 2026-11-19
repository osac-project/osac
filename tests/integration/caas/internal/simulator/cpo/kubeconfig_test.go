// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cpo

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Control-plane kubeconfig publication", func() {
	DescribeTable("encodes the observed endpoint without guest credentials", func(name, host, server string) {
		data, err := guestKubeconfig(name, host, 6443)
		Expect(err).NotTo(HaveOccurred())
		config, err := clientcmd.Load(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(clientcmd.Validate(*config)).To(Succeed())
		current := config.Contexts[config.CurrentContext]
		Expect(current).NotTo(BeNil())
		Expect(current.Cluster).To(Equal(name))
		Expect(config.Clusters[current.Cluster].Server).To(Equal(server))
		Expect(config.Clusters[current.Cluster].InsecureSkipTLSVerify).To(BeFalse())
		credentials, err := json.Marshal(config.AuthInfos[current.AuthInfo])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(credentials)).To(Equal("{}"))
	},
		Entry("a first control plane", "first", "192.0.2.10", "https://192.0.2.10:6443"),
		Entry("a distinct control plane", "second", "api.example.invalid", "https://api.example.invalid:6443"),
		Entry("an IPv6 endpoint", "ipv6", "2001:db8::10", "https://[2001:db8::10]:6443"),
	)

	DescribeTable("publishes allocation independently and preserves Secret identity across endpoint changes",
		func(ctx SpecContext, strategy hypershiftv1.PublishingStrategyType) {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			Expect(hypershiftv1.AddToScheme(scheme)).To(Succeed())

			cp := &hypershiftv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster", UID: "original-hcp"},
				Spec: hypershiftv1.HostedControlPlaneSpec{Services: []hypershiftv1.ServicePublishingStrategyMapping{{
					Service: hypershiftv1.APIServer,
					ServicePublishingStrategy: hypershiftv1.ServicePublishingStrategy{
						Type: strategy, NodePort: &hypershiftv1.NodePortPublishingStrategy{Address: "192.0.2.10"},
					},
				}}},
			}
			service := &corev1.Service{Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 6443}}}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption) error {
					object.SetUID("original-secret")
					return c.Create(ctx, object, opts...)
				},
			}).Build()
			r := &HostedControlPlaneReconciler{Client: c, APIReader: c}

			By("waiting without publishing an unallocated endpoint or dangling reference")

			ref, err := r.ensureKubeconfig(ctx, cp, service)
			Expect(err).NotTo(HaveOccurred())
			Expect(ref).To(BeNil())

			var secrets corev1.SecretList

			Expect(c.List(ctx, &secrets)).To(Succeed())
			Expect(secrets.Items).To(BeEmpty())
			Expect(controlPlaneStatus(cp, service, nil, false, nil).Status.KubeConfig).To(BeNil())

			By("publishing and updating the native endpoint on the original Secret")

			secret := &corev1.Secret{}

			for _, host := range []string{"192.0.2.10", "192.0.2.20"} {
				cp.Spec.Services[0].NodePort.Address = host
				service.Spec.Ports[0].NodePort = 31443
				service.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: host}}
				ref, err = r.ensureKubeconfig(ctx, cp, service)
				Expect(err).NotTo(HaveOccurred())
				Expect(ref).To(Equal(&hypershiftv1.KubeconfigSecretRef{Name: "admin-kubeconfig", Key: "kubeconfig"}))
				Expect(c.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: ref.Name}, secret)).To(Succeed())
				Expect(secret.UID).To(BeEquivalentTo("original-secret"))
				config, err := clientcmd.Load(secret.Data["kubeconfig"])
				Expect(err).NotTo(HaveOccurred())

				expectedPort := "6443"
				if strategy == hypershiftv1.NodePort {
					expectedPort = "31443"
				}

				Expect(config.Clusters[cp.Name].Server).To(Equal("https://" + host + ":" + expectedPort))

				secret.Data["unrelated"] = []byte("retained")
				Expect(c.Update(ctx, secret)).To(Succeed())
			}

			By("preserving published data while allocation observations are missing")

			before := secret.DeepCopy()
			service.Spec.Ports[0].NodePort = 0
			service.Status.LoadBalancer.Ingress = nil
			restarted := &HostedControlPlaneReconciler{Client: c, APIReader: c}
			pending, err := restarted.ensureKubeconfig(ctx, cp, service)
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(BeNil())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(secret), secret)).To(Succeed())
			Expect(secret).To(Equal(before))
		}, Entry("NodePort", hypershiftv1.NodePort), Entry("LoadBalancer", hypershiftv1.LoadBalancer))
})
