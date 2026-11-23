// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// controlPlaneSecrets supplies native HC inputs independently of the control-plane consumers.
func controlPlaneSecrets(hc *hypershiftv1.HostedCluster) []*corev1.Secret {
	hc.Spec.SecretEncryption = &hypershiftv1.SecretEncryptionSpec{
		Type: hypershiftv1.AESCBC,
		AESCBC: &hypershiftv1.AESCBCSpec{
			ActiveKey: corev1.LocalObjectReference{Name: "active"},
			BackupKey: &corev1.LocalObjectReference{Name: "backup"},
		},
	}
	hc.Spec.AuditWebhook = &corev1.LocalObjectReference{Name: "audit"}
	hc.Spec.Etcd = hypershiftv1.EtcdSpec{
		ManagementType: hypershiftv1.Unmanaged,
		Unmanaged: &hypershiftv1.UnmanagedEtcdSpec{
			Endpoint: "https://etcd.invalid:2379",
			TLS:      hypershiftv1.EtcdTLSConfig{ClientSecret: corev1.LocalObjectReference{Name: "etcd"}},
		},
	}

	return []*corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "active"},
			Data:       map[string][]byte{"key": []byte("active-key"), "uncopied": []byte("source-extra")},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "backup"},
			Data:       map[string][]byte{"key": []byte("backup-key")},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "audit"},
			Data:       map[string][]byte{hypershiftv1.AuditWebhookKubeconfigKey: []byte("kubeconfig")},
		},
		{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "etcd"}, Data: map[string][]byte{
			"etcd-client.crt": []byte("certificate"), "etcd-client.key": []byte("private-key"),
			"etcd-client-ca.crt": []byte("ca"), "extra": []byte("native-full-data-copy"),
		}},
	}
}

