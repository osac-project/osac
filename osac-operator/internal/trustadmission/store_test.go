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

package trustadmission_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/internal/trustadmission"
)

const (
	tenantNamespace = "osac-csi"
	tenantName      = "tenant-a"
	clusterOrderUID = "cluster-order-uid"
)

var _ = Describe("MemoryStore", func() {
	var (
		now    time.Time
		bundle []byte
		store  trustadmission.Store
		record trustadmission.ExpectedBundle
	)

	BeforeEach(func() {
		now = time.Date(2026, time.September, 27, 12, 0, 0, 0, time.UTC)
		bundle = certificatePEM()
		store = trustadmission.NewStore()
		record = expectedBundle(bundle, now.Add(time.Hour))
	})

	It("authorizes the exact active ConfigMap bundle", func() {
		Expect(store.Publish(context.Background(), record)).To(Succeed())

		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).To(Succeed())
	})

	It("allows harmless labels but rejects controller ownership metadata", func() {
		Expect(store.Publish(context.Background(), record)).To(Succeed())
		candidate := configMapFor(record)
		candidate.Labels = map[string]string{"app.kubernetes.io/managed-by": "helm"}
		Expect(store.AuthorizeConfigMap(context.Background(), candidate, now)).To(Succeed())

		candidate.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "owner", UID: "owner-uid"}}
		Expect(store.AuthorizeConfigMap(context.Background(), candidate, now)).NotTo(Succeed())
		candidate.OwnerReferences = nil
		candidate.Finalizers = []string{"example.com/finalizer"}
		Expect(store.AuthorizeConfigMap(context.Background(), candidate, now)).NotTo(Succeed())
	})

	It("rejects a record whose payload is not a certificate bundle", func() {
		record.BundlePEM = []byte("apiVersion: v1\nkind: Config\n")
		record.Key.BundleSHA256 = bundleHash(record.BundlePEM)

		Expect(store.Publish(context.Background(), record)).NotTo(Succeed())
	})

	DescribeTable("denies ConfigMaps that differ from the expected record",
		func(mutate func(*corev1.ConfigMap)) {
			Expect(store.Publish(context.Background(), record)).To(Succeed())
			candidate := configMapFor(record)
			mutate(candidate)

			Expect(store.AuthorizeConfigMap(context.Background(), candidate, now)).NotTo(Succeed())
		},
		Entry("with altered bundle bytes", func(candidate *corev1.ConfigMap) {
			candidate.Data[trustadmission.BundleDataKey] = "different bundle"
		}),
		Entry("with an extra data key", func(candidate *corev1.ConfigMap) {
			candidate.Data["extra"] = "value"
		}),
		Entry("with an extra annotation", func(candidate *corev1.ConfigMap) {
			candidate.Annotations["example.com/extra"] = "value"
		}),
		Entry("with a different tenant", func(candidate *corev1.ConfigMap) {
			candidate.Annotations[trustadmission.TenantAnnotation] = "tenant-b"
		}),
		Entry("with a different owner reference", func(candidate *corev1.ConfigMap) {
			candidate.Annotations[trustadmission.OwnerReferenceAnnotation] = "other-cluster-order"
		}),
	)

	It("denies expired and revoked records", func() {
		record.ExpiresAt = now.Add(-time.Second)
		Expect(store.Publish(context.Background(), record)).To(Succeed())
		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).NotTo(Succeed())

		record.ExpiresAt = now.Add(time.Hour)
		Expect(store.Publish(context.Background(), record)).To(Succeed())
		Expect(store.Revoke(context.Background(), record.Key)).To(Succeed())
		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).NotTo(Succeed())
	})

	It("authorizes a Deployment hash only for one active tenant record", func() {
		Expect(store.Publish(context.Background(), record)).To(Succeed())
		Expect(store.AuthorizeDeployment(context.Background(), record.Key.TenantNamespace, record.Key.BundleSHA256, now)).To(Succeed())

		second := record
		second.Key.ClusterOrderUID = "another-cluster-order"
		second.OwnerReference = second.Key.ClusterOrderUID
		Expect(store.Publish(context.Background(), second)).To(Succeed())
		Expect(store.AuthorizeDeployment(context.Background(), record.Key.TenantNamespace, record.Key.BundleSHA256, now)).NotTo(Succeed())
	})

	It("denies authorization after revocation completes", func() {
		Expect(store.Publish(context.Background(), record)).To(Succeed())
		Expect(store.Revoke(context.Background(), record.Key)).To(Succeed())

		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).NotTo(Succeed())
		Expect(store.AuthorizeDeployment(context.Background(), record.Key.TenantNamespace, record.Key.BundleSHA256, now)).NotTo(Succeed())
	})
})

func expectedBundle(bundle []byte, expiresAt time.Time) trustadmission.ExpectedBundle {
	return trustadmission.ExpectedBundle{
		Key: trustadmission.RecordKey{
			ClusterOrderUID: clusterOrderUID,
			TenantNamespace: tenantNamespace,
			ConfigMapName:   trustadmission.ConfigMapName,
			BundleSHA256:    bundleHash(bundle),
		},
		Tenant:         tenantName,
		OwnerReference: clusterOrderUID,
		BundlePEM:      bundle,
		ExpiresAt:      expiresAt,
	}
}

func configMapFor(record trustadmission.ExpectedBundle) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      record.Key.ConfigMapName,
			Namespace: record.Key.TenantNamespace,
			Annotations: map[string]string{
				trustadmission.TenantAnnotation:         record.Tenant,
				trustadmission.OwnerReferenceAnnotation: record.OwnerReference,
				trustadmission.BundleHashAnnotation:     record.Key.BundleSHA256,
			},
		},
		Data: map[string]string{trustadmission.BundleDataKey: string(record.BundlePEM)},
	}
}

func bundleHash(bundle []byte) string {
	digest := sha256.Sum256(bundle)
	return hex.EncodeToString(digest[:])
}

func certificatePEM() []byte {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())

	certificateDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}, &privateKey.PublicKey, privateKey)
	Expect(err).NotTo(HaveOccurred())

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
}
