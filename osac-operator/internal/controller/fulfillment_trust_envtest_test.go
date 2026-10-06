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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/trustadmission"
)

type fakeTrustTarget struct {
	currentHashes map[string]bool
	unsupported   bool
	autoConverge  bool
	applyErr      error
	published     []trustadmission.ExpectedBundle
	applied       []string
	revoked       []trustadmission.RecordKey
	events        []string
}

func (t *fakeTrustTarget) Observe(_ context.Context, expected trustadmission.ExpectedBundle) (bool, bool, error) {
	return t.currentHashes[expected.Key.BundleSHA256], t.unsupported, nil
}
func (t *fakeTrustTarget) Publish(_ context.Context, record trustadmission.ExpectedBundle) error {
	t.published = append(t.published, record)
	t.events = append(t.events, "publish")
	return nil
}
func (t *fakeTrustTarget) Apply(_ context.Context, record trustadmission.ExpectedBundle) error {
	t.applied = append(t.applied, record.Key.BundleSHA256)
	t.events = append(t.events, "apply")
	if t.applyErr != nil {
		return t.applyErr
	}
	if t.autoConverge {
		t.currentHashes[record.Key.BundleSHA256] = true
	}
	return nil
}
func (t *fakeTrustTarget) Revoke(_ context.Context, key trustadmission.RecordKey) error {
	t.revoked = append(t.revoked, key)
	t.events = append(t.events, "revoke")
	return nil
}

type fakeTrustResolver struct {
	target FulfillmentTrustTarget
	err    error
}

func (r fakeTrustResolver) Resolve(_ context.Context, _ *v1alpha1.ClusterOrder) (FulfillmentTrustTarget, error) {
	return r.target, r.err
}

type fakeKubeconfigReader struct{ data []byte }

func (r fakeKubeconfigReader) Read(context.Context, *v1alpha1.ClusterOrder) ([]byte, error) {
	return r.data, nil
}

type fakeTrustTokenIssuer struct {
	names []string
	now   func() time.Time
}

func (i *fakeTrustTokenIssuer) Issue(_ context.Context, _ []byte, namespace, name, tenant, owner string) (TrustServiceAccountToken, error) {
	Expect(namespace).To(Equal("osac-csi"))
	Expect(tenant).To(Equal("tenant-a"))
	Expect(owner).To(Equal("order-uid"))
	i.names = append(i.names, name)
	now := time.Now()
	if i.now != nil {
		now = i.now()
	}
	return TrustServiceAccountToken{Token: "scoped-" + name, ExpiresAt: now.Add(trustTokenLifetime)}, nil
}

type statusPatchConflictClient struct {
	client.Client
	beforeFirstPatch func(context.Context) error
	patchCalls       int
}

func (c *statusPatchConflictClient) Status() client.SubResourceWriter {
	return &statusPatchConflictWriter{SubResourceWriter: c.Client.Status(), client: c}
}

type statusPatchConflictWriter struct {
	client.SubResourceWriter
	client *statusPatchConflictClient
}