var _ = Describe("Control-plane Secret inputs", func() {
	It(
		"waits for incomplete encryption without publishing a control plane or returning an API error",
		func(ctx SpecContext) {
			hc := agentHostedCluster()
			hc.Spec.SecretEncryption = &hypershiftv1.SecretEncryptionSpec{Type: hypershiftv1.AESCBC}
			pull := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name},
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"registry.invalid":{}}}`)},
			}
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc, pull).WithStatusSubresource(hc).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeZero())

			var controlPlanes hypershiftv1.HostedControlPlaneList

			Expect(c.List(ctx, &controlPlanes)).To(Succeed())
			Expect(controlPlanes.Items).To(BeEmpty())
		},
	)

	It("copies native input data and propagates updates without replacing identities", func(ctx SpecContext) {
		hc := agentHostedCluster()

		sources := controlPlaneSecrets(hc)
		copyFailure := errors.New("pull Secret copy rejected")
		rejectCopy := false

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(hc).WithStatusSubresource(hc).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption) error {
					if _, secret := object.(*corev1.Secret); secret && rejectCopy && object.GetName() == "pull-secret" {
						return copyFailure
					}

					return c.Create(ctx, object, opts...)
				},
			}).Build()
		for _, source := range sources {
			Expect(c.Create(ctx, source)).To(Succeed())
		}

		r := &HostedClusterReconciler{Client: c, APIReader: c}
		ready, err := r.ensureControlPlaneSecrets(ctx, hc, "cp")
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		Expect(r.observeUnmanagedEtcd(ctx, hc)).To(Succeed())
		Expect(meta.IsStatusConditionTrue(hc.Status.Conditions, string(hypershiftv1.UnmanagedEtcdAvailable))).To(BeTrue())

		for _, source := range sources {
			copy := &corev1.Secret{}
			Expect(c.Get(ctx, client.ObjectKey{Namespace: "cp", Name: source.Name}, copy)).To(Succeed())
			Expect(copy.Annotations).To(HaveKeyWithValue(kube.HostedClusterAnnotation, hc.Namespace+"/"+hc.Name))
			Expect(copy.Type).To(Equal(corev1.SecretTypeOpaque))

			for key, value := range source.Data {
				if key != "uncopied" {
					Expect(copy.Data).To(HaveKeyWithValue(key, value))
				}
			}

			Expect(copy.Data).NotTo(HaveKey("uncopied"))
		}

		active := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: "cp", Name: "active"}, active)).To(Succeed())
		active.UID, active.Data["retained"] = "original-copy", []byte("destination-extra")
		Expect(c.Update(ctx, active)).To(Succeed())

		sources[0].Data["key"] = []byte("rotated-key")
		Expect(c.Update(ctx, sources[0])).To(Succeed())
		ready, err = r.ensureControlPlaneSecrets(ctx, hc, "cp")
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(active), active)).To(Succeed())
		Expect(active.UID).To(BeEquivalentTo("original-copy"))
		Expect(active.Data).To(HaveKeyWithValue("key", []byte("rotated-key")))
		Expect(active.Data).To(HaveKeyWithValue("retained", []byte("destination-extra")))
		Expect(c.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"registry.invalid":{}}}`)},
		})).To(Succeed())

		By("keeping the HCP unpublished until its pull Secret is written")

		rejectCopy = true
		_, err = r.inputs(ctx, hc)
		Expect(err).To(MatchError(copyFailure))

		key := client.ObjectKey{Namespace: hc.Namespace + "-" + hc.Name, Name: hc.Name}
		Expect(apierrors.IsNotFound(c.Get(ctx, key, &hypershiftv1.HostedControlPlane{}))).To(BeTrue())

		rejectCopy = false
		cp, err := r.inputs(ctx, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(cp.Spec.PullSecret.Name).To(Equal("pull-secret"))

		copy := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: cp.Spec.PullSecret.Name}, copy)).To(Succeed())
		Expect(copy.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
		Expect(copy.Annotations).To(HaveKeyWithValue(kube.HostedClusterAnnotation, hc.Namespace+"/"+hc.Name))
		Expect(copy.OwnerReferences).To(BeEmpty())
		Expect(copy.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, []byte(`{"auths":{"registry.invalid":{}}}`)))
		Expect(cp.Spec.AuditWebhook).To(Equal(hc.Spec.AuditWebhook))
		Expect(cp.Spec.SecretEncryption).To(Equal(hc.Spec.SecretEncryption))
		Expect(cp.Spec.Etcd).To(Equal(hc.Spec.Etcd))

		By("keeping both children paused until the pull Secret is valid again")

		hc.Spec.PausedUntil = new("true")
		Expect(r.pause(ctx, hc)).To(Succeed())
		hc.Spec.PausedUntil = nil
		pull := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, pull)).To(Succeed())
		data := pull.Data
		pull.Data = nil
		Expect(c.Update(ctx, pull)).To(Succeed())
		_, err = r.inputs(ctx, hc)
		Expect(err).To(MatchError(ContainSubstring("must contain .dockerconfigjson")))

		capiCluster := capiClusterOutput(hc)
		Expect(c.Get(ctx, client.ObjectKeyFromObject(capiCluster), capiCluster)).To(Succeed())
		Expect(capiCluster.Spec.Paused).To(BeTrue())
		Expect(c.Get(ctx, key, cp)).To(Succeed())
		Expect(cp.Spec.PausedUntil).To(HaveValue(Equal("true")))

		pull.Data = data
		Expect(c.Update(ctx, pull)).To(Succeed())
		cp, err = r.inputs(ctx, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(cp.Spec.PausedUntil).To(BeNil())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(capiCluster), capiCluster)).To(Succeed())
		Expect(capiCluster.Spec.Paused).To(BeFalse())
	})

	DescribeTable("handles encryption metadata without management-side Secret I/O",
		func(ctx SpecContext, encryption *hypershiftv1.SecretEncryptionSpec, wantReady bool) {
			hc := agentHostedCluster()
			hc.Spec.SecretEncryption = encryption
			reads := 0
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object,
					opts ...client.GetOption,
				) error {
					reads++
					return c.Get(ctx, key, object, opts...)
				},
			}).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c}
			ready, err := r.ensureSecretEncryption(ctx, hc, "cp")
			Expect(err).NotTo(HaveOccurred())
			Expect(ready).To(Equal(wantReady))
			Expect(reads).To(BeZero())

			var secrets corev1.SecretList

			Expect(c.List(ctx, &secrets)).To(Succeed())
			Expect(secrets.Items).To(BeEmpty())
		},
		Entry("missing AES-CBC metadata", &hypershiftv1.SecretEncryptionSpec{Type: hypershiftv1.AESCBC}, false),
		Entry("missing KMS metadata", &hypershiftv1.SecretEncryptionSpec{Type: hypershiftv1.KMS}, false),
		Entry("Agent KMS uses no management-side credentials", &hypershiftv1.SecretEncryptionSpec{
			Type: hypershiftv1.KMS,
			KMS: &hypershiftv1.KMSSpec{Provider: hypershiftv1.AWS, AWS: &hypershiftv1.AWSKMSSpec{
				Region:    "us-east-1",
				ActiveKey: hypershiftv1.AWSKMSKeyEntry{ARN: "arn:aws:kms:us-east-1:123456789012:key/synthetic"},
				Auth:      hypershiftv1.AWSKMSAuthSpec{AWSKMSRoleARN: "arn:aws:iam::123456789012:role/synthetic-kms"},
			}},
		}, true),
		Entry("unsupported encryption type", &hypershiftv1.SecretEncryptionSpec{Type: "unsupported"}, false),
	)

	DescribeTable("requires native Secret data fields",
		func(ctx SpecContext, name, key string, wantError bool) {
			hc := agentHostedCluster()
			sources := controlPlaneSecrets(hc)
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).Build()

			for _, source := range sources {
				Expect(c.Create(ctx, source)).To(Succeed())
			}

			source := &corev1.Secret{}
			Expect(c.Get(ctx, client.ObjectKey{Namespace: hc.Namespace, Name: name}, source)).To(Succeed())
			delete(source.Data, key)
			Expect(c.Update(ctx, source)).To(Succeed())
			r := &HostedClusterReconciler{Client: c, APIReader: c}
			ready, err := r.ensureControlPlaneSecrets(ctx, hc, "cp")
			Expect(ready).To(BeFalse())

			if wantError {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("active key", "active", hypershiftv1.AESCBCKeySecretKey, false),
		Entry("backup key", "backup", hypershiftv1.AESCBCKeySecretKey, false),
		Entry("audit kubeconfig", "audit", hypershiftv1.AuditWebhookKubeconfigKey, true),
	)

	It("requires an unmanaged etcd endpoint", func(ctx SpecContext) {
		hc := agentHostedCluster()
		sources := controlPlaneSecrets(hc)
		hc.Spec.Etcd.Unmanaged.Endpoint = ""
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).Build()

		for _, source := range sources {
			Expect(c.Create(ctx, source)).To(Succeed())
		}

		r := &HostedClusterReconciler{Client: c, APIReader: c}
		ready, err := r.ensureControlPlaneSecrets(ctx, hc, "cp")
		Expect(ready).To(BeFalse())
		Expect(err).To(MatchError("unmanaged etcd endpoint and TLS Secret reference are required"))
	})

	It("leaves a foreign destination unchanged", func(ctx SpecContext) {
		hc := agentHostedCluster()
		sources := controlPlaneSecrets(hc)
		foreign := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "active", UID: "foreign",
			Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/other"},
		}, Data: map[string][]byte{"retained": []byte("yes")}}
		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(foreign).Build()

		for _, source := range sources {
			Expect(c.Create(ctx, source)).To(Succeed())
		}

		r := &HostedClusterReconciler{Client: c, APIReader: c}
		ready, err := r.ensureControlPlaneSecrets(ctx, hc, "cp")
		Expect(ready).To(BeFalse())
		Expect(err).To(HaveOccurred())

		before := foreign.DeepCopy()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
		Expect(foreign).To(Equal(before))
	})

	DescribeTable("reports unmanaged etcd key availability", func(ctx SpecContext, missing string) {
		hc := agentHostedCluster()
		sources := controlPlaneSecrets(hc)
		delete(sources[3].Data, missing)
		c := fake.NewClientBuilder().
			WithScheme(hypershiftScheme()).
			WithObjects(hc, sources[3]).
			WithStatusSubresource(hc).
			Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c}
		Expect(r.observeUnmanagedEtcd(ctx, hc)).To(Succeed())
		condition := meta.FindStatusCondition(hc.Status.Conditions, string(hypershiftv1.UnmanagedEtcdAvailable))
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(hypershiftv1.UnmanagedEtcdMisconfiguredReason))
	}, Entry("client certificate", "etcd-client.crt"), Entry("private key", "etcd-client.key"),
		Entry("CA", "etcd-client-ca.crt"))

	DescribeTable("rejects invalid signing inputs",
		func(ctx SpecContext, issuer string, data map[string][]byte, message string) {
			hc := agentHostedCluster()
			hc.Spec.IssuerURL = issuer
			hc.Spec.ServiceAccountSigningKey = &corev1.LocalObjectReference{Name: "signing"}
			source := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "signing"}, Data: data,
			}
			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(source).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c}
			Expect(r.ensureSigningKey(ctx, hc, "cp")).To(MatchError(ContainSubstring(message)))
		},
		Entry("missing issuer", "", map[string][]byte{"key": []byte("invalid")},
			"IssuerURL is required with a service account signing key"),
		Entry("invalid PEM", "https://issuer.invalid", map[string][]byte{"key": []byte("invalid")},
			"signing Secret has no private-key PEM block"),
		Entry("invalid RSA", "https://issuer.invalid", map[string][]byte{
			"key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid")}),
		}, "decode service account private key"),
		Entry("missing data key", "https://issuer.invalid", map[string][]byte{}, "signing Secret must contain key"),
	)

	It("initializes signing keys and preserves native key consistency", func(ctx SpecContext) {
		hc := agentHostedCluster()
		hc.Spec.IssuerURL = "https://issuer.invalid"
		hc.Spec.ServiceAccountSigningKey = &corev1.LocalObjectReference{Name: "signing"}
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())

		private := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		source := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "signing"},
			Data:       map[string][]byte{"key": private},
		}

		c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(source).Build()
		r := &HostedClusterReconciler{Client: c, APIReader: c}

		err = r.ensureSigningKey(ctx, hc, "cp")
		Expect(err).NotTo(HaveOccurred())

		copy := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: "cp", Name: "sa-signing-key"}, copy)).To(Succeed())
		Expect(copy.Data).To(HaveKeyWithValue("service-account.key", private))
		block, _ := pem.Decode(copy.Data["service-account.pub"])
		Expect(block).NotTo(BeNil())
		public, err := x509.ParsePKIXPublicKey(block.Bytes)
		Expect(err).NotTo(HaveOccurred())
		Expect(public.(*rsa.PublicKey).Equal(&key.PublicKey)).To(BeTrue())

		before := copy.DeepCopy()

		Expect(r.ensureSigningKey(ctx, hc, "cp")).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(copy), copy)).To(Succeed())
		Expect(copy).To(Equal(before))

		source.Data["key"] = append(private, '\n')
		Expect(c.Update(ctx, source)).To(Succeed())
		Expect(r.ensureSigningKey(ctx, hc, "cp")).NotTo(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(copy), copy)).To(Succeed())
		Expect(copy).To(Equal(before))
	})
})

