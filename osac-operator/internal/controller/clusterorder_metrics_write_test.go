/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/metricswrite"
)

const (
	metricsWriteTestNamespace = "osac-observability"
	metricsWriteTestIssuer    = "osac-metrics-write-ca"
	metricsWriteTestOrder     = "order-metrics"
	metricsWriteTestCluster   = "prod-east"
)

func metricsWriteTestConfig() MetricsWriteConfig {
	return MetricsWriteConfig{
		Enabled:             true,
		Namespace:           metricsWriteTestNamespace,
		WriteURL:            "https://thanos-receive-managed-osac-observability.apps.example.com/api/v1/receive",
		Issuer:              metricsWriteTestIssuer,
		RevocationConfigMap: "osac-metrics-write-revocation",
		CertificateDuration: "90d",
		RenewBefore:         "30d",
		ScrapeInterval:      "30s",
		KeepKubePrefix:      "kube_node_",
		DropPrefixes:        []string{"container_", "claim", "mount", "kserve_", "vllm_", "dcgm_"},
	}
}

func metricsWriteTestInstance() *v1alpha1.ClusterOrder {
	return &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
		Name:      metricsWriteTestOrder,
		Namespace: "orders",
		UID:       types.UID("order-uid"),
	}}
}

func metricsWriteTestHostedCluster() *hypershiftv1beta1.HostedCluster {
	hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      metricsWriteTestCluster,
		Namespace: "clusters-prod-east",
		Labels:    map[string]string{osacClusterOrderNameLabel: metricsWriteTestOrder},
	}}
	hc.Status.Conditions = []metav1.Condition{
		{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: "Available"},
		{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, Reason: "AsExpected"},
	}
	return hc
}

func newMetricsWriteIssuer(ready bool) *unstructured.Unstructured {
	issuer := &unstructured.Unstructured{}
	issuer.SetAPIVersion(metricswrite.CertificateAPIVersion)
	issuer.SetKind(metricswrite.IssuerKind)
	issuer.SetNamespace(metricsWriteTestNamespace)
	issuer.SetName(metricsWriteTestIssuer)
	status := "False"
	if ready {
		status = "True"
	}
	Expect(unstructured.SetNestedSlice(issuer.Object, []any{
		map[string]any{"type": "Ready", "status": status, "reason": "KeyPairVerified"},
	}, "status", "conditions")).To(Succeed())
	return issuer
}

func metricsWriteCertificateKey() client.ObjectKey {
	return client.ObjectKey{
		Namespace: metricsWriteTestNamespace,
		Name:      metricswrite.CertificateName(metricsWriteTestOrder),
	}
}

func newMetricsWriteCertificate() *unstructured.Unstructured {
	certificate := &unstructured.Unstructured{}
	certificate.SetAPIVersion(metricswrite.CertificateAPIVersion)
	certificate.SetKind(metricswrite.CertificateKind)
	certificate.SetNamespace(metricsWriteCertificateKey().Namespace)
	certificate.SetName(metricsWriteCertificateKey().Name)
	return certificate
}

// newClientIdentity returns PEM material shaped like a cert-manager issued
// Secret. Only the serial and parseability matter to the operator.
func newClientIdentity() (caPEM, certPEM, keyPEM []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(0x5eed),
		Subject:      pkix.Name{CommonName: metricsWriteTestCluster},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	keyDER, err := x509.MarshalECPrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return caPEM, certPEM, keyPEM
}

func issuedClientSecret() *corev1.Secret {
	caPEM, certPEM, keyPEM := newClientIdentity()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      metricswrite.CertificateName(metricsWriteTestOrder),
			Namespace: metricsWriteTestNamespace,
		},
		Data: map[string][]byte{
			metricswrite.CAKey:   caPEM,
			metricswrite.CertKey: certPEM,
			metricswrite.KeyKey:  keyPEM,
		},
	}
}

func tenantMonitoringConfigMap(config string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      metricswrite.MonitoringConfigName,
			Namespace: metricswrite.MonitoringNamespace,
		},
		Data: map[string]string{metricswrite.MonitoringConfigKey: config},
	}
}