func (w *statusPatchConflictWriter) Patch(
	ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption,
) error {
	w.client.patchCalls++
	if w.client.patchCalls == 1 && w.client.beforeFirstPatch != nil {
		if err := w.client.beforeFirstPatch(ctx); err != nil {
			return err
		}
	}
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

var _ = Describe("FulfillmentTrustReconciler", func() {
	var (
		ctx        context.Context
		order      *v1alpha1.ClusterOrder
		reconciler *FulfillmentTrustReconciler
		target     *fakeTrustTarget
	)

	BeforeEach(func() {
		ctx = context.Background()
		order = &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			GenerateName: "trust-order-", Namespace: "default",
			Annotations: map[string]string{
				trustadmission.TenantAnnotation: "tenant-a",
			},
		}, Spec: v1alpha1.ClusterOrderSpec{TemplateID: "test.template"}}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		order.Status.ClusterReference = &v1alpha1.ClusterOrderClusterReferenceType{Namespace: "hosted", HostedClusterName: "cluster"}
		Expect(k8sClient.Status().Update(ctx, order)).To(Succeed())
		target = &fakeTrustTarget{currentHashes: make(map[string]bool), autoConverge: true}
		reconciler = &FulfillmentTrustReconciler{
			Client: k8sClient, APIReader: k8sClient, Enabled: true,
			ClusterOrderNamespace: "default", SourceNamespace: "default", TenantNamespace: "osac-csi",
			SourceName: "missing", Targets: fakeTrustResolver{target: target}, PollInterval: time.Second,
		}
	})

	AfterEach(func() {
		stored := &v1alpha1.ClusterOrder{}
		if k8sClient.Get(ctx, client.ObjectKeyFromObject(order), stored) == nil {
			stored.Finalizers = nil
			Expect(k8sClient.Update(ctx, stored)).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, stored))).To(Succeed())
		}
	})

	reconcile := func() ctrlreconcile.Result {
		result, err := reconciler.Reconcile(ctx, ctrlreconcile.Request{NamespacedName: client.ObjectKeyFromObject(order)})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		return result
	}
	getOrder := func() *v1alpha1.ClusterOrder {
		stored := &v1alpha1.ClusterOrder{}
		ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(order), stored)).To(Succeed())
		return stored
	}
	addBundle := func() []byte {
		bundle := testTrustPEM()
		configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{GenerateName: "trust-source-", Namespace: "default"},
			Data: map[string]string{trustadmission.BundleDataKey: string(bundle)}}
		ExpectWithOffset(1, k8sClient.Create(ctx, configMap)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, configMap)).To(Succeed()) })
		reconciler.SourceName = configMap.Name
		return bundle
	}

	It("does no work while disabled", func() {
		reconciler.Enabled = false
		reconcile()
		Expect(getOrder().Status.Conditions).To(BeEmpty())
		Expect(target.published).To(BeEmpty())
	})

	It("reports a missing source and retries", func() {
		result := reconcile()
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(getOrder().Status.Conditions[0].Reason).To(Equal("TrustBundleUnavailable"))
		Expect(target.published).To(BeEmpty())
	})

	It("preserves concurrent status updates while patching trust status", func() {
		interceptor := &statusPatchConflictClient{Client: k8sClient}
		interceptor.beforeFirstPatch = func(ctx context.Context) error {
			concurrent := &v1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(order), concurrent); err != nil {
				return err
			}
			concurrent.SetStatusCondition("Progressing", metav1.ConditionTrue, "updated by another controller", "ConcurrentUpdate")
			return k8sClient.Status().Update(ctx, concurrent)
		}
		reconciler.Client = interceptor

		update := order.DeepCopy()
		update.Status.FulfillmentTrustBundleHash = "expected-hash"
		update.SetStatusCondition(string(v1alpha1.ClusterOrderConditionFulfillmentTrustReady), metav1.ConditionTrue,
			"Trust bundle and CSI rollout verified", "TrustBundleSynchronized")
		Expect(reconciler.patchStatus(ctx, update)).To(Succeed())

		stored := getOrder()
		Expect(stored.IsStatusConditionTrue("Progressing")).To(BeTrue())
		Expect(stored.IsStatusConditionTrue(string(v1alpha1.ClusterOrderConditionFulfillmentTrustReady))).To(BeTrue())
		Expect(stored.Status.FulfillmentTrustBundleHash).To(Equal("expected-hash"))
		Expect(interceptor.patchCalls).To(Equal(2))
	})

	It("reports unavailable short-lived target access without publishing", func() {
		addBundle()
		reconciler.Targets = fakeTrustResolver{err: errors.New("target unavailable")}
		reconcile()
		Expect(getOrder().Status.Conditions[0].Reason).To(Equal("KubeconfigNotAvailable"))
		Expect(target.published).To(BeEmpty())
	})

	It("publishes, applies and verifies trust without a per-order opt-in annotation", func() {
		bundle := addBundle()
		target.autoConverge = false
		reconcile()
		stored := getOrder()
		Expect(stored.IsStatusConditionTrue(string(v1alpha1.ClusterOrderConditionFulfillmentTrustReady))).To(BeFalse())
		Expect(stored.Status.FulfillmentTrustBundleHash).To(BeEmpty())
		Expect(target.published).To(HaveLen(1))
		Expect(target.published[0].BundlePEM).To(Equal(bundle))
		Expect(target.applied).To(HaveLen(1))
		Expect(strings.Join(target.events, ",")).To(Equal("publish,apply"))

		target.currentHashes[target.applied[0]] = true
		reconcile()
		stored = getOrder()
		Expect(stored.IsStatusConditionTrue(string(v1alpha1.ClusterOrderConditionFulfillmentTrustReady))).To(BeTrue())
		Expect(stored.Status.FulfillmentTrustBundleHash).To(Equal(target.applied[0]))
	})

	It("repairs same-hash drift and applies a rotated CA before revoking the old record", func() {
		addBundle()
		reconcile()
		oldHash := getOrder().Status.FulfillmentTrustBundleHash
		Expect(oldHash).NotTo(BeEmpty())

		target.currentHashes[oldHash] = false
		reconcile()
		Expect(target.applied).To(HaveLen(2))
		Expect(getOrder().Status.FulfillmentTrustBundleHash).To(Equal(oldHash))

		configMap := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: reconciler.SourceName}, configMap)).To(Succeed())
		newBundle := testTrustPEM()
		configMap.Data[trustadmission.BundleDataKey] = string(newBundle)
		Expect(k8sClient.Update(ctx, configMap)).To(Succeed())
		target.events = nil
		reconcile()
		Expect(target.applied).To(HaveLen(3))
		Expect(target.revoked).To(HaveLen(1))
		Expect(target.revoked[0].BundleSHA256).To(Equal(oldHash))
		Expect(strings.Join(target.events, ",")).To(Equal("publish,apply,revoke"))
		Expect(getOrder().Status.FulfillmentTrustBundleHash).NotTo(Equal(oldHash))
	})

	It("revokes the last authorization when the management bundle becomes invalid", func() {
		addBundle()
		reconcile()
		oldHash := getOrder().Status.FulfillmentTrustBundleHash
		configMap := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: reconciler.SourceName}, configMap)).To(Succeed())
		configMap.Data[trustadmission.BundleDataKey] = "invalid PEM"
		Expect(k8sClient.Update(ctx, configMap)).To(Succeed())

		reconcile()
		Expect(getOrder().Status.Conditions[0].Reason).To(Equal("TrustBundleUnavailable"))
		Expect(target.revoked).To(HaveLen(1))
		Expect(target.revoked[0].BundleSHA256).To(Equal(oldHash))
	})

	It("does not enable an unsupported CSI controller", func() {
		addBundle()
		target.unsupported = true
		reconcile()
		Expect(getOrder().Status.Conditions[0].Reason).To(Equal("CSIClientUpgradeRequired"))
		Expect(target.applied).To(BeEmpty())
		Expect(target.published).To(BeEmpty())
	})

	It("keeps trust false until a CSI trust client is installed", func() {
		addBundle()
		target.applyErr = errTrustClientUnavailable
		reconcile()
		Expect(getOrder().Status.Conditions[0].Reason).To(Equal("CSIClientUnavailable"))
	})

	It("fences the active authorization when a ClusterOrder is deleted", func() {
		addBundle()
		reconcile()
		oldHash := getOrder().Status.FulfillmentTrustBundleHash
		stored := getOrder()
		stored.Finalizers = []string{"test.osac.openshift.io/finalizer"}
		Expect(k8sClient.Update(ctx, stored)).To(Succeed())
		Expect(k8sClient.Delete(ctx, stored)).To(Succeed())
		reconcile()
		Expect(target.revoked).To(HaveLen(1))
		Expect(target.revoked[0].BundleSHA256).To(Equal(oldHash))
	})
})