var _ = Describe("HostedCluster SSH inputs", func() {
	DescribeTable("copies only native SSH inputs before publishing the HCP reference",
		func(ctx SpecContext, reference string, data map[string][]byte, destinationSource string, wantError bool) {
			hc := agentHostedCluster()
			hc.Spec.SSHKey.Name = reference
			source := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "source-ssh"}, Data: data,
			}
			pull := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name},
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
			}
			destination := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "hub-cluster", Name: "ssh-key", UID: "original-copy",
					Annotations:     map[string]string{"retained": "yes", kube.HostedClusterAnnotation: destinationSource},
					OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Secret", Name: "other", UID: "other-owner"}},
				},
				Data: map[string][]byte{"retained": []byte("other field")},
			}

			objects := []client.Object{hc, source, pull}
			if destinationSource != "" {
				objects = append(objects, destination)
			}

			c := fake.NewClientBuilder().WithScheme(hypershiftScheme()).WithObjects(objects...).Build()
			r := &HostedClusterReconciler{Client: c, APIReader: c}

			key := client.ObjectKeyFromObject(destination)
			if destinationSource != "" {
				Expect(c.Get(ctx, key, destination)).To(Succeed())
			}

			cp, err := r.inputs(ctx, hc)
			if wantError {
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Namespace: "hub-cluster", Name: hc.Name},
					&hypershiftv1.HostedControlPlane{}))).To(BeTrue())

				if destinationSource != "" {
					unchanged := &corev1.Secret{}
					Expect(c.Get(ctx, key, unchanged)).To(Succeed())
					Expect(unchanged).To(Equal(destination))
				}

				return
			}

			Expect(err).NotTo(HaveOccurred())

			copied := &corev1.Secret{}

			if reference == "" {
				Expect(cp.Spec.SSHKey.Name).To(BeEmpty())
				Expect(apierrors.IsNotFound(c.Get(ctx, key, copied))).To(BeTrue())
				return
			}

			Expect(cp.Spec.SSHKey.Name).To(Equal("ssh-key"))
			Expect(c.Get(ctx, key, copied)).To(Succeed())
			Expect(copied.Type).To(Equal(corev1.SecretTypeOpaque))
			Expect(copied.Data).To(HaveKey("id_rsa.pub"))
			Expect(bytes.Equal(copied.Data["id_rsa.pub"], source.Data["id_rsa.pub"])).To(BeTrue())
			Expect(copied.Annotations[kube.HostedClusterAnnotation]).To(Equal("hub/cluster"))

			if destinationSource != "" {
				Expect(copied.UID).To(Equal(destination.UID))
				Expect(copied.OwnerReferences).To(Equal(destination.OwnerReferences))
				Expect(copied.Annotations["retained"]).To(Equal("yes"))
				Expect(copied.Data).To(HaveKey("retained"))
			}

			before := copied.DeepCopy()
			_, err = r.inputs(ctx, hc)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, key, copied)).To(Succeed())
			Expect(copied).To(Equal(before))
		},
		Entry("no SSH reference", "", map[string][]byte{}, "", false),
		Entry("missing source", "missing-ssh", map[string][]byte{}, "", true),
		Entry("missing public-key field", "source-ssh", map[string][]byte{}, "", true),
		Entry("an empty public-key field", "source-ssh", map[string][]byte{"id_rsa.pub": nil}, "", false),
		Entry("an existing destination with foreign fields", "source-ssh",
			map[string][]byte{"id_rsa.pub": []byte("synthetic public key")}, "hub/cluster", false),
		Entry("a destination linked to another HostedCluster", "source-ssh",
			map[string][]byte{"id_rsa.pub": []byte("synthetic public key")}, "hub/other", true),
		Entry("a public-key field", "source-ssh",
			map[string][]byte{"id_rsa.pub": []byte("synthetic public key")}, "", false),
	)
})
