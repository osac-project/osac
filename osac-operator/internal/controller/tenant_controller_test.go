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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func mcReconcileRequest(nn types.NamespacedName) mcreconcile.Request {
	return mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}}
}

type tenantNamespaceLifecycleClient struct {
	client.Client
	deleteRequested bool
	deleted         bool
}

func (c *tenantNamespaceLifecycleClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, isNamespace := obj.(*corev1.Namespace); isNamespace && c.deleted {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *tenantNamespaceLifecycleClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if _, isNamespace := obj.(*corev1.Namespace); isNamespace {
		c.deleteRequested = true
		return nil
	}
	return c.Client.Delete(ctx, obj, opts...)
}

type tenantNamespaceRaceClient struct {
	client.Client
	beforePatch   func(context.Context) error
	beforeDelete  func(context.Context) error
	deleteOptions *client.DeleteOptions
}

func (c *tenantNamespaceRaceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.beforePatch != nil {
		beforePatch := c.beforePatch
		c.beforePatch = nil
		if err := beforePatch(ctx); err != nil {
			return err
		}
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *tenantNamespaceRaceClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.deleteOptions = (&client.DeleteOptions{}).ApplyOptions(opts)
	if c.beforeDelete != nil {
		beforeDelete := c.beforeDelete
		c.beforeDelete = nil
		if err := beforeDelete(ctx); err != nil {
			return err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func cleanupTenantControllerTestResources(ctx context.Context, name string) {
	tenant := &v1alpha1.Tenant{}
	tenantKey := types.NamespacedName{Name: name, Namespace: "default"}
	if err := k8sClient.Get(ctx, tenantKey, tenant); err == nil {
		tenant.Finalizers = nil
		Expect(k8sClient.Update(ctx, tenant)).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, tenant))).To(Succeed())
	} else {
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}

	namespace := &corev1.Namespace{}
	namespaceKey := types.NamespacedName{Name: name}
	if err := k8sClient.Get(ctx, namespaceKey, namespace); err == nil {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, namespace))).To(Succeed())
	} else {
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}
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

		It("should set Phase=Ready without setting NamespaceReady", func() {
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
			Expect(tenant.Status.Namespace).To(BeEmpty())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionNamespaceReady)).To(BeNil())
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

		It("should be Ready without creating a namespace or changing existing conditions", func() {
			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, tenant)).To(Succeed())
			tenant.Status.Namespace = resourceName
			tenant.SetStatusCondition(v1alpha1.TenantConditionNamespaceReady, metav1.ConditionFalse, v1alpha1.TenantReasonNotFound, "Old workload namespace missing")
			tenant.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady, metav1.ConditionTrue, v1alpha1.TenantReasonFound, "Storage available")
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			r := NewTenantReconciler(testMcManager, "default", mcmanager.LocalCluster)

			Eventually(func(g Gomega) {
				cached := &v1alpha1.Tenant{}
				g.Expect(r.Client.Get(ctx, typeNamespacedName, cached)).To(Succeed())
				g.Expect(cached.Status.Namespace).To(Equal(resourceName))
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(typeNamespacedName))
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, typeNamespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.Phase).To(Equal(v1alpha1.TenantPhaseReady))
			Expect(tenant.Status.Namespace).To(BeEmpty())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionNamespaceReady)).NotTo(BeNil())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionNamespaceReady).Status).To(Equal(metav1.ConditionFalse))
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: resourceName}, &corev1.Namespace{}))).To(BeTrue())
		})
	})

	Context("When targeting a remote cluster", func() {
		ctx := context.Background()

		It("creates one tenant namespace remotely with the Fulfillment labels", func() {
			const name = "test-remote-tenant-create"
			remoteClient := fake.NewClientBuilder().WithScheme(clientscheme.Scheme).Build()
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			tenant := &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
					Labels:    map[string]string{osacTenantIDLabel: "tenant-id-remote-create"},
				},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			Eventually(func() error {
				return r.Client.Get(ctx, nn, &v1alpha1.Tenant{})
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			remoteNamespace := &corev1.Namespace{}
			Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace)).To(Succeed())
			Expect(remoteNamespace.Labels).To(HaveKeyWithValue(osacTenantRefLabel, name))
			Expect(remoteNamespace.Labels).To(HaveKeyWithValue(osacProjectRefLabel, "default"))
			Expect(remoteNamespace.Labels).NotTo(HaveKey(osacTenantKey))
			Expect(remoteNamespace.Labels).NotTo(HaveKey(osacTenantIDLabel))
			Expect(remoteNamespace.Labels).NotTo(HaveKey("osac.openshift.io/tenant-uid"))
			Expect(remoteNamespace.Annotations).NotTo(HaveKey(osacTenantKey))
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &corev1.Namespace{}))).To(BeTrue())

			storedTenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, storedTenant)).To(Succeed())
			Expect(storedTenant.Status.Phase).To(Equal(v1alpha1.TenantPhaseReady))
			Expect(storedTenant.Status.Namespace).To(BeEmpty())

			_, err = r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace)).To(Succeed())
		})

		It("adopts a same-name namespace and repairs only the Fulfillment labels", func() {
			const name = "test-remote-tenant-adopt"
			const tenantID = "tenant-id-remote-adopt"
			remoteClient := fake.NewClientBuilder().WithScheme(clientscheme.Scheme).WithObjects(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
					Labels: map[string]string{
						osacTenantRefLabel:                "another-tenant",
						osacProjectRefLabel:               "another-project",
						"test.osac.openshift.io/preserve": "yes",
					},
					Annotations: map[string]string{"test.osac.openshift.io/preserve": "yes"},
				},
			}).Build()
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
					Labels:    map[string]string{osacTenantIDLabel: tenantID},
				},
			})).To(Succeed())
			Eventually(func() error {
				return r.Client.Get(ctx, nn, &v1alpha1.Tenant{})
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			remoteNamespace := &corev1.Namespace{}
			Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace)).To(Succeed())
			Expect(remoteNamespace.Labels).To(HaveKeyWithValue(osacTenantRefLabel, name))
			Expect(remoteNamespace.Labels).To(HaveKeyWithValue(osacProjectRefLabel, "default"))
			Expect(remoteNamespace.Labels).To(HaveKeyWithValue("test.osac.openshift.io/preserve", "yes"))
			Expect(remoteNamespace.Labels).NotTo(HaveKey(osacTenantIDLabel))
			Expect(remoteNamespace.Labels).NotTo(HaveKey(osacTenantKey))
			Expect(remoteNamespace.Annotations).To(Equal(map[string]string{"test.osac.openshift.io/preserve": "yes"}))
		})

		It("adopts a namespace without a tenant ID", func() {
			const name = "test-remote-tenant-missing-id"
			const tenantID = "tenant-id-remote-missing"
			labels := map[string]string{
				osacTenantKey:       name,
				osacTenantRefLabel:  name,
				osacProjectRefLabel: "default",
			}
			remoteClient := fake.NewClientBuilder().WithScheme(clientscheme.Scheme).WithObjects(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
			}).Build()
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name: name, Namespace: "default",
					Labels: map[string]string{osacTenantIDLabel: tenantID},
				},
			})).To(Succeed())
			Eventually(func() error {
				return r.Client.Get(ctx, nn, &v1alpha1.Tenant{})
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			remoteNamespace := &corev1.Namespace{}
			Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace)).To(Succeed())
			Expect(remoteNamespace.Labels).To(Equal(labels))
		})

		It("adopts an ID-less Tenant namespace without a UID marker", func() {
			const name = "test-remote-tenant-missing-uid"
			labels := map[string]string{
				osacTenantKey:       name,
				osacTenantRefLabel:  name,
				osacProjectRefLabel: "default",
			}
			remoteClient := fake.NewClientBuilder().WithScheme(clientscheme.Scheme).WithObjects(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
			}).Build()
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default",
			}})).To(Succeed())
			Eventually(func() error {
				return r.Client.Get(ctx, nn, &v1alpha1.Tenant{})
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			remoteNamespace := &corev1.Namespace{}
			Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace)).To(Succeed())
			Expect(remoteNamespace.Labels).To(Equal(labels))
		})

		It("overwrites tenant association labels on a same-name namespace", func() {
			const name = "test-remote-tenant-conflict"
			remoteClient := fake.NewClientBuilder().WithScheme(clientscheme.Scheme).WithObjects(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
					Labels: map[string]string{
						osacTenantKey:       "another-tenant",
						osacTenantRefLabel:  "another-tenant",
						osacProjectRefLabel: "default",
						osacTenantIDLabel:   "tenant-id-other",
					},
				},
			}).Build()
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
					Labels:    map[string]string{osacTenantIDLabel: "tenant-id-remote-conflict"},
				},
			})).To(Succeed())
			storedTenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, storedTenant)).To(Succeed())
			storedTenant.Status.Phase = v1alpha1.TenantPhaseReady
			storedTenant.Status.Namespace = "stale-namespace"
			Expect(k8sClient.Status().Update(ctx, storedTenant)).To(Succeed())
			Eventually(func(g Gomega) {
				cachedTenant := &v1alpha1.Tenant{}
				g.Expect(r.Client.Get(ctx, nn, cachedTenant)).To(Succeed())
				g.Expect(cachedTenant.Status.Phase).To(Equal(v1alpha1.TenantPhaseReady))
				g.Expect(cachedTenant.Status.Namespace).To(Equal("stale-namespace"))
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, nn, storedTenant)).To(Succeed())
			Expect(storedTenant.Status.Phase).To(Equal(v1alpha1.TenantPhaseReady))
			Expect(storedTenant.Status.Namespace).To(BeEmpty())
			remoteNamespace := &corev1.Namespace{}
			Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace)).To(Succeed())
			Expect(remoteNamespace.Labels[osacTenantRefLabel]).To(Equal(name))
			Expect(remoteNamespace.Labels[osacProjectRefLabel]).To(Equal("default"))
			Expect(remoteNamespace.Labels[osacTenantKey]).To(Equal("another-tenant"))
			Expect(remoteNamespace.Labels[osacTenantIDLabel]).To(Equal("tenant-id-other"))
		})

		It("preserves a conflicting tenant annotation on an existing namespace", func() {
			const name = "test-remote-tenant-annotation-conflict"
			const tenantID = "tenant-id-remote-annotation-conflict"
			labels := map[string]string{
				osacTenantKey:       name,
				osacTenantRefLabel:  name,
				osacProjectRefLabel: "default",
				osacTenantIDLabel:   tenantID,
			}
			annotations := map[string]string{
				osacTenantKey:                     "another-tenant",
				"test.osac.openshift.io/preserve": "yes",
			}
			remoteClient := fake.NewClientBuilder().WithScheme(clientscheme.Scheme).WithObjects(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels, Annotations: annotations},
			}).Build()
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name: name, Namespace: "default",
					Labels: map[string]string{osacTenantIDLabel: tenantID},
				},
			})).To(Succeed())
			Eventually(func() error {
				return r.Client.Get(ctx, nn, &v1alpha1.Tenant{})
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			remoteNamespace := &corev1.Namespace{}
			Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace)).To(Succeed())
			Expect(remoteNamespace.Labels).To(Equal(labels))
			Expect(remoteNamespace.Annotations).To(Equal(annotations))
		})

		It("waits for other Tenant finalizers and remote namespace deletion", func() {
			const name = "test-remote-tenant-delete"
			remoteClient := &tenantNamespaceLifecycleClient{
				Client: fake.NewClientBuilder().WithScheme(clientscheme.Scheme).Build(),
			}
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			tenantID := "tenant-id-remote-delete"
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:       name,
					Namespace:  "default",
					Labels:     map[string]string{osacTenantIDLabel: tenantID},
					Finalizers: []string{tenantFinalizer, "test.osac.openshift.io/storage"},
				},
			})).To(Succeed())
			Expect(remoteClient.Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
					Labels: map[string]string{
						osacTenantRefLabel:  name,
						osacProjectRefLabel: "default",
					},
				},
			})).To(Succeed())
			Expect(k8sClient.Delete(ctx, &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}})).To(Succeed())

			Eventually(func(g Gomega) {
				tenant := &v1alpha1.Tenant{}
				g.Expect(r.Client.Get(ctx, nn, tenant)).To(Succeed())
				g.Expect(tenant.DeletionTimestamp.IsZero()).To(BeFalse())
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())
			result, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			remoteNamespace := &corev1.Namespace{}
			Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace)).To(Succeed())

			storedTenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, storedTenant)).To(Succeed())
			storedTenant.Finalizers = []string{tenantFinalizer}
			Expect(k8sClient.Update(ctx, storedTenant)).To(Succeed())
			Eventually(func(g Gomega) {
				cachedTenant := &v1alpha1.Tenant{}
				g.Expect(r.Client.Get(ctx, nn, cachedTenant)).To(Succeed())
				g.Expect(cachedTenant.Finalizers).To(Equal([]string{tenantFinalizer}))
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			result, err = r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace)).To(Succeed())
			Expect(k8sClient.Get(ctx, nn, storedTenant)).To(Succeed())
			Expect(storedTenant.Finalizers).To(ContainElement(tenantFinalizer))

			Expect(remoteClient.deleteRequested).To(BeTrue())
			remoteClient.deleted = true
			result, err = r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, nn, storedTenant))).To(BeTrue())
		})

		It("deletes a same-name namespace despite conflicting tenant labels", func() {
			const name = "test-remote-tenant-delete-conflict"
			remoteClient := fake.NewClientBuilder().WithScheme(clientscheme.Scheme).WithObjects(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
					Labels: map[string]string{
						osacTenantRefLabel:  "another-tenant",
						osacProjectRefLabel: "another-project",
						osacTenantIDLabel:   "tenant-id-foreign",
					},
				},
			}).Build()
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default",
				Labels:     map[string]string{osacTenantIDLabel: "tenant-id-remote-delete-conflict"},
				Finalizers: []string{tenantFinalizer},
			}})).To(Succeed())
			Expect(k8sClient.Delete(ctx, &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}})).To(Succeed())
			Eventually(func(g Gomega) {
				tenant := &v1alpha1.Tenant{}
				g.Expect(r.Client.Get(ctx, nn, tenant)).To(Succeed())
				g.Expect(tenant.DeletionTimestamp.IsZero()).To(BeFalse())
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			result, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			remoteNamespace := &corev1.Namespace{}
			Expect(apierrors.IsNotFound(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace))).To(BeTrue())
			Eventually(func(g Gomega) {
				tenant := &v1alpha1.Tenant{}
				g.Expect(r.Client.Get(ctx, nn, tenant)).To(Succeed())
				g.Expect(tenant.Status.Phase).To(Equal(v1alpha1.TenantPhaseDeleting))
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err = r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			storedTenant := &v1alpha1.Tenant{}
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, nn, storedTenant))).To(BeTrue())
		})

		It("deletes a same-name namespace without a tenant ID", func() {
			const name = "test-remote-tenant-delete-missing-id"
			remoteClient := fake.NewClientBuilder().WithScheme(clientscheme.Scheme).WithObjects(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: name},
			}).Build()
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default",
				Labels:     map[string]string{osacTenantIDLabel: "tenant-id-remote-delete-missing"},
				Finalizers: []string{tenantFinalizer},
			}})).To(Succeed())
			Expect(k8sClient.Delete(ctx, &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}})).To(Succeed())
			Eventually(func(g Gomega) {
				tenant := &v1alpha1.Tenant{}
				g.Expect(r.Client.Get(ctx, nn, tenant)).To(Succeed())
				g.Expect(tenant.DeletionTimestamp.IsZero()).To(BeFalse())
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			result, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			remoteNamespace := &corev1.Namespace{}
			Expect(apierrors.IsNotFound(remoteClient.Get(ctx, types.NamespacedName{Name: name}, remoteNamespace))).To(BeTrue())
			_, err = r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, nn, &v1alpha1.Tenant{}))).To(BeTrue())
		})

		It("retries label repair when namespace labels change during patch", func() {
			const name = "test-remote-tenant-adopt-race"
			DeferCleanup(func() { cleanupTenantControllerTestResources(ctx, name) })

			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					osacTenantRefLabel:  "another-tenant",
					osacProjectRefLabel: "another-project",
				},
			}}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			remoteClient := &tenantNamespaceRaceClient{
				Client: k8sClient,
				beforePatch: func(ctx context.Context) error {
					latest := &corev1.Namespace{}
					if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, latest); err != nil {
						return err
					}
					latest.Labels[osacTenantRefLabel] = "raced-tenant"
					return k8sClient.Update(ctx, latest)
				},
			}
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default",
			}})).To(Succeed())
			Eventually(func() error { return r.Client.Get(ctx, nn, &v1alpha1.Tenant{}) }).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(apierrors.IsConflict(err)).To(BeTrue())
			storedNamespace := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, storedNamespace)).To(Succeed())
			Expect(storedNamespace.Labels).To(HaveKeyWithValue(osacTenantRefLabel, "raced-tenant"))

			_, err = r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, storedNamespace)).To(Succeed())
			Expect(storedNamespace.Labels).To(HaveKeyWithValue(osacTenantRefLabel, name))
			Expect(storedNamespace.Labels).To(HaveKeyWithValue(osacProjectRefLabel, "default"))
			storedTenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, storedTenant)).To(Succeed())
			Expect(storedTenant.Status.Phase).To(Equal(v1alpha1.TenantPhaseReady))
		})

		It("retries namespace deletion when its labels change after lookup", func() {
			const name = "test-remote-tenant-delete-race"
			DeferCleanup(func() { cleanupTenantControllerTestResources(ctx, name) })

			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					osacTenantRefLabel:  name,
					osacProjectRefLabel: "default",
				},
			}}
			remoteBaseClient := fake.NewClientBuilder().WithScheme(clientscheme.Scheme).WithObjects(namespace).Build()
			Expect(remoteBaseClient.Get(ctx, types.NamespacedName{Name: name}, namespace)).To(Succeed())
			initialUID, initialResourceVersion := namespace.UID, namespace.ResourceVersion
			remoteClient := &tenantNamespaceRaceClient{
				Client: remoteBaseClient,
				beforeDelete: func(ctx context.Context) error {
					latest := &corev1.Namespace{}
					if err := remoteBaseClient.Get(ctx, types.NamespacedName{Name: name}, latest); err != nil {
						return err
					}
					latest.Labels[osacTenantRefLabel] = "another-tenant"
					return remoteBaseClient.Update(ctx, latest)
				},
			}
			r := NewTenantReconciler(newRemoteTestMcManager(remoteClient), "default", mc.ClusterName("remote"))
			nn := types.NamespacedName{Name: name, Namespace: "default"}
			Expect(k8sClient.Create(ctx, &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", Finalizers: []string{tenantFinalizer},
			}})).To(Succeed())
			Expect(k8sClient.Delete(ctx, &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default",
			}})).To(Succeed())
			Eventually(func(g Gomega) {
				tenant := &v1alpha1.Tenant{}
				g.Expect(r.Client.Get(ctx, nn, tenant)).To(Succeed())
				g.Expect(tenant.DeletionTimestamp.IsZero()).To(BeFalse())
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(apierrors.IsConflict(err)).To(BeTrue())
			Expect(remoteClient.deleteOptions).NotTo(BeNil())
			Expect(remoteClient.deleteOptions.Preconditions.UID).NotTo(BeNil())
			Expect(*remoteClient.deleteOptions.Preconditions.UID).To(Equal(initialUID))
			Expect(remoteClient.deleteOptions.Preconditions.ResourceVersion).NotTo(BeNil())
			Expect(*remoteClient.deleteOptions.Preconditions.ResourceVersion).To(Equal(initialResourceVersion))

			storedNamespace := &corev1.Namespace{}
			Expect(remoteBaseClient.Get(ctx, types.NamespacedName{Name: name}, storedNamespace)).To(Succeed())
			Expect(storedNamespace.Labels).To(HaveKeyWithValue(osacTenantRefLabel, "another-tenant"))
			Expect(storedNamespace.DeletionTimestamp.IsZero()).To(BeTrue())
			result, err := r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(apierrors.IsNotFound(remoteBaseClient.Get(ctx, types.NamespacedName{Name: name}, storedNamespace))).To(BeTrue())
			_, err = r.Reconcile(ctx, mcReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, nn, &v1alpha1.Tenant{}))).To(BeTrue())
		})
	})
})