var _ = Describe("KubernetesTrustTarget", func() {
	It("applies only the expected ConfigMap and CSI pod-template hash", func() {
		ctx := context.Background()
		bundle := testTrustPEM()
		record := testExpectedBundle(bundle)
		configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: trustadmission.ConfigMapName, Namespace: "osac-csi",
			Labels: map[string]string{"keep": "label"}, Annotations: map[string]string{"old": "annotation"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "owner", UID: "owner-uid"}},
			Finalizers:      []string{"example.com/finalizer"}},
			Data: map[string]string{"old": "data"}, BinaryData: map[string][]byte{"old": []byte("data")}}
		deployment := testTrustDeployment()
		deployment.Labels[trustadmission.TrustClientLabel] = labelValueTrue
		writer := fake.NewClientBuilder().WithScheme(k8sClient.Scheme()).WithObjects(configMap, deployment).Build()
		target := &KubernetesTrustTarget{Writer: writer, Namespace: "osac-csi"}
		Expect(target.Apply(ctx, record)).To(Succeed())

		storedConfigMap := &corev1.ConfigMap{}
		Expect(writer.Get(ctx, client.ObjectKeyFromObject(configMap), storedConfigMap)).To(Succeed())
		Expect(storedConfigMap.Data).To(Equal(map[string]string{trustadmission.BundleDataKey: string(bundle)}))
		Expect(storedConfigMap.BinaryData).To(BeNil())
		Expect(storedConfigMap.OwnerReferences).To(BeEmpty())
		Expect(storedConfigMap.Finalizers).To(BeEmpty())
		Expect(storedConfigMap.Labels).To(Equal(map[string]string{"keep": "label"}))
		Expect(storedConfigMap.Annotations).To(Equal(trustAnnotations(record)))

		storedDeployment := &appsv1.Deployment{}
		Expect(writer.Get(ctx, client.ObjectKeyFromObject(deployment), storedDeployment)).To(Succeed())
		Expect(storedDeployment.Spec.Template.Annotations[trustadmission.BundleHashAnnotation]).To(Equal(record.Key.BundleSHA256))
		Expect(storedDeployment.Spec.Template.Spec.Containers).To(Equal(deployment.Spec.Template.Spec.Containers))
	})

	It("requires a trust-enabled CSI deployment and a fully ready rollout", func() {
		ctx := context.Background()
		record := testExpectedBundle(testTrustPEM())
		configMap := trustConfigMap(record, "osac-csi")
		deployment := testTrustDeployment()
		deployment.Labels[trustadmission.TrustClientLabel] = labelValueTrue
		deployment.Spec.Template.Annotations[trustadmission.BundleHashAnnotation] = record.Key.BundleSHA256
		deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: deployment.Generation, UpdatedReplicas: 1,
			ReadyReplicas: 1, AvailableReplicas: 1}
		observer := fake.NewClientBuilder().WithScheme(k8sClient.Scheme()).WithObjects(configMap, deployment).Build()
		target := &KubernetesTrustTarget{Observer: observer, Namespace: "osac-csi"}
		current, unsupported, err := target.Observe(ctx, record)
		Expect(err).NotTo(HaveOccurred())
		Expect(unsupported).To(BeFalse())
		Expect(current).To(BeTrue())

		deployment.Labels[trustadmission.TrustClientLabel] = "false"
		Expect(observer.Update(ctx, deployment)).To(Succeed())
		_, unsupported, err = target.Observe(ctx, record)
		Expect(err).NotTo(HaveOccurred())
		Expect(unsupported).To(BeTrue())
	})

	It("does not report ready when no CSI controller is installed", func() {
		observer := fake.NewClientBuilder().WithScheme(k8sClient.Scheme()).Build()
		target := &KubernetesTrustTarget{Observer: observer, Namespace: "osac-csi"}
		current, unsupported, err := target.Observe(context.Background(), testExpectedBundle(testTrustPEM()))
		Expect(err).NotTo(HaveOccurred())
		Expect(unsupported).To(BeFalse())
		Expect(current).To(BeFalse())

		target.Writer = fake.NewClientBuilder().WithScheme(k8sClient.Scheme()).Build()
		record := testExpectedBundle(testTrustPEM())
		Expect(errors.Is(target.Apply(context.Background(), record), errTrustClientUnavailable)).To(BeTrue())
		created := &corev1.ConfigMap{}
		Expect(target.Writer.Get(context.Background(), client.ObjectKey{Namespace: "osac-csi", Name: trustadmission.ConfigMapName}, created)).To(Succeed())
	})
})

