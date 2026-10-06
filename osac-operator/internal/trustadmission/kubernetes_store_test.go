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
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/osac-project/osac/osac-operator/internal/trustadmission"
)

var _ = Describe("KubernetesStore", func() {
	var (
		now       time.Time
		store     trustadmission.Store
		record    trustadmission.ExpectedBundle
		clientset *fake.Clientset
	)

	BeforeEach(func() {
		now = time.Now().UTC()
		clientset = fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name:      trustadmission.ExpectedBundleStoreName,
			Namespace: tenantNamespace,
		}})
		var err error
		store, err = trustadmission.NewKubernetesStore(clientset.CoreV1(), tenantNamespace)
		Expect(err).NotTo(HaveOccurred())
		record = expectedBundle(certificatePEM(), now.Add(time.Hour))
	})

	It("persists an active record for a restarted admission service", func() {
		Expect(store.Publish(context.Background(), record)).To(Succeed())

		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).To(Succeed())
	})

	It("removes persisted records atomically when revoked", func() {
		Expect(store.Publish(context.Background(), record)).To(Succeed())
		Expect(store.Revoke(context.Background(), record.Key)).To(Succeed())

		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).NotTo(Succeed())
	})

	It("fails closed when the protected store is absent", func() {
		clientset := fake.NewSimpleClientset()
		missingStore, err := trustadmission.NewKubernetesStore(clientset.CoreV1(), tenantNamespace)
		Expect(err).NotTo(HaveOccurred())

		Expect(missingStore.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).NotTo(Succeed())
		Expect(missingStore.Ready(context.Background())).NotTo(Succeed())
	})

	It("is not ready until the protected store has initialized records", func() {
		Expect(store.(*trustadmission.KubernetesStore).Ready(context.Background())).NotTo(Succeed())
		Expect(store.Publish(context.Background(), record)).To(Succeed())
		Expect(store.(*trustadmission.KubernetesStore).Ready(context.Background())).To(Succeed())
	})

	It("ignores an invalid persisted record when authorizing a valid record", func() {
		Expect(store.Publish(context.Background(), record)).To(Succeed())
		secret, err := clientset.CoreV1().Secrets(tenantNamespace).Get(
			context.Background(), trustadmission.ExpectedBundleStoreName, metav1.GetOptions{},
		)
		Expect(err).NotTo(HaveOccurred())
		invalidRecord := map[string]any{
			"key":            map[string]any{},
			"tenant":         "tenant-b",
			"ownerReference": "invalid-order",
			"bundlePEM":      []byte("not a certificate"),
			"expiresAt":      now.Add(time.Hour),
		}
		encoded, err := json.Marshal([]any{
			map[string]any{
				"key": map[string]string{
					"ClusterOrderUID": record.Key.ClusterOrderUID,
					"TenantNamespace": record.Key.TenantNamespace,
					"ConfigMapName":   record.Key.ConfigMapName,
					"BundleSHA256":    record.Key.BundleSHA256,
				},
				"tenant": record.Tenant, "ownerReference": record.OwnerReference,
				"bundlePEM": record.BundlePEM, "expiresAt": record.ExpiresAt,
			},
			invalidRecord,
		})
		Expect(err).NotTo(HaveOccurred())
		secret.Data = map[string][]byte{"records.json": encoded}
		_, err = clientset.CoreV1().Secrets(tenantNamespace).Update(context.Background(), secret, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).To(Succeed())
	})

	It("prunes expired records when publishing a replacement record", func() {
		expired := record
		expired.Key.ClusterOrderUID = "expired-order"
		expired.OwnerReference = expired.Key.ClusterOrderUID
		expired.ExpiresAt = now.Add(-time.Minute)
		Expect(store.Publish(context.Background(), expired)).To(Succeed())
		Expect(store.Publish(context.Background(), record)).To(Succeed())

		secret, err := clientset.CoreV1().Secrets(tenantNamespace).Get(
			context.Background(), trustadmission.ExpectedBundleStoreName, metav1.GetOptions{},
		)
		Expect(err).NotTo(HaveOccurred())
		var persisted []map[string]json.RawMessage
		Expect(json.Unmarshal(secret.Data["records.json"], &persisted)).To(Succeed())
		Expect(persisted).To(HaveLen(1))
		var persistedKey map[string]string
		Expect(json.Unmarshal(persisted[0]["key"], &persistedKey)).To(Succeed())
		Expect(persistedKey["ClusterOrderUID"]).To(Equal(record.Key.ClusterOrderUID))
	})

})
