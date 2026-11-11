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

package bmh

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
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/directclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

// Synthetic test values only; this suite never uses real BMC credentials.
const (
	testBMCUsername            = "fake-user"
	testBMCPassword            = "fake-value"
	testOldBMCPassword         = "fake-old-value"
	testNewBMCPassword         = "fake-new-value"
	testOriginalBMCPassword    = "fake-original-value"
	testReplacementBMCPassword = "fake-replacement-value"
	testForeignBMCPassword     = "fake-foreign-value"
	testUnmanagedBMCUsername   = "fake-other-user"
	testUnmanagedBMCPassword   = "fake-other-value"
)

func TestSecretWriter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BMC Secret Writer Suite")
}

const testNamespace = "osac-baremetal"

func newWriter(objects ...client.Object) (*SecretWriter, client.Client) {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(metal3api.AddToScheme(scheme)).To(Succeed())
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return NewSecretWriter(directclient.Wrap(k8sClient), testNamespace), k8sClient
}

var _ = Describe("BMC Secret Writer", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Create", func() {
		It("creates an operator-managed Secret owned by the BMI", func() {
			writer, k8sClient := newWriter()

			Expect(writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-A"}, credentials.BMC{
				Username: []byte(testBMCUsername),
				Password: []byte(testBMCPassword),
			})).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(secret.Type).To(Equal(corev1.SecretTypeOpaque))
			Expect(string(secret.Data["username"])).To(Equal(testBMCUsername))
			Expect(string(secret.Data["password"])).To(Equal(testBMCPassword))
			Expect(secret.Labels).To(HaveKeyWithValue(managedByLabel, shared.OsacDefaultManagedByValue))
			Expect(secret.Labels).To(HaveKeyWithValue(ownerUIDLabel, "bmi-A"))
		})

		It("updates credentials for the same BMI", func() {
			writer, k8sClient := newWriter()
			Expect(writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-A"}, credentials.BMC{
				Username: []byte(testBMCUsername), Password: []byte(testOldBMCPassword),
			})).To(Succeed())

			Expect(writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-A"}, credentials.BMC{
				Username: []byte(testBMCUsername), Password: []byte(testNewBMCPassword),
			})).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(string(secret.Data["password"])).To(Equal(testNewBMCPassword))
		})

		It("refuses an update by a different BMI", func() {
			writer, k8sClient := newWriter()
			Expect(writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-A"}, credentials.BMC{
				Username: []byte(testBMCUsername), Password: []byte(testOriginalBMCPassword),
			})).To(Succeed())

			err := writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-B"}, credentials.BMC{
				Username: []byte(testBMCUsername), Password: []byte(testReplacementBMCPassword),
			})
			Expect(err).To(MatchError(ContainSubstring("not owned by this BMI")))

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(string(secret.Data["password"])).To(Equal(testOriginalBMCPassword))
		})

		It("adopts a legacy Secret only for its BMH consumer", func() {
			bmh := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{Name: "node001", Namespace: testNamespace},
				Spec:       metal3api.BareMetalHostSpec{ConsumerRef: &corev1.ObjectReference{Name: "bmi-A"}},
			}
			legacy := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "node001-bmc-secret", Namespace: testNamespace,
				Labels: map[string]string{managedByLabel: shared.OsacDefaultManagedByValue},
			}, Data: map[string][]byte{"password": []byte(testOriginalBMCPassword)}}
			writer, k8sClient := newWriter(bmh, legacy)

			err := writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-B"}, credentials.BMC{
				Username: []byte(testBMCUsername), Password: []byte(testForeignBMCPassword),
			})
			Expect(err).To(MatchError(ContainSubstring("not owned by this BMI")))
			Expect(writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-A"}, credentials.BMC{
				Username: []byte(testBMCUsername), Password: []byte(testReplacementBMCPassword),
			})).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(legacy), secret)).To(Succeed())
			Expect(secret.Labels).To(HaveKeyWithValue(ownerUIDLabel, "bmi-A"))
			Expect(string(secret.Data["password"])).To(Equal(testReplacementBMCPassword))
		})

		It("refuses to overwrite an unmanaged Secret", func() {
			foreign := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "node001-bmc-secret", Namespace: testNamespace},
				Data:       map[string][]byte{"username": []byte(testUnmanagedBMCUsername), "password": []byte(testUnmanagedBMCPassword)},
			}
			writer, k8sClient := newWriter(foreign)

			err := writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-A"}, credentials.BMC{
				Username: []byte(testBMCUsername), Password: []byte(testBMCPassword),
			})

			Expect(err).To(MatchError(ContainSubstring("not operator-managed")))
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}, secret)).To(Succeed())
			Expect(string(secret.Data["password"])).To(Equal(testUnmanagedBMCPassword))
			Expect(secret.Labels).NotTo(HaveKey(managedByLabel))
		})

		It("rejects a missing owner", func() {
			writer, _ := newWriter()
			Expect(writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: ""}, credentials.BMC{})).To(MatchError(ContainSubstring("requires a BMI UID")))
		})
	})

	Describe("Delete", func() {
		It("deletes a Secret only for its BMI owner", func() {
			writer, k8sClient := newWriter()
			Expect(writer.Create(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-A"}, credentials.BMC{
				Username: []byte(testBMCUsername), Password: []byte(testBMCPassword),
			})).To(Succeed())

			Expect(writer.Delete(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-B"})).To(MatchError(ContainSubstring("not owned by this BMI")))
			secret := &corev1.Secret{}
			key := client.ObjectKey{Namespace: testNamespace, Name: "node001-bmc-secret"}
			Expect(k8sClient.Get(ctx, key, secret)).To(Succeed())
			Expect(writer.Delete(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-A"})).To(Succeed())
			Expect(k8sClient.Get(ctx, key, &corev1.Secret{})).To(HaveOccurred())
		})

		It("refuses to delete an unmanaged Secret", func() {
			adminSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-bmc-secret", Namespace: testNamespace},
				Data:       map[string][]byte{"username": []byte(testBMCUsername), "password": []byte(testBMCPassword)},
			}
			writer, k8sClient := newWriter(adminSecret)

			Expect(writer.Delete(ctx, credentials.Reference{HostID: "admin", BMIID: "bmi-A"})).To(MatchError(ContainSubstring("not operator-managed")))
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(adminSecret), &corev1.Secret{})).To(Succeed())
		})

		It("deletes a legacy managed Secret after its BMH is gone", func() {
			legacy := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "node001-bmc-secret", Namespace: testNamespace,
				Labels: map[string]string{managedByLabel: shared.OsacDefaultManagedByValue},
			}}
			writer, k8sClient := newWriter(legacy)

			Expect(writer.Delete(ctx, credentials.Reference{HostID: "node001", BMIID: "bmi-A"})).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(legacy), &corev1.Secret{})).To(HaveOccurred())
		})

		It("is idempotent when the Secret does not exist", func() {
			writer, _ := newWriter()
			Expect(writer.Delete(ctx, credentials.Reference{HostID: "nonexistent", BMIID: "bmi-A"})).To(Succeed())
		})
	})
})