func tenantMonitoringConfig() string {
	return `enableUserWorkload: true
prometheusK8s:
  remoteWrite:
    - name: tenant-feed
      url: https://other.example.com/api/v1/receive
    - name: osac
      url: https://stale.example.com/api/v1/receive
`
}

// metricsWriteScheme registers the cert-manager kinds as unstructured so the
// fake hub client can hold Certificates and Issuers without the cert-manager
// Go module.
func metricsWriteScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(hypershiftv1beta1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	for _, kind := range []string{metricswrite.IssuerKind, metricswrite.CertificateKind} {
		gvk := schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: kind}
		scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(gvk.GroupVersion().WithKind(kind+"List"), &unstructured.UnstructuredList{})
	}
	return scheme
}

func newMetricsWriteReconciler(
	hub client.Client,
	cfg MetricsWriteConfig,
	guest kubernetes.Interface,
	ready, reachable bool,
) *ClusterOrderReconciler {
	return &ClusterOrderReconciler{
		Client:             hub,
		apiReader:          hub,
		Scheme:             hub.Scheme(),
		MetricsWrite:       cfg,
		StatusPollInterval: 30 * time.Second,
		guestClusterClientOverride: func(
			context.Context, *hypershiftv1beta1.HostedCluster,
		) (kubernetes.Interface, bool, bool, error) {
			return guest, ready, reachable, nil
		},
	}
}

