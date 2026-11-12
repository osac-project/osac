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

package networkattachment

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func TestDeleteOwned(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NetworkAttachment Delete Suite")
}

var _ = Describe("DeleteOwned", func() {
	const ownerFinalizer = "owner.example/finalizer"

	var (
		ctx        context.Context
		kubeClient client.Client
		key        types.NamespacedName
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		kubeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		key = types.NamespacedName{Namespace: "tenant-a", Name: "attachment-a"}
	})

	It("reports an absent attachment as complete", func() {
		done, err := DeleteOwned(ctx, kubeClient, kubeClient, key, &v1alpha1.NetworkAttachment{}, ownerFinalizer)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	It("requests deletion and waits for controller cleanup", func() {
		na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{
			Namespace: key.Namespace, Name: key.Name,
			Finalizers: []string{ownerFinalizer, "networking.example/finalizer"},
		}}
		Expect(kubeClient.Create(ctx, na)).To(Succeed())

		done, err := DeleteOwned(ctx, kubeClient, kubeClient, key, &v1alpha1.NetworkAttachment{}, ownerFinalizer)

		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		fresh := &v1alpha1.NetworkAttachment{}
		Expect(kubeClient.Get(ctx, key, fresh)).To(Succeed())
		Expect(fresh.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(fresh.Finalizers).To(ConsistOf(ownerFinalizer, "networking.example/finalizer"))
	})

	It("keeps the owner's finalizer while another finalizer remains", func() {
		na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{
			Namespace: key.Namespace, Name: key.Name,
			Finalizers: []string{ownerFinalizer, "networking.example/finalizer"},
		}}
		Expect(kubeClient.Create(ctx, na)).To(Succeed())
		Expect(kubeClient.Delete(ctx, na)).To(Succeed())

		done, err := DeleteOwned(ctx, kubeClient, kubeClient, key, &v1alpha1.NetworkAttachment{}, ownerFinalizer)

		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		fresh := &v1alpha1.NetworkAttachment{}
		Expect(kubeClient.Get(ctx, key, fresh)).To(Succeed())
		Expect(fresh.Finalizers).To(ConsistOf(ownerFinalizer, "networking.example/finalizer"))
	})

	It("removes the owner's finalizer when it is the only one left", func() {
		na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{
			Namespace: key.Namespace, Name: key.Name,
			Finalizers: []string{ownerFinalizer},
		}}
		Expect(kubeClient.Create(ctx, na)).To(Succeed())
		Expect(kubeClient.Delete(ctx, na)).To(Succeed())

		done, err := DeleteOwned(ctx, kubeClient, kubeClient, key, &v1alpha1.NetworkAttachment{}, ownerFinalizer)

		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(kubeClient.Get(ctx, key, &v1alpha1.NetworkAttachment{})).To(Satisfy(apierrors.IsNotFound))
	})
})
