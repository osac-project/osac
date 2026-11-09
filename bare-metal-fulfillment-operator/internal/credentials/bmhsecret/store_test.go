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

package bmhsecret

import (
	"context"
	"testing"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

func TestBMCSecretStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BMC Secret Store Suite")
}

const testNamespace = "osac-baremetal"

func newStore(objects ...client.Object) (*Store, client.Client) {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(metal3api.AddToScheme(scheme)).To(Succeed())
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return NewStore(k8sClient, testNamespace), k8sClient
}

var _ = Describe("BMC Secret Store", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Ensure", func() {
		It("creates an operator-managed Secret owned by the BMI", func() {
			store, k8sClient := newStore()

			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"),
				Password: []byte("s3cret"),
			}, "bmi-A")).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(secret.Type).To(Equal(corev1.SecretTypeOpaque))
			Expect(string(secret.Data["username"])).To(Equal("admin"))
			Expect(string(secret.Data["password"])).To(Equal("s3cret"))
			Expect(secret.Labels).To(HaveKeyWithValue(managedByLabel, shared.OsacDefaultManagedByValue))
			Expect(secret.Labels).To(HaveKeyWithValue(ownerUIDLabel, "bmi-A"))
		})

		It("updates credentials for the same BMI", func() {
			store, k8sClient := newStore()
			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("old"),
			}, "bmi-A")).To(Succeed())

			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("new"),
			}, "bmi-A")).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(string(secret.Data["password"])).To(Equal("new"))
		})

		It("refuses an update by a different BMI", func() {
			store, k8sClient := newStore()
			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("original"),
			}, "bmi-A")).To(Succeed())

			err := store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("replacement"),
			}, "bmi-B")
			Expect(err).To(MatchError(ContainSubstring("not owned by this BMI")))

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(string(secret.Data["password"])).To(Equal("original"))
		})

		It("adopts a legacy Secret only for its BMH consumer", func() {
			bmh := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{Name: "node001", Namespace: testNamespace},
				Spec:       metal3api.BareMetalHostSpec{ConsumerRef: &corev1.ObjectReference{Name: "bmi-A"}},
			}
			legacy := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "node001-bmc-secret", Namespace: testNamespace,
				Labels: map[string]string{managedByLabel: shared.OsacDefaultManagedByValue},
			}, Data: map[string][]byte{"password": []byte("original")}}
			store, k8sClient := newStore(bmh, legacy)

			err := store.Ensure(ctx, legacy.Name, &credentials.BMC{
				Username: []byte("admin"), Password: []byte("foreign"),
			}, "bmi-B")
			Expect(err).To(MatchError(ContainSubstring("not owned by this BMI")))
			Expect(store.Ensure(ctx, legacy.Name, &credentials.BMC{
				Username: []byte("admin"), Password: []byte("replacement"),
			}, "bmi-A")).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(legacy), secret)).To(Succeed())
			Expect(secret.Labels).To(HaveKeyWithValue(ownerUIDLabel, "bmi-A"))
			Expect(string(secret.Data["password"])).To(Equal("replacement"))
		})

		It("refuses to overwrite an unmanaged Secret", func() {
			foreign := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bmc-secret", Namespace: testNamespace},
				Data:       map[string][]byte{"username": []byte("someone"), "password": []byte("else")},
			}
			store, k8sClient := newStore(foreign)

			err := store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("s3cret"),
			}, "bmi-A")

			Expect(err).To(MatchError(ContainSubstring("not operator-managed")))
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(string(secret.Data["password"])).To(Equal("else"))
			Expect(secret.Labels).NotTo(HaveKey(managedByLabel))
		})

		It("rejects nil credentials and a missing owner", func() {
			store, _ := newStore()
			Expect(store.Ensure(ctx, "node001-bmc-secret", nil, "bmi-A")).To(MatchError("BMC credentials are required"))
			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{}, "")).To(MatchError(ContainSubstring("requires a BMI UID")))
		})
	})

	Describe("Delete", func() {
		It("deletes a Secret only for its BMI owner", func() {
			store, k8sClient := newStore()
			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("s3cret"),
			}, "bmi-A")).To(Succeed())

			Expect(store.Delete(ctx, "node001-bmc-secret", "bmi-B")).To(MatchError(ContainSubstring("not owned by this BMI")))
			secret := &corev1.Secret{}
			key := client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}
			Expect(k8sClient.Get(ctx, key, secret)).To(Succeed())
			Expect(store.Delete(ctx, "node001-bmc-secret", "bmi-A")).To(Succeed())
			Expect(k8sClient.Get(ctx, key, &corev1.Secret{})).To(HaveOccurred())
		})

		It("refuses to delete an unmanaged Secret", func() {
			adminSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-bmc-secret", Namespace: testNamespace},
				Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("s3cret")},
			}
			store, k8sClient := newStore(adminSecret)

			Expect(store.Delete(ctx, "admin-bmc-secret", "bmi-A")).To(MatchError(ContainSubstring("not operator-managed")))
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(adminSecret), &corev1.Secret{})).To(Succeed())
		})

		It("deletes a legacy managed Secret after its BMH is gone", func() {
			legacy := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "node001-bmc-secret", Namespace: testNamespace,
				Labels: map[string]string{managedByLabel: shared.OsacDefaultManagedByValue},
			}}
			store, k8sClient := newStore(legacy)

			Expect(store.Delete(ctx, legacy.Name, "bmi-A")).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(legacy), &corev1.Secret{})).To(HaveOccurred())
		})

		It("is idempotent when the Secret does not exist", func() {
			store, _ := newStore()
			Expect(store.Delete(ctx, "nonexistent", "bmi-A")).To(Succeed())
		})
	})
})