var _ = Describe("ClusterOrder tenant remote write", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("is a no-op when tenant remote write is disabled", func() {
		hub := fake.NewClientBuilder().WithScheme(metricsWriteScheme()).Build()
		guest := k8sfake.NewSimpleClientset()
		reconciler := newMetricsWriteReconciler(hub, MetricsWriteConfig{}, guest, true, true)

		result, err := reconciler.reconcileClusterMetricsWrite(ctx, metricsWriteTestInstance(), metricsWriteTestHostedCluster())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		revoked, _, err := reconciler.revokeClusterMetricsWrite(ctx, metricsWriteTestInstance())
		Expect(err).NotTo(HaveOccurred())
		Expect(revoked).To(BeTrue())
		Expect(guest.Actions()).To(BeEmpty())

		existing := newMetricsWriteCertificate()
		Expect(apierrors.IsNotFound(hub.Get(ctx, metricsWriteCertificateKey(), existing))).To(BeTrue())
	})

	It("waits for the write CA issuer before creating a client Certificate", func() {
		guest := k8sfake.NewSimpleClientset()
		hub := fake.NewClientBuilder().WithScheme(metricsWriteScheme()).Build()

		missing := newMetricsWriteReconciler(hub, metricsWriteTestConfig(), guest, true, true)
		result, err := missing.reconcileClusterMetricsWrite(ctx, metricsWriteTestInstance(), metricsWriteTestHostedCluster())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(30 * time.Second))
		Expect(apierrors.IsNotFound(hub.Get(ctx, metricsWriteCertificateKey(), newMetricsWriteCertificate()))).To(BeTrue())

		notReady := fake.NewClientBuilder().
			WithScheme(metricsWriteScheme()).
			WithObjects(newMetricsWriteIssuer(false)).
			Build()
		pending := newMetricsWriteReconciler(notReady, metricsWriteTestConfig(), guest, true, true)
		result, err = pending.reconcileClusterMetricsWrite(ctx, metricsWriteTestInstance(), metricsWriteTestHostedCluster())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(30 * time.Second))
		Expect(apierrors.IsNotFound(notReady.Get(ctx, metricsWriteCertificateKey(), newMetricsWriteCertificate()))).To(BeTrue())

		ready := fake.NewClientBuilder().
			WithScheme(metricsWriteScheme()).
			WithObjects(newMetricsWriteIssuer(true)).
			Build()
		applied := newMetricsWriteReconciler(ready, metricsWriteTestConfig(), guest, true, true)
		result, err = applied.reconcileClusterMetricsWrite(ctx, metricsWriteTestInstance(), metricsWriteTestHostedCluster())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(30 * time.Second))

		certificate := newMetricsWriteCertificate()
		Expect(ready.Get(ctx, metricsWriteCertificateKey(), certificate)).To(Succeed())
		Expect(certificate.GetLabels()).To(HaveKeyWithValue(metricswrite.ClusterOrderLabel, metricsWriteTestOrder))
		Expect(certificate.GetAnnotations()).To(HaveKeyWithValue(
			metricswrite.ClusterOrderUIDAnnotation, "order-uid"))
		spec, _, err := unstructured.NestedMap(certificate.Object, "spec")
		Expect(err).NotTo(HaveOccurred())
		Expect(spec).To(HaveKeyWithValue("commonName", metricsWriteTestCluster))
		Expect(spec).To(HaveKeyWithValue("secretName", metricswrite.CertificateName(metricsWriteTestOrder)))
		Expect(spec).To(HaveKeyWithValue("duration", "90d"))
		Expect(spec).To(HaveKeyWithValue("renewBefore", "30d"))
		Expect(spec).To(HaveKeyWithValue("usages", []any{"client auth"}))
		Expect(spec).To(HaveKeyWithValue("issuerRef", map[string]any{
			"name":  metricsWriteTestIssuer,
			"kind":  metricswrite.IssuerKind,
			"group": "cert-manager.io",
		}))
		// No issued Secret yet, so nothing reaches the tenant cluster.
		Expect(guest.Actions()).To(BeEmpty())
	})

	It("does not create a Certificate while the tenant cluster is unreachable", func() {
		hub := fake.NewClientBuilder().
			WithScheme(metricsWriteScheme()).
			WithObjects(newMetricsWriteIssuer(true)).
			Build()
		guest := k8sfake.NewSimpleClientset()
		reconciler := newMetricsWriteReconciler(hub, metricsWriteTestConfig(), guest, false, false)

		result, err := reconciler.reconcileClusterMetricsWrite(ctx, metricsWriteTestInstance(), metricsWriteTestHostedCluster())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(30 * time.Second))
		Expect(apierrors.IsNotFound(hub.Get(ctx, metricsWriteCertificateKey(), newMetricsWriteCertificate()))).To(BeTrue())
		Expect(guest.Actions()).To(BeEmpty())
	})

	It("replaces a Certificate that names a different hosted cluster", func() {
		existing := newMetricsWriteCertificate()
		Expect(unstructured.SetNestedField(existing.Object, "stale-cluster", "spec", "commonName")).To(Succeed())
		hub := fake.NewClientBuilder().
			WithScheme(metricsWriteScheme()).
			WithObjects(newMetricsWriteIssuer(true), existing).
			Build()
		guest := k8sfake.NewSimpleClientset(tenantMonitoringConfigMap(""), issuedClientSecret())
		reconciler := newMetricsWriteReconciler(hub, metricsWriteTestConfig(), guest, true, true)

		_, err := reconciler.reconcileClusterMetricsWrite(ctx, metricsWriteTestInstance(), metricsWriteTestHostedCluster())
		Expect(err).NotTo(HaveOccurred())

		certificate := newMetricsWriteCertificate()
		Expect(hub.Get(ctx, metricsWriteCertificateKey(), certificate)).To(Succeed())
		commonName, _, err := unstructured.NestedString(certificate.Object, "spec", "commonName")
		Expect(err).NotTo(HaveOccurred())
		Expect(commonName).To(Equal(metricsWriteTestCluster))
	})

	It("writes the client identity and the remote-write entry to the tenant cluster", func() {
		hub := fake.NewClientBuilder().
			WithScheme(metricsWriteScheme()).
			WithObjects(newMetricsWriteIssuer(true), issuedClientSecret()).
			Build()
		guest := k8sfake.NewSimpleClientset()
		reconciler := newMetricsWriteReconciler(hub, metricsWriteTestConfig(), guest, true, true)

		_, err := reconciler.reconcileClusterMetricsWrite(ctx, metricsWriteTestInstance(), metricsWriteTestHostedCluster())
		Expect(err).NotTo(HaveOccurred())

		secrets := guest.CoreV1().Secrets(metricswrite.MonitoringNamespace)
		for _, name := range []string{
			metricswrite.CABundleSecretName,
			metricswrite.ClientCertSecretName,
			metricswrite.ClientKeySecretName,
		} {
			secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "tenant Secret %s", name)
			Expect(secret.Labels).To(HaveKeyWithValue(metricswrite.ClusterOrderLabel, metricsWriteTestOrder))
			Expect(secret.Data).To(HaveLen(1))
		}

		configMap, err := guest.CoreV1().ConfigMaps(metricswrite.MonitoringNamespace).
			Get(ctx, metricswrite.MonitoringConfigName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		config := configMap.Data[metricswrite.MonitoringConfigKey]
		Expect(config).To(ContainSubstring("name: osac"))
		Expect(config).To(ContainSubstring(metricsWriteTestConfig().WriteURL))
		Expect(config).To(ContainSubstring("scrapeInterval: 30s"))
		Expect(config).To(ContainSubstring("name: " + metricswrite.CABundleSecretName))
		Expect(config).To(ContainSubstring("name: " + metricswrite.ClientCertSecretName))
		Expect(config).To(ContainSubstring("name: " + metricswrite.ClientKeySecretName))
	})

	It("installs the client identity through the cert-manager API", func() {
		namespace := "metrics-write-envtest"
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		}))).To(Succeed())

		issuer := &unstructured.Unstructured{}
		issuer.SetAPIVersion(metricswrite.CertificateAPIVersion)
		issuer.SetKind(metricswrite.IssuerKind)
		issuer.SetNamespace(namespace)
		issuer.SetName(metricsWriteTestIssuer)
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, issuer))).To(Succeed())
		Expect(unstructured.SetNestedSlice(issuer.Object, []any{
			map[string]any{"type": "Ready", "status": "True", "reason": "KeyPairVerified"},
		}, "status", "conditions")).To(Succeed())
		Expect(k8sClient.Status().Update(ctx, issuer)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, issuer) })

		issued := issuedClientSecret()
		issued.SetNamespace(namespace)
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, issued))).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, issued) })

		cfg := metricsWriteTestConfig()
		cfg.Namespace = namespace
		guest := k8sfake.NewSimpleClientset()
		reconciler := newMetricsWriteReconciler(k8sClient, cfg, guest, true, true)
		result, err := reconciler.reconcileClusterMetricsWrite(
			ctx, metricsWriteTestInstance(), metricsWriteTestHostedCluster())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(30 * time.Second))

		key := client.ObjectKey{
			Namespace: namespace,
			Name:      metricswrite.CertificateName(metricsWriteTestOrder),
		}
		certificate := newMetricsWriteCertificate()
		Expect(k8sClient.Get(ctx, key, certificate)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, certificate) })
		commonName, _, err := unstructured.NestedString(certificate.Object, "spec", "commonName")
		Expect(err).NotTo(HaveOccurred())
		Expect(commonName).To(Equal(metricsWriteTestCluster))
		usages, _, err := unstructured.NestedStringSlice(certificate.Object, "spec", "usages")
		Expect(err).NotTo(HaveOccurred())
		Expect(usages).To(Equal([]string{"client auth"}))

		configMap, err := guest.CoreV1().ConfigMaps(metricswrite.MonitoringNamespace).
			Get(ctx, metricswrite.MonitoringConfigName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap.Data[metricswrite.MonitoringConfigKey]).To(ContainSubstring("name: osac"))
	})

	It("revokes in order: record the serial, delete the Certificate, then remove the entry", func() {
		steps := []string{}
		revocation := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      metricsWriteTestConfig().RevocationConfigMap,
				Namespace: metricsWriteTestNamespace,
			},
			Data: map[string]string{metricswrite.RevocationKey: "aa"},
		}
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   "clusters-prod-east",
			Labels: map[string]string{osacClusterOrderNameLabel: metricsWriteTestOrder},
		}}
		hub := fake.NewClientBuilder().
			WithScheme(metricsWriteScheme()).
			WithObjects(
				revocation,
				newMetricsWriteCertificate(),
				issuedClientSecret(),
				metricsWriteTestHostedCluster(),
				namespace,
			).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(
					updateCtx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption,
				) error {
					if _, ok := obj.(*corev1.ConfigMap); ok {
						certificate := newMetricsWriteCertificate()
						if err := c.Get(updateCtx, metricsWriteCertificateKey(), certificate); err == nil {
							steps = append(steps, "recorded-serial-before-certificate-deletion")
						}
					}
					return c.Update(updateCtx, obj, opts...)
				},
			}).
			Build()

		guest := k8sfake.NewSimpleClientset(tenantMonitoringConfigMap(tenantMonitoringConfig()))
		guest.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
			certificate := newMetricsWriteCertificate()
			if apierrors.IsNotFound(hub.Get(ctx, metricsWriteCertificateKey(), certificate)) {
				revoked := &corev1.ConfigMap{}
				Expect(hub.Get(ctx, client.ObjectKeyFromObject(revocation), revoked)).To(Succeed())
				Expect(revoked.Data[metricswrite.RevocationKey]).To(ContainSubstring("5eed"))
				steps = append(steps, "removed-entry-after-certificate-deletion")
			}
			return false, nil, nil
		})

		reconciler := newMetricsWriteReconciler(hub, metricsWriteTestConfig(), guest, true, true)
		revoked, _, err := reconciler.revokeClusterMetricsWrite(ctx, metricsWriteTestInstance())
		Expect(err).NotTo(HaveOccurred())
		Expect(revoked).To(BeTrue())
		Expect(steps).To(Equal([]string{
			"recorded-serial-before-certificate-deletion",
			"removed-entry-after-certificate-deletion",
		}))

		stored := &corev1.ConfigMap{}
		Expect(hub.Get(ctx, client.ObjectKeyFromObject(revocation), stored)).To(Succeed())
		Expect(stored.Data[metricswrite.RevocationKey]).To(Equal("aa\n5eed"))
		Expect(apierrors.IsNotFound(hub.Get(ctx, metricsWriteCertificateKey(), newMetricsWriteCertificate()))).To(BeTrue())

		configMap, err := guest.CoreV1().ConfigMaps(metricswrite.MonitoringNamespace).
			Get(ctx, metricswrite.MonitoringConfigName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		config := configMap.Data[metricswrite.MonitoringConfigKey]
		Expect(config).To(ContainSubstring("name: tenant-feed"))
		Expect(config).NotTo(ContainSubstring("name: osac"))
	})

	It("still deletes the Certificate and removes the entry when the issued Secret is gone", func() {
		revocation := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      metricsWriteTestConfig().RevocationConfigMap,
				Namespace: metricsWriteTestNamespace,
			},
			Data: map[string]string{metricswrite.RevocationKey: "aa"},
		}
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   "clusters-prod-east",
			Labels: map[string]string{osacClusterOrderNameLabel: metricsWriteTestOrder},
		}}
		hub := fake.NewClientBuilder().
			WithScheme(metricsWriteScheme()).
			WithObjects(
				revocation,
				newMetricsWriteCertificate(),
				metricsWriteTestHostedCluster(),
				namespace,
			).
			Build()
		guest := k8sfake.NewSimpleClientset(tenantMonitoringConfigMap(tenantMonitoringConfig()))
		reconciler := newMetricsWriteReconciler(hub, metricsWriteTestConfig(), guest, true, true)

		revoked, _, err := reconciler.revokeClusterMetricsWrite(ctx, metricsWriteTestInstance())
		Expect(err).NotTo(HaveOccurred())
		Expect(revoked).To(BeTrue())

		stored := &corev1.ConfigMap{}
		Expect(hub.Get(ctx, client.ObjectKeyFromObject(revocation), stored)).To(Succeed())
		Expect(stored.Data[metricswrite.RevocationKey]).To(Equal("aa"))
		Expect(apierrors.IsNotFound(hub.Get(ctx, metricsWriteCertificateKey(), newMetricsWriteCertificate()))).To(BeTrue())

		configMap, err := guest.CoreV1().ConfigMaps(metricswrite.MonitoringNamespace).
			Get(ctx, metricswrite.MonitoringConfigName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap.Data[metricswrite.MonitoringConfigKey]).NotTo(ContainSubstring("name: osac"))
	})
})
