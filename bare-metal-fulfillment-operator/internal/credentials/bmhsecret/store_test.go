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
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return NewStore(k8sClient, testNamespace), k8sClient
}

var _ = Describe("BMC Secret Store", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Ensure", func() {
		It("creates an operator-managed BMO Secret", func() {
			store, k8sClient := newStore()

			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"),
				Password: []byte("s3cret"),
			})).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(secret.Type).To(Equal(corev1.SecretTypeOpaque))
			Expect(string(secret.Data["username"])).To(Equal("admin"))
			Expect(string(secret.Data["password"])).To(Equal("s3cret"))
			Expect(secret.Labels).To(HaveKeyWithValue(managedByLabel, shared.OsacDefaultManagedByValue))
		})

		It("updates an existing managed Secret", func() {
			store, k8sClient := newStore()
			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("old"),
			})).To(Succeed())

			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("new"),
			})).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(string(secret.Data["password"])).To(Equal("new"))
		})

		It("refuses to overwrite an unmanaged Secret", func() {
			foreign := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bmc-secret", Namespace: testNamespace},
				Data:       map[string][]byte{"username": []byte("someone"), "password": []byte("else")},
			}
			store, k8sClient := newStore(foreign)

			err := store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("s3cret"),
			})

			Expect(err).To(MatchError(ContainSubstring("not operator-managed")))
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(string(secret.Data["password"])).To(Equal("else"))
			Expect(secret.Labels).NotTo(HaveKey(managedByLabel))
		})

		It("rejects nil credentials", func() {
			store, _ := newStore()
			Expect(store.Ensure(ctx, "node001-bmc-secret", nil)).To(MatchError("BMC credentials are required"))
		})
	})

	Describe("Delete", func() {
		It("deletes an operator-managed Secret", func() {
			store, k8sClient := newStore()
			Expect(store.Ensure(ctx, "node001-bmc-secret", &credentials.BMC{
				Username: []byte("admin"), Password: []byte("s3cret"),
			})).To(Succeed())

			Expect(store.Delete(ctx, "node001-bmc-secret")).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(HaveOccurred())
		})

		It("leaves an unmanaged Secret in place", func() {
			adminSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-bmc-secret", Namespace: testNamespace},
				Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("s3cret")},
			}
			store, k8sClient := newStore(adminSecret)

			Expect(store.Delete(ctx, "admin-bmc-secret")).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "admin-bmc-secret"}, secret)).To(Succeed())
		})

		It("is idempotent when the Secret does not exist", func() {
			store, _ := newStore()
			Expect(store.Delete(ctx, "nonexistent")).To(Succeed())
		})
	})
})