var _ = Describe("HostedClusterTrustTargetResolver", func() {
	It("reads the kubeconfig referenced by the order's HostedControlPlane", func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(hypershiftv1beta1.AddToScheme(scheme)).To(Succeed())
		const kubeconfig = "hosted-cluster-kubeconfig"
		hcp := &hypershiftv1beta1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Name: "tenant-cluster", Namespace: "hosted-tenant-cluster"},
			Status: hypershiftv1beta1.HostedControlPlaneStatus{KubeConfig: &hypershiftv1beta1.KubeconfigSecretRef{
				Name: "tenant-kubeconfig", Key: "value",
			}}}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "tenant-kubeconfig", Namespace: "hosted-tenant-cluster"},
			Data: map[string][]byte{"value": []byte(kubeconfig)}}
		management := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp, secret).Build()
		order := &v1alpha1.ClusterOrder{Status: v1alpha1.ClusterOrderStatus{ClusterReference: &v1alpha1.ClusterOrderClusterReferenceType{
			Namespace: "hosted", HostedClusterName: "tenant-cluster",
		}}}
		data, err := (&HostedClusterKubeconfigResolver{Management: management}).Read(context.Background(), order)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(kubeconfig))
	})

	It("uses distinct short-lived identities and caches them only until renewal is due", func() {
		now := time.Now()
		issuer := &fakeTrustTokenIssuer{now: func() time.Time { return now }}
		resolver := &HostedClusterTrustTargetResolver{
			Kubeconfigs: fakeKubeconfigReader{data: testTrustKubeconfig("management-admin-token")},
			Issuer:      issuer, Namespace: "osac-csi", Now: func() time.Time { return now },
		}
		order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Namespace: "orders", Name: "order-one", UID: types.UID("order-uid"), Annotations: map[string]string{
			trustadmission.TenantAnnotation: "tenant-a",
		}}}
		first, err := resolver.Resolve(context.Background(), order)
		Expect(err).NotTo(HaveOccurred())
		second, err := resolver.Resolve(context.Background(), order)
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(BeIdenticalTo(first))
		Expect(issuer.names).To(ConsistOf(trustObserverServiceAccount, trustadmission.TrustSyncServiceAccount,
			"osac-trust-publisher-order-uid"))
		Expect(issuer.names).To(HaveLen(3))

		now = now.Add(10 * time.Minute)
		_, err = resolver.Resolve(context.Background(), order)
		Expect(err).NotTo(HaveOccurred())
		Expect(issuer.names).To(HaveLen(6))
		resolver.ForgetByOrder(client.ObjectKeyFromObject(order))
		Expect(resolver.cache).NotTo(HaveKey("orders/order-one"))
	})

	It("requires verified TLS and removes admin credentials from scoped clients", func() {
		data := testTrustKubeconfig("management-admin-token")
		config, err := verifiedHostedClusterConfig(data)
		Expect(err).NotTo(HaveOccurred())
		scoped, err := trustServiceAccountConfig(config, "short-lived-token")
		Expect(err).NotTo(HaveOccurred())
		Expect(scoped.BearerToken).To(Equal("short-lived-token"))
		Expect(scoped.BearerToken).NotTo(Equal(config.BearerToken))
		Expect(scoped.CAData).To(Equal(config.CAData))
		Expect(scoped.Insecure).To(BeFalse())
		Expect(scoped.Timeout).To(Equal(trustKubeAPITimeout))

		insecure := clientcmdapi.NewConfig()
		insecure.Clusters["tenant"] = &clientcmdapi.Cluster{Server: "https://tenant.example.invalid",
			InsecureSkipTLSVerify: true, CertificateAuthorityData: testTrustPEM()}
		insecure.AuthInfos["management"] = &clientcmdapi.AuthInfo{Token: "management-admin-token"}
		insecure.Contexts["tenant"] = &clientcmdapi.Context{Cluster: "tenant", AuthInfo: "management"}
		insecure.CurrentContext = "tenant"
		_, err = verifiedHostedClusterConfig(mustWriteKubeconfig(insecure))
		Expect(err).To(HaveOccurred())
	})

	It("checks tenant and owner annotations before requesting a bounded token", func() {
		now := time.Now()
		account := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: trustObserverServiceAccount, Namespace: "osac-csi",
			UID: types.UID("observer-uid"), Annotations: map[string]string{
				trustadmission.TenantAnnotation: "tenant-a", trustadmission.OwnerReferenceAnnotation: "order-uid",
			}}}
		kube := k8sfake.NewSimpleClientset(account)
		called := false
		kube.PrependReactor("create", "serviceaccounts", func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "token" {
				return false, nil, nil
			}
			called = true
			request := action.(clienttesting.CreateAction).GetObject().(*authenticationv1.TokenRequest)
			Expect(request.Spec.ExpirationSeconds).NotTo(BeNil())
			Expect(*request.Spec.ExpirationSeconds).To(Equal(int64(600)))
			return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{
				Token: "ephemeral-token", ExpirationTimestamp: metav1.NewTime(now.Add(trustTokenLifetime)),
			}}, nil
		})
		issuer := &KubernetesTrustTokenIssuer{Now: func() time.Time { return now }}
		issued, err := issuer.issueWithClient(context.Background(), kube, "osac-csi", trustObserverServiceAccount, "tenant-a", "order-uid")
		Expect(err).NotTo(HaveOccurred())
		Expect(called).To(BeTrue())
		Expect(issued.Token).To(Equal("ephemeral-token"))
		Expect(issued.ExpiresAt).To(Equal(now.Add(trustTokenLifetime)))

		called = false
		_, err = issuer.issueWithClient(context.Background(), kube, "osac-csi", trustObserverServiceAccount, "tenant-b", "order-uid")
		Expect(err).To(HaveOccurred())
		Expect(called).To(BeFalse())

		rotated := account.DeepCopy()
		tracingKube := k8sfake.NewSimpleClientset(rotated)
		tracingKube.PrependReactor("create", "serviceaccounts", func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "token" {
				return false, nil, nil
			}
			object, getErr := tracingKube.Tracker().Get(corev1.SchemeGroupVersion.WithResource("serviceaccounts"), "osac-csi", trustObserverServiceAccount)
			Expect(getErr).NotTo(HaveOccurred())
			updated := object.(*corev1.ServiceAccount).DeepCopy()
			updated.Annotations[trustadmission.OwnerReferenceAnnotation] = "replacement-order"
			Expect(tracingKube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("serviceaccounts"), updated, "osac-csi")).To(Succeed())
			return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{
				Token: "stale-identity-token", ExpirationTimestamp: metav1.NewTime(now.Add(trustTokenLifetime)),
			}}, nil
		})
		_, err = issuer.issueWithClient(context.Background(), tracingKube, "osac-csi", trustObserverServiceAccount, "tenant-a", "order-uid")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("changed during token request"))
	})
})

