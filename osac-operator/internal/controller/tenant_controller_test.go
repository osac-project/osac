/*
Copyright 2025.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

func mcReconcileRequest(nn types.NamespacedName) mcreconcile.Request {
	return mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}}
}

var _ = Describe("Tenant Controller", func() {
	Context("When namespace exists", func() {
		ctx := context.Background()

		createTenantWithNamespace := func(name string) types.NamespacedName {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
					Labels: map[string]string{
						"osac.openshift.io/tenant-ref": name,
						"osac.openshift.io/project":    "default",
					},
				},
			}
			if err := k8sClient.Create(ctx, ns); err != nil {
				Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
			}
			tenant := &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
			return types.NamespacedName{Name: name, Namespace: "default"}
		}

		It("should set Phase=Ready and NamespaceReady=True", func() {
			nn := createTenantWithNamespace("test-tenant-ns-phase")
			r := NewTenantReconciler(testMcManager, "default", mcmanager.LocalCluster)

			Eventually(func() error {
				return r.Client.Get(ctx, nn, &v1alpha1.Tenant{})
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.Phase).To(Equal(v1alpha1.TenantPhaseReady))
			Expect(tenant.Status.Namespace).To(Equal(nn.Name))

			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionNamespaceReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should not modify storage fields", func() {
			nn := createTenantWithNamespace("test-tenant-ns-storage")
			r := NewTenantReconciler(testMcManager, "default", mcmanager.LocalCluster)

			Eventually(func() error {
				return r.Client.Get(ctx, nn, &v1alpha1.Tenant{})
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.StorageClasses).To(BeNil())
			Expect(tenant.Status.ProvisioningJobs).To(BeNil())
		})
	})

	Context("When namespace does not exist", func() {
		const resourceName = "test-tenant-no-ns"

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

		BeforeEach(func() {
			tenant := &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
		})

		AfterEach(func() {
			tenant := &v1alpha1.Tenant{}
			if err := k8sClient.Get(ctx, typeNamespacedName, tenant); err == nil {
				Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())
			}
		})

		It("should set NamespaceReady=False and stay Progressing", func() {
			r := NewTenantReconciler(testMcManager, "default", mcmanager.LocalCluster)

			Eventually(func() error {
				return r.Client.Get(ctx, typeNamespacedName, &v1alpha1.Tenant{})
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(typeNamespacedName))
			Expect(err).To(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.Phase).To(Equal(v1alpha1.TenantPhaseProgressing))

			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionNamespaceReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})
	})
})
