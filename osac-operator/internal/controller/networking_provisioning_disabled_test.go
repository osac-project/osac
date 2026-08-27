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
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// Tests verifying that all 7 networking controllers skip provisioning and set
// resources to Ready immediately when NetworkProvisioningEnabled is false.
var _ = Describe("Networking provisioning disabled", func() {
	Context("VirtualNetworkReconciler", func() {
		It("should skip provisioning and set Ready when networking provisioning is disabled", func() {
			ctx := context.TODO()
			provisionCalled := false
			mockProvider := &mockVirtualNetworkProvider{}
			mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				return &provisioning.ProvisionResult{JobID: "should-not-fire", InitialState: osacv1alpha1.JobStatePending}, nil
			}

			r := &VirtualNetworkReconciler{
				Client:                     k8sClient,
				APIReader:                  k8sClient,
				Scheme:                     k8sClient.Scheme(),
				NetworkingNamespace:        "default",
				ProvisioningProvider:       mockProvider,
				StatusPollInterval:         1 * time.Second,
				MaxJobHistory:              10,
				NetworkProvisioningEnabled: false,
			}

			vnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-vnet",
					Namespace: "default",
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.0.0.0/16",
					NetworkClass: "cudn-net",
				},
			}
			Expect(k8sClient.Create(ctx, vnet)).To(Succeed())
			defer func() {
				fresh := &osacv1alpha1.VirtualNetwork{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), fresh); err == nil {
					fresh.Finalizers = nil
					_ = k8sClient.Update(ctx, fresh)
					_ = k8sClient.Delete(ctx, fresh)
				}
			}()

			key := types.NamespacedName{Name: vnet.Name, Namespace: vnet.Namespace}
			req := mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}}

			// First reconcile adds finalizer
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile sets Ready (noop path)
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.VirtualNetwork{}
			Expect(k8sClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.VirtualNetworkPhaseReady))
			Expect(provisionCalled).To(BeFalse(), "AAP provisioning must not be triggered when disabled")

			readyCond := apimeta.FindStatusCondition(updated.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))

			// Verify no implementation-strategy annotation was stamped
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(BeEmpty())
		})

		It("should allow deletion without errors when networking provisioning is disabled", func() {
			ctx := context.TODO()
			r := &VirtualNetworkReconciler{
				Client:                     k8sClient,
				APIReader:                  k8sClient,
				Scheme:                     k8sClient.Scheme(),
				NetworkingNamespace:        "default",
				ProvisioningProvider:       &mockVirtualNetworkProvider{},
				StatusPollInterval:         1 * time.Second,
				MaxJobHistory:              10,
				NetworkProvisioningEnabled: false,
			}

			vnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-vnet-delete",
					Namespace: "default",
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.0.0.0/16",
					NetworkClass: "cudn-net",
				},
			}
			Expect(k8sClient.Create(ctx, vnet)).To(Succeed())

			key := types.NamespacedName{Name: vnet.Name, Namespace: vnet.Namespace}
			req := mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}}

			// Add finalizer
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Set to Ready
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Delete — no annotation means deprovisioning is skipped
			Expect(k8sClient.Delete(ctx, vnet)).To(Succeed())
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify deletion completes (finalizer removed)
			deleted := &osacv1alpha1.VirtualNetwork{}
			err = k8sClient.Get(ctx, key, deleted)
			Expect(err).To(HaveOccurred()) // NotFound expected
		})
	})

	Context("SubnetReconciler", func() {
		It("should skip provisioning and set Ready when networking provisioning is disabled", func() {
			ctx := context.TODO()
			provisionCalled := false
			mockProvider := &mockSubnetProvider{}
			mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				return &provisioning.ProvisionResult{JobID: "should-not-fire", InitialState: osacv1alpha1.JobStatePending}, nil
			}

			r := &SubnetReconciler{
				Client:                     k8sClient,
				APIReader:                  k8sClient,
				Scheme:                     k8sClient.Scheme(),
				NetworkingNamespace:        "default",
				ProvisioningProvider:       mockProvider,
				StatusPollInterval:         1 * time.Second,
				MaxJobHistory:              10,
				NetworkProvisioningEnabled: false,
			}

			// Create parent VirtualNetwork
			vnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-subnet-vnet",
					Namespace: "default",
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: "noop-subnet-vnet-uuid",
					},
					Annotations: map[string]string{
						osacImplementationStrategyAnnotation: "cudn-net",
					},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.0.0.0/16",
					NetworkClass: "cudn-net",
				},
			}
			Expect(k8sClient.Create(ctx, vnet)).To(Succeed())
			defer func() {
				fresh := &osacv1alpha1.VirtualNetwork{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), fresh); err == nil {
					fresh.Finalizers = nil
					_ = k8sClient.Update(ctx, fresh)
					_ = k8sClient.Delete(ctx, fresh)
				}
			}()

			subnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-subnet",
					Namespace: "default",
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "noop-subnet-vnet-uuid",
					IPv4CIDR:       "10.0.1.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())
			defer func() {
				fresh := &osacv1alpha1.Subnet{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), fresh); err == nil {
					fresh.Finalizers = nil
					_ = k8sClient.Update(ctx, fresh)
					_ = k8sClient.Delete(ctx, fresh)
				}
			}()

			key := types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}
			req := mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}}

			// First reconcile adds finalizer
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile sets Ready (noop path)
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseReady))
			Expect(provisionCalled).To(BeFalse())

			readyCond := apimeta.FindStatusCondition(updated.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Context("SecurityGroupReconciler", func() {
		It("should skip provisioning and set Ready when networking provisioning is disabled", func() {
			ctx := context.TODO()
			testScheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
			Expect(scheme.AddToScheme(testScheme)).To(Succeed())

			provisionCalled := false
			mockProvider := &mockProvisioningProvider{name: "mock-aap"}
			mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				return &provisioning.ProvisionResult{JobID: "should-not-fire", InitialState: osacv1alpha1.JobStatePending}, nil
			}

			sg := &osacv1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-sg",
					Namespace: "test-namespace",
				},
				Spec: osacv1alpha1.SecurityGroupSpec{
					VirtualNetwork: "vn-uuid-123",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(sg).
				WithStatusSubresource(&osacv1alpha1.SecurityGroup{}).
				Build()

			r := &SecurityGroupReconciler{
				Client:                     fakeClient,
				APIReader:                  fakeClient,
				Scheme:                     testScheme,
				NetworkingNamespace:        "test-namespace",
				ProvisioningProvider:       mockProvider,
				StatusPollInterval:         1 * time.Second,
				MaxJobHistory:              10,
				NetworkProvisioningEnabled: false,
			}

			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}
			req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}}

			// First reconcile adds finalizer
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile sets Ready (noop path)
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.SecurityGroupPhaseReady))
			Expect(provisionCalled).To(BeFalse())
		})
	})

	Context("ExternalIPPoolReconciler", func() {
		It("should skip provisioning and set Ready when networking provisioning is disabled", func() {
			ctx := context.TODO()
			testScheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
			Expect(scheme.AddToScheme(testScheme)).To(Succeed())

			provisionCalled := false
			mockProvider := &mockProvisioningProvider{name: "mock-aap"}
			mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				return &provisioning.ProvisionResult{JobID: "should-not-fire", InitialState: osacv1alpha1.JobStatePending}, nil
			}

			pool := &osacv1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-pool",
					Namespace: "test-namespace",
				},
				Spec: osacv1alpha1.ExternalIPPoolSpec{
					CIDRs:                  []string{"192.168.1.0/24"},
					IPFamily:               "IPv4",
					ImplementationStrategy: "metallb-l2",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(pool).
				WithStatusSubresource(&osacv1alpha1.ExternalIPPool{}).
				Build()

			r := &ExternalIPPoolReconciler{
				Client:                     fakeClient,
				APIReader:                  fakeClient,
				Scheme:                     testScheme,
				NetworkingNamespace:        "test-namespace",
				ProvisioningProvider:       mockProvider,
				StatusPollInterval:         1 * time.Second,
				MaxJobHistory:              10,
				NetworkProvisioningEnabled: false,
			}

			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}
			req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}}

			// First reconcile adds finalizer
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile sets Ready (noop path)
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPoolPhaseReady))
			Expect(provisionCalled).To(BeFalse())
		})
	})

	Context("ExternalIPReconciler", func() {
		It("should skip provisioning and set Ready with placeholder address when networking provisioning is disabled", func() {
			ctx := context.TODO()
			testScheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
			Expect(scheme.AddToScheme(testScheme)).To(Succeed())

			provisionCalled := false
			mockProvider := &mockProvisioningProvider{name: "mock-aap"}
			mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				return &provisioning.ProvisionResult{JobID: "should-not-fire", InitialState: osacv1alpha1.JobStatePending}, nil
			}

			eip := &osacv1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-eip",
					Namespace: "test-namespace",
				},
				Spec: osacv1alpha1.ExternalIPSpec{
					Pool: "pool-uuid-123",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(eip).
				WithStatusSubresource(&osacv1alpha1.ExternalIP{}).
				Build()

			r := &ExternalIPReconciler{
				Client:                     fakeClient,
				APIReader:                  fakeClient,
				Scheme:                     testScheme,
				NetworkingNamespace:        "test-namespace",
				ProvisioningProvider:       mockProvider,
				StatusPollInterval:         1 * time.Second,
				MaxJobHistory:              10,
				NetworkProvisioningEnabled: false,
			}

			key := types.NamespacedName{Name: eip.Name, Namespace: eip.Namespace}
			req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}}

			// First reconcile adds finalizer
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile sets Ready with placeholder address (noop path)
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPhaseReady))
			Expect(updated.Status.State).To(Equal(osacv1alpha1.ExternalIPStateAllocated))
			Expect(updated.Status.Address).To(Equal("0.0.0.0"))
			Expect(provisionCalled).To(BeFalse())
		})
	})

	Context("ExternalIPAttachmentReconciler", func() {
		It("should skip provisioning and set Ready when networking provisioning is disabled", func() {
			ctx := context.TODO()
			testScheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
			Expect(scheme.AddToScheme(testScheme)).To(Succeed())

			provisionCalled := false
			mockProvider := &mockProvisioningProvider{name: "mock-aap"}
			mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				return &provisioning.ProvisionResult{JobID: "should-not-fire", InitialState: osacv1alpha1.JobStatePending}, nil
			}

			attachment := &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-attachment",
					Namespace: "test-namespace",
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: "eip-uuid-123",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(attachment).
				WithStatusSubresource(&osacv1alpha1.ExternalIPAttachment{}).
				Build()

			r := &ExternalIPAttachmentReconciler{
				Client:                     fakeClient,
				APIReader:                  fakeClient,
				Scheme:                     testScheme,
				NetworkingNamespace:        "test-namespace",
				ComputeInstanceNamespace:   "test-namespace",
				ClusterOrderNamespace:      "test-namespace",
				BaremetalInstanceNamespace: "test-namespace",
				ProvisioningProvider:       mockProvider,
				StatusPollInterval:         1 * time.Second,
				MaxJobHistory:              10,
				NetworkProvisioningEnabled: false,
			}

			key := types.NamespacedName{Name: attachment.Name, Namespace: attachment.Namespace}
			req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}}

			// First reconcile adds finalizer
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile sets Ready (noop path — skips before BMI IP check)
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPAttachmentPhaseReady))
			Expect(provisionCalled).To(BeFalse())
		})
	})

	Context("NATGatewayReconciler", func() {
		It("should skip provisioning and set Ready when networking provisioning is disabled", func() {
			ctx := context.TODO()
			provisionCalled := false
			mockProvider := &mockNATGatewayProvider{}
			mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				return &provisioning.ProvisionResult{JobID: "should-not-fire", InitialState: osacv1alpha1.JobStatePending}, nil
			}

			r := &NATGatewayReconciler{
				Client:                     k8sClient,
				APIReader:                  k8sClient,
				Scheme:                     k8sClient.Scheme(),
				NetworkingNamespace:        "default",
				ProvisioningProvider:       mockProvider,
				StatusPollInterval:         1 * time.Second,
				MaxJobHistory:              10,
				NetworkProvisioningEnabled: false,
			}

			// Create parent VirtualNetwork for NAT gateway
			vnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-natgw-vnet",
					Namespace: "default",
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: "noop-natgw-vnet-uuid",
					},
					Annotations: map[string]string{
						osacImplementationStrategyAnnotation: "cudn-net",
					},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.0.0.0/16",
					NetworkClass: "cudn-net",
				},
				Status: osacv1alpha1.VirtualNetworkStatus{
					Phase: osacv1alpha1.VirtualNetworkPhaseReady,
				},
			}
			Expect(k8sClient.Create(ctx, vnet)).To(Succeed())
			defer func() {
				fresh := &osacv1alpha1.VirtualNetwork{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), fresh); err == nil {
					fresh.Finalizers = nil
					_ = k8sClient.Update(ctx, fresh)
					_ = k8sClient.Delete(ctx, fresh)
				}
			}()

			natgw := &osacv1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "noop-natgw",
					Namespace: "default",
				},
				Spec: osacv1alpha1.NATGatewaySpec{
					VirtualNetwork: "noop-natgw-vnet-uuid",
					ExternalIP:     "eip-uuid-123",
				},
			}
			Expect(k8sClient.Create(ctx, natgw)).To(Succeed())
			defer func() {
				fresh := &osacv1alpha1.NATGateway{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(natgw), fresh); err == nil {
					fresh.Finalizers = nil
					_ = k8sClient.Update(ctx, fresh)
					_ = k8sClient.Delete(ctx, fresh)
				}
			}()

			key := types.NamespacedName{Name: natgw.Name, Namespace: natgw.Namespace}
			req := mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}}

			// First reconcile adds finalizer
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile sets Ready (noop path)
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.NATGateway{}
			Expect(k8sClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.NATGatewayPhaseReady))
			Expect(provisionCalled).To(BeFalse())

			readyCond := apimeta.FindStatusCondition(updated.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
		})
	})
})