func testTrustDeployment() *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "csi-controller", Namespace: "osac-csi", UID: types.UID("deployment-uid"),
		Generation: 1, Labels: map[string]string{trustCSINameLabel: "csiDriver", trustCSIComponentLabel: "controller"}},
		Spec: appsv1.DeploymentSpec{Replicas: &one, Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "csi-controller", Image: "example.invalid/csi:latest"}}},
		}},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}}
}

func testExpectedBundle(bundle []byte) trustadmission.ExpectedBundle {
	return trustadmission.ExpectedBundle{Key: trustadmission.RecordKey{ClusterOrderUID: "order-uid", TenantNamespace: "osac-csi",
		ConfigMapName: trustadmission.ConfigMapName, BundleSHA256: "bundle-hash"}, Tenant: "tenant-a", OwnerReference: "order-uid", BundlePEM: bundle}
}

func testTrustKubeconfig(token string) []byte {
	config := clientcmdapi.NewConfig()
	config.Clusters["tenant"] = &clientcmdapi.Cluster{Server: "https://tenant.example.invalid", CertificateAuthorityData: testTrustPEM()}
	config.AuthInfos["management"] = &clientcmdapi.AuthInfo{Token: token}
	config.Contexts["tenant"] = &clientcmdapi.Context{Cluster: "tenant", AuthInfo: "management"}
	config.CurrentContext = "tenant"
	data, err := clientcmd.Write(*config)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return data
}

func mustWriteKubeconfig(config *clientcmdapi.Config) []byte {
	data, err := clientcmd.Write(*config)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return data
}

func testTrustPEM() []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test trust CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
