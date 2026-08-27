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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/dispatcheradapter"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("SubnetReconciler", func() {
	var (
		reconciler   *SubnetReconciler
		mockProvider *mockSubnetProvider
		ctx          context.Context
		subnet       *osacv1alpha1.Subnet
		vnet         *osacv1alpha1.VirtualNetwork
	)

	BeforeEach(func() {
		ctx = context.TODO()
		mockProvider = &mockSubnetProvider{}
		reconciler = &SubnetReconciler{
			Client:                     k8sClient,
			APIReader:                  k8sClient,
			Scheme:                     k8sClient.Scheme(),
			NetworkingNamespace:        "default",
			ProvisioningProvider:       mockProvider,
			StatusPollInterval:         1 * time.Second,
			MaxJobHistory:              10,
			NetworkProvisioningEnabled: true,
		}

		// Create VirtualNetwork fixture. SubnetReconciler reads the fabric implementation
		// strategy from the parent VirtualNetwork's annotation (already resolved by the
		// VirtualNetwork's own controller) when its own Resolver has no dispatch plan for
		// the NetworkClass, so set it directly here rather than via a dispatcher Resolver.
		vnet = &osacv1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-vnet",
				Namespace: "default",
				Labels: map[string]string{
					osacVirtualNetworkIDLabel: "test-vnet-uuid",
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

		// Create Subnet fixture
		subnet = &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-subnet",
				Namespace: "default",
			},
			Spec: osacv1alpha1.SubnetSpec{
				VirtualNetwork: "test-vnet-uuid",
				IPv4CIDR:       "10.0.1.0/24",
			},
		}
	})

	AfterEach(func() {
		// Cleanup VirtualNetwork
		vnetKey := types.NamespacedName{Name: vnet.Name, Namespace: vnet.Namespace}
		existingVnet := &osacv1alpha1.VirtualNetwork{}
		if err := k8sClient.Get(ctx, vnetKey, existingVnet); err == nil {
			existingVnet.Finalizers = nil
			_ = k8sClient.Update(ctx, existingVnet)
			_ = k8sClient.Delete(ctx, existingVnet)
		}

		// Cleanup Subnet if it exists
		subnetKey := types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}
		existingSubnet := &osacv1alpha1.Subnet{}
		if err := k8sClient.Get(ctx, subnetKey, existingSubnet); err == nil {
			existingSubnet.Finalizers = nil
			_ = k8sClient.Update(ctx, existingSubnet)
			_ = k8sClient.Delete(ctx, existingSubnet)
		}

		// Cleanup Lease
		leaseKey := types.NamespacedName{Name: "netris-vnet-lock-" + subnet.Name, Namespace: subnet.Namespace}
		existingLease := &coordinationv1.Lease{}
		if err := k8sClient.Get(ctx, leaseKey, existingLease); err == nil {
			_ = k8sClient.Delete(ctx, existingLease)
		}
	})

	Context("Reconcile", func() {
		It("should add finalizer on first reconcile", func() {
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnet.Name,
					Namespace: subnet.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated Subnet
			updatedSubnet := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}, updatedSubnet)).To(Succeed())
			Expect(updatedSubnet.Finalizers).To(ContainElement(osacSubnetFinalizer))
		})

		It("should set phase to Progressing on first reconcile", func() {
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())

			req := mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnet.Name,
					Namespace: subnet.Namespace,
				},
			}}

			// First reconcile sets annotation and requeues
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Second reconcile persists the Progressing phase
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated Subnet
			updatedSubnet := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}, updatedSubnet)).To(Succeed())
			Expect(updatedSubnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseProgressing))
		})

		It("should persist job status even when resource is concurrently modified", func() {
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())

			req := mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnet.Name,
					Namespace: subnet.Namespace,
				},
			}}

			// First reconcile: adds finalizer + sets annotation, returns early
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Simulate feedback controller: during TriggerProvision, modify
			// the resource's metadata (add feedback finalizer) so the
			// resourceVersion changes before the status flush runs.
			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				fresh := &osacv1alpha1.Subnet{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}, fresh)).To(Succeed())
				fresh.Finalizers = append(fresh.Finalizers, "osac.openshift.io/subnet-feedback")
				Expect(k8sClient.Update(ctx, fresh)).To(Succeed())

				return &provisioning.ProvisionResult{
					JobID:        "concurrent-job-123",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Provisioning triggered",
				}, nil
			}

			// Second reconcile: triggers job — the concurrent modification
			// must not prevent the job from being recorded in status.
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify the job was persisted to the API server
			updatedSubnet := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}, updatedSubnet)).To(Succeed())
			latestJob := provisioning.FindLatestJobByType(updatedSubnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("concurrent-job-123"))
		})

		It("should requeue when parent VirtualNetwork not found", func() {
			// Create subnet with non-existent parent
			subnetNoParent := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnet-no-parent",
					Namespace: "default",
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "missing-vnet",
					IPv4CIDR:       "10.0.2.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, subnetNoParent)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetNoParent.Name,
					Namespace: subnetNoParent.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			// Cleanup
			_ = k8sClient.Delete(ctx, subnetNoParent)
		})

		It("should return an error when multiple VirtualNetworks share the parent uuid label", func() {
			// Create a second VirtualNetwork with the same osacVirtualNetworkIDLabel as
			// the fixture "vnet" created in BeforeEach, simulating an ambiguous parent lookup.
			duplicateVnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vnet-duplicate",
					Namespace: "default",
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: "test-vnet-uuid",
					},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.9.0.0/16",
					NetworkClass: "cudn-net",
				},
			}
			Expect(k8sClient.Create(ctx, duplicateVnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, duplicateVnet)

			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnet.Name,
					Namespace: subnet.Namespace,
				},
			}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("expected exactly one parent VirtualNetwork"))
		})

		It("should requeue when parent VirtualNetwork has no implementation-strategy annotation", func() {
			// Create VirtualNetwork without an implementation-strategy annotation
			vnetNoStrategy := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vnet-no-strategy",
					Namespace: "default",
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: "vnet-no-strategy-uuid",
					},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.0.0.0/16",
					NetworkClass: "some-class",
					// implementation-strategy annotation intentionally not set
				},
			}
			Expect(k8sClient.Create(ctx, vnetNoStrategy)).To(Succeed())

			subnetNoStrategy := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnet-no-strategy",
					Namespace: "default",
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-no-strategy-uuid",
					IPv4CIDR:       "10.0.3.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, subnetNoStrategy)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetNoStrategy.Name,
					Namespace: subnetNoStrategy.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			// Cleanup
			_ = k8sClient.Delete(ctx, subnetNoStrategy)
			_ = k8sClient.Delete(ctx, vnetNoStrategy)
		})

		It("should ignore subnet with unmanaged annotation", func() {
			unmanagedSubnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unmanaged-subnet",
					Namespace: "default",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "test-vnet",
					IPv4CIDR:       "10.0.4.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, unmanagedSubnet)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      unmanagedSubnet.Name,
					Namespace: unmanagedSubnet.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())

			// Verify status was not updated
			updatedSubnet := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: unmanagedSubnet.Name, Namespace: unmanagedSubnet.Namespace}, updatedSubnet)).To(Succeed())
			Expect(updatedSubnet.Status.Phase).To(BeEmpty())

			// Cleanup
			_ = k8sClient.Delete(ctx, unmanagedSubnet)
		})

		It("should create V-Net lock lease on first reconcile", func() {
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())

			req := mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnet.Name,
					Namespace: subnet.Namespace,
				},
			}}

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify Lease was created
			lease := &coordinationv1.Lease{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "netris-vnet-lock-" + subnet.Name,
				Namespace: subnet.Namespace,
			}, lease)).To(Succeed())

			// Verify owner reference
			Expect(lease.OwnerReferences).To(HaveLen(1))
			Expect(lease.OwnerReferences[0].Kind).To(Equal("Subnet"))
			Expect(lease.OwnerReferences[0].Name).To(Equal(subnet.Name))
			Expect(*lease.OwnerReferences[0].Controller).To(BeTrue())
			Expect(*lease.OwnerReferences[0].BlockOwnerDeletion).To(BeFalse())

			// Verify lease duration
			Expect(*lease.Spec.LeaseDurationSeconds).To(Equal(int32(120)))
		})

		It("should be idempotent when lease already exists", func() {
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())

			req := mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnet.Name,
					Namespace: subnet.Namespace,
				},
			}}

			// First reconcile creates the lease
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile should not error (lease already exists)
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify lease still exists with same properties
			lease := &coordinationv1.Lease{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "netris-vnet-lock-" + subnet.Name,
				Namespace: subnet.Namespace,
			}, lease)).To(Succeed())
			Expect(*lease.Spec.LeaseDurationSeconds).To(Equal(int32(120)))
		})

		It("should wait for child ComputeInstance before deprovisioning", func() {
			testSubnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "subnet-with-ci",
					Namespace:  "default",
					Finalizers: []string{osacSubnetFinalizer},
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "ci-gate-vn",
					IPv4CIDR:       "10.0.10.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, testSubnet)).To(Succeed())

			ciSpec := newTestComputeInstanceSpec("test_template")
			ciSpec.NetworkAttachments = []osacv1alpha1.ComputeNetworkAttachment{
				{SubnetRef: testSubnet.Name},
			}
			childCI := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "child-ci",
					Namespace: "default",
				},
				Spec: ciSpec,
			}
			Expect(k8sClient.Create(ctx, childCI)).To(Succeed())

			result, err := reconciler.handleDelete(ctx, testSubnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			// Clean up
			Expect(k8sClient.Delete(ctx, childCI)).To(Succeed())
			_ = k8sClient.Delete(ctx, testSubnet)
		})

		It("should wait for child BareMetalInstance before deprovisioning", func() {
			testSubnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "subnet-with-bmi",
					Namespace:  "default",
					Finalizers: []string{osacSubnetFinalizer},
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "bmi-gate-vn",
					IPv4CIDR:       "10.0.11.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, testSubnet)).To(Succeed())

			childBMI := &bmfov1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "child-bmi",
					Namespace: "default",
				},
				Spec: bmfov1alpha1.BareMetalInstanceSpec{
					HostType:   "test-host",
					TemplateID: "noop",
					NetworkAttachments: []bmfov1alpha1.BareMetalNetworkAttachment{
						{SubnetRef: testSubnet.Name, Primary: true},
					},
				},
			}
			Expect(k8sClient.Create(ctx, childBMI)).To(Succeed())

			result, err := reconciler.handleDelete(ctx, testSubnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			// Clean up
			Expect(k8sClient.Delete(ctx, childBMI)).To(Succeed())
			_ = k8sClient.Delete(ctx, testSubnet)
		})

		It("should still handle delete for unmanaged subnet with finalizer", func() {
			managedThenUnmanaged := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-then-unmanaged",
					Namespace: "default",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
					Finalizers: []string{osacSubnetFinalizer},
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "test-vnet-uuid",
					IPv4CIDR:       "10.0.5.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, managedThenUnmanaged)).To(Succeed())

			key := types.NamespacedName{Name: managedThenUnmanaged.Name, Namespace: managedThenUnmanaged.Namespace}

			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionSkipped,
				}, nil
			}

			Expect(k8sClient.Delete(ctx, managedThenUnmanaged)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: key,
			}})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, key, &osacv1alpha1.Subnet{}))
			}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())
		})
	})

	Context("dispatcher path", func() {
		var fakeDiscoveryClient client.Client

		BeforeEach(func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeDiscoveryClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				newFabricManagerConfigMap("fm-netris", "osac", "netris"),
			).Build()
		})

		It("uses the resolved fabric manager name from the parent VirtualNetwork's NetworkClass", func() {
			disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
			Expect(err).NotTo(HaveOccurred())
			reconciler.Resolver = dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
				[]*privatev1.NetworkClass{{Id: "nc-dispatch", FabricManager: ptr.To("netris")}}, &[]*privatev1.NetworkClass{},
			)), disc)

			dispatchVnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dispatch-vnet",
					Namespace: "default",
					Labels:    map[string]string{osacVirtualNetworkIDLabel: "dispatch-vnet-uuid"},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.1.0.0/16",
					NetworkClass: "nc-dispatch",
				},
			}
			Expect(k8sClient.Create(ctx, dispatchVnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, dispatchVnet)

			dispatchSubnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "dispatch-subnet", Namespace: "default"},
				Spec:       osacv1alpha1.SubnetSpec{VirtualNetwork: "dispatch-vnet-uuid", IPv4CIDR: "10.1.1.0/24"},
			}
			Expect(k8sClient.Create(ctx, dispatchSubnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, dispatchSubnet)

			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dispatchSubnet.Name, Namespace: dispatchSubnet.Namespace},
			}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dispatchSubnet.Name, Namespace: dispatchSubnet.Namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal("netris"))
		})

		It("falls back to the parent VirtualNetwork's resolved implementation-strategy annotation when fabricManager is not set", func() {
			disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
			Expect(err).NotTo(HaveOccurred())
			reconciler.Resolver = dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
				[]*privatev1.NetworkClass{{Id: "nc-legacy"}}, &[]*privatev1.NetworkClass{},
			)), disc)

			dispatchVnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "legacy-vnet",
					Namespace: "default",
					Labels:    map[string]string{osacVirtualNetworkIDLabel: "legacy-vnet-uuid"},
					// The parent VirtualNetwork's own controller has already resolved and
					// written this annotation (its NetworkClass has no fabricManager either).
					Annotations: map[string]string{osacImplementationStrategyAnnotation: "cudn-net"},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.2.0.0/16",
					NetworkClass: "nc-legacy",
				},
			}
			Expect(k8sClient.Create(ctx, dispatchVnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, dispatchVnet)

			dispatchSubnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "legacy-subnet", Namespace: "default"},
				Spec:       osacv1alpha1.SubnetSpec{VirtualNetwork: "legacy-vnet-uuid", IPv4CIDR: "10.2.1.0/24"},
			}
			Expect(k8sClient.Create(ctx, dispatchSubnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, dispatchSubnet)

			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dispatchSubnet.Name, Namespace: dispatchSubnet.Namespace},
			}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dispatchSubnet.Name, Namespace: dispatchSubnet.Namespace}, updated)).To(Succeed())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal("cudn-net"))
		})

		It("triggers both fabric and k8s provisioning jobs and persists both implementation-strategy annotations when the NetworkClass has both managers", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			dualDiscoveryClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				newFabricManagerConfigMap("fm-netris", "osac", "netris"),
				newK8sManagerConfigMap("km-cudn", "osac", "cudn_net", "ipv4"),
			).Build()
			disc, err := networkmanager.NewDiscovery(dualDiscoveryClient, "osac")
			Expect(err).NotTo(HaveOccurred())
			k8sManagerName := "cudn_net"
			reconciler.Resolver = dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
				[]*privatev1.NetworkClass{{Id: "nc-dual", FabricManager: ptr.To("netris"), K8SManager: &k8sManagerName}},
				&[]*privatev1.NetworkClass{},
			)), disc)

			dualVnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dual-vnet",
					Namespace: "default",
					Labels:    map[string]string{osacVirtualNetworkIDLabel: "dual-vnet-uuid"},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.4.0.0/16",
					NetworkClass: "nc-dual",
				},
			}
			Expect(k8sClient.Create(ctx, dualVnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, dualVnet)

			dualSubnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "dual-subnet", Namespace: "default"},
				Spec:       osacv1alpha1.SubnetSpec{VirtualNetwork: "dual-vnet-uuid", IPv4CIDR: "10.4.1.0/24"},
			}
			Expect(k8sClient.Create(ctx, dualSubnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, dualSubnet)

			var seenAnnotations []string
			triggerCount := 0
			mockProvider.triggerProvisionFunc = func(_ context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				triggerCount++
				seenAnnotations = append(seenAnnotations, resource.GetAnnotations()[osacImplementationStrategyAnnotation])
				return &provisioning.ProvisionResult{JobID: fmt.Sprintf("job-%d", triggerCount), InitialState: osacv1alpha1.JobStatePending}, nil
			}

			key := types.NamespacedName{Name: dualSubnet.Name, Namespace: dualSubnet.Namespace}
			req := mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}}

			// First reconcile: adds finalizer, then (same call) sets both
			// implementation-strategy annotations since neither is present yet.
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			afterAnnotations := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, key, afterAnnotations)).To(Succeed())
			Expect(afterAnnotations.Annotations[osacImplementationStrategyAnnotation]).To(Equal("netris"))
			Expect(afterAnnotations.Annotations[osacK8sImplementationStrategyAnnotation]).To(Equal("cudn_net"))
			Expect(triggerCount).To(Equal(0))

			// Second reconcile: annotations already match, proceeds to trigger
			// both targets' provision jobs in the same call.
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			Expect(triggerCount).To(Equal(2))
			Expect(seenAnnotations).To(ConsistOf("netris", "cudn_net"))

			final := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, key, final)).To(Succeed())
			Expect(provisioning.FindLatestJobByTypeAndTarget(final.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision, string(dispatcher.ManagerRoleFabric))).NotTo(BeNil())
			Expect(provisioning.FindLatestJobByTypeAndTarget(final.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision, string(dispatcher.ManagerRoleK8s))).NotTo(BeNil())
		})

		It("clears the stale k8s implementation-strategy annotation when the NetworkClass later drops its k8sManager", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			dualDiscoveryClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				newFabricManagerConfigMap("fm-netris", "osac", "netris"),
				newK8sManagerConfigMap("km-cudn", "osac", "cudn_net", "ipv4"),
			).Build()
			disc, err := networkmanager.NewDiscovery(dualDiscoveryClient, "osac")
			Expect(err).NotTo(HaveOccurred())
			k8sManagerName := "cudn_net"
			// Held by pointer so mutating its K8SManager field below simulates the
			// NetworkClass being updated (e.g. via fulfillment-service) between reconciles,
			// without needing to construct a second resolver.
			transitionNetworkClass := &privatev1.NetworkClass{Id: "nc-dual-to-fabric", FabricManager: ptr.To("netris"), K8SManager: &k8sManagerName}
			reconciler.Resolver = dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
				[]*privatev1.NetworkClass{transitionNetworkClass}, &[]*privatev1.NetworkClass{},
			)), disc)

			transitionVnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "transition-vnet",
					Namespace: "default",
					Labels:    map[string]string{osacVirtualNetworkIDLabel: "transition-vnet-uuid"},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.5.0.0/16",
					NetworkClass: "nc-dual-to-fabric",
				},
			}
			Expect(k8sClient.Create(ctx, transitionVnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, transitionVnet)

			transitionSubnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "transition-subnet", Namespace: "default"},
				Spec:       osacv1alpha1.SubnetSpec{VirtualNetwork: "transition-vnet-uuid", IPv4CIDR: "10.5.1.0/24"},
			}
			Expect(k8sClient.Create(ctx, transitionSubnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, transitionSubnet)

			key := types.NamespacedName{Name: transitionSubnet.Name, Namespace: transitionSubnet.Namespace}
			req := mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}}

			// First reconcile: adds finalizer, then (same call) sets both
			// implementation-strategy annotations since the NetworkClass has both managers.
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			dual := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, key, dual)).To(Succeed())
			Expect(dual.Annotations[osacImplementationStrategyAnnotation]).To(Equal("netris"))
			Expect(dual.Annotations[osacK8sImplementationStrategyAnnotation]).To(Equal("cudn_net"))

			// NetworkClass drops its k8sManager (e.g. migrated to fabric-only). The next
			// reconcile resolves a fabric-only plan, but must not immediately drop the
			// k8s annotation: it should first deprovision the now-stale k8s target so
			// the k8s manager's resource isn't orphaned. Deprovisioning triggers on this
			// reconcile and hasn't reached a terminal state yet, so both annotations —
			// and the pending k8s deprovision job — are still present.
			transitionNetworkClass.K8SManager = nil

			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			midTransition := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, key, midTransition)).To(Succeed())
			Expect(midTransition.Annotations[osacImplementationStrategyAnnotation]).To(Equal("netris"))
			Expect(midTransition.Annotations[osacK8sImplementationStrategyAnnotation]).To(Equal("cudn_net"))
			k8sDeprovisionJob := provisioning.FindLatestJobByTypeAndTarget(midTransition.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision, "k8s")
			Expect(k8sDeprovisionJob).NotTo(BeNil())
			Expect(k8sDeprovisionJob.State.IsTerminal()).To(BeFalse())

			// Once the k8s target's deprovision job reaches a terminal, successful state
			// (polled on the next reconcile), the k8s annotation is finally removed while
			// the fabric one remains intact.
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			afterTransition := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, key, afterTransition)).To(Succeed())
			Expect(afterTransition.Annotations[osacImplementationStrategyAnnotation]).To(Equal("netris"))
			Expect(afterTransition.Annotations).NotTo(HaveKey(osacK8sImplementationStrategyAnnotation))
		})

		It("returns a reconcile error when the NetworkClass references an unregistered manager", func() {
			disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
			Expect(err).NotTo(HaveOccurred())
			reconciler.Resolver = dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
				[]*privatev1.NetworkClass{{Id: "nc-broken", FabricManager: ptr.To("does-not-exist")}}, &[]*privatev1.NetworkClass{},
			)), disc)

			dispatchVnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "broken-vnet",
					Namespace: "default",
					Labels:    map[string]string{osacVirtualNetworkIDLabel: "broken-vnet-uuid"},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.3.0.0/16",
					NetworkClass: "nc-broken",
				},
			}
			Expect(k8sClient.Create(ctx, dispatchVnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, dispatchVnet)

			dispatchSubnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "broken-subnet", Namespace: "default"},
				Spec:       osacv1alpha1.SubnetSpec{VirtualNetwork: "broken-vnet-uuid", IPv4CIDR: "10.3.1.0/24"},
			}
			Expect(k8sClient.Create(ctx, dispatchSubnet)).To(Succeed())
			DeferCleanup(deleteObjectWithClearedFinalizers, ctx, dispatchSubnet)

			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dispatchSubnet.Name, Namespace: dispatchSubnet.Namespace},
			}})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("handleProvisioning", func() {
		BeforeEach(func() {
			subnet.Status.Phase = osacv1alpha1.SubnetPhaseProgressing
		})

		It("should trigger provisioning when no job exists", func() {
			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "test-job-123",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Provisioning triggered",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("test-job-123"))
			Expect(latestJob.State).To(Equal(osacv1alpha1.JobStatePending))
		})

		It("should trigger new job when previous job failed", func() {
			subnet.Status.DesiredConfigVersion = testConfigVersionNew
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "old-failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "Previous job failed",
					ConfigVersion: testConfigVersionOld,
				},
			}

			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "new-job-456",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Retry provisioning",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("new-job-456"))
			Expect(latestJob.State).To(Equal(osacv1alpha1.JobStatePending))
		})

		It("should poll job status when job exists", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "existing-job-789",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
					Message:   "Job running",
				},
			}

			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateRunning,
					Message: "Still running",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob.State).To(Equal(osacv1alpha1.JobStateRunning))
		})

		It("should set phase to Ready when job succeeds", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "success-job-101",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
					Message:   "Job running",
				},
			}

			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Job succeeded",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
			Expect(subnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseReady))
		})

		It("should set phase to Failed when job fails", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "failed-job-202",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
					Message:   "Job running",
				},
			}

			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateFailed,
					Message: "Job failed",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
			Expect(subnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseFailed))
		})

		It("should set Ready=False condition with error message when job fails", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "failed-job-cond",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
				},
			}

			mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateFailed,
					Message: "Ansible traceback: role xyz failed",
				}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(subnet.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(osacv1alpha1.SubnetProvisioningFailed))
			Expect(cond.Message).To(ContainSubstring("Ansible traceback"))
		})

		It("should set Ready=True condition when job succeeds", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "success-job-cond",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
				},
			}

			mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID,
					State: osacv1alpha1.JobStateSucceeded,
				}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(subnet.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
		})

		It("should clear stale Ready=False condition on provisioning recovery", func() {
			subnet.Status.Conditions = []metav1.Condition{
				{
					Type:               osacv1alpha1.ConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             osacv1alpha1.SubnetProvisioningFailed,
					Message:            "previous failure",
					LastTransitionTime: metav1.Now(),
				},
			}
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "recovery-job",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
				},
			}

			mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID,
					State: osacv1alpha1.JobStateSucceeded,
				}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(subnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseReady))
			cond := apimeta.FindStatusCondition(subnet.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
			Expect(cond.Message).To(BeEmpty())
		})
	})

	Context("handleProvisioning dual-dispatch", func() {
		var dualPlan *dispatcher.DispatchPlan

		BeforeEach(func() {
			subnet.Status.Phase = osacv1alpha1.SubnetPhaseProgressing
			dualPlan = &dispatcher.DispatchPlan{
				Targets: []dispatcher.DispatchTarget{
					{Role: dispatcher.ManagerRoleFabric, Manager: networkmanager.Manager{Name: "netris"}},
					{Role: dispatcher.ManagerRoleK8s, Manager: networkmanager.Manager{Name: "cudn_net"}},
				},
			}
		})

		It("triggers both fabric and k8s jobs in parallel when no job history exists", func() {
			var seenAnnotations []string
			triggerCount := 0
			mockProvider.triggerProvisionFunc = func(_ context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				triggerCount++
				seenAnnotations = append(seenAnnotations, resource.GetAnnotations()[osacImplementationStrategyAnnotation])
				return &provisioning.ProvisionResult{JobID: fmt.Sprintf("job-%d", triggerCount), InitialState: osacv1alpha1.JobStatePending}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, subnet, dualPlan)
			Expect(err).NotTo(HaveOccurred())

			Expect(triggerCount).To(Equal(2))
			Expect(seenAnnotations).To(ConsistOf("netris", "cudn_net"))
			Expect(provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision, string(dispatcher.ManagerRoleFabric))).NotTo(BeNil())
			Expect(provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision, string(dispatcher.ManagerRoleK8s))).NotTo(BeNil())
		})

		It("sets Ready only once both targets' latest jobs have succeeded", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{JobID: "fabric-1", Type: osacv1alpha1.JobTypeProvision, Target: string(dispatcher.ManagerRoleFabric), State: osacv1alpha1.JobStateRunning, Timestamp: metav1.NewTime(time.Now().UTC())},
				{JobID: "k8s-1", Type: osacv1alpha1.JobTypeProvision, Target: string(dispatcher.ManagerRoleK8s), State: osacv1alpha1.JobStateRunning, Timestamp: metav1.NewTime(time.Now().UTC())},
			}
			mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: osacv1alpha1.JobStateSucceeded}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, subnet, dualPlan)
			Expect(err).NotTo(HaveOccurred())

			Expect(subnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseReady))
			cond := apimeta.FindStatusCondition(subnet.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("does not set Ready when only one target has succeeded and the other is still running", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{JobID: "fabric-1", Type: osacv1alpha1.JobTypeProvision, Target: string(dispatcher.ManagerRoleFabric), State: osacv1alpha1.JobStateSucceeded, Timestamp: metav1.NewTime(time.Now().UTC())},
				{JobID: "k8s-1", Type: osacv1alpha1.JobTypeProvision, Target: string(dispatcher.ManagerRoleK8s), State: osacv1alpha1.JobStateRunning, Timestamp: metav1.NewTime(time.Now().UTC())},
			}
			mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				// Only the k8s job is polled (fabric already succeeded, terminal — Skip).
				return provisioning.ProvisionStatus{JobID: jobID, State: osacv1alpha1.JobStateRunning}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, subnet, dualPlan)
			Expect(err).NotTo(HaveOccurred())

			Expect(subnet.Status.Phase).NotTo(Equal(osacv1alpha1.SubnetPhaseReady))
		})

		It("sets Failed with a target-scoped message when one target fails, without affecting the other's already-succeeded state", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{JobID: "fabric-1", Type: osacv1alpha1.JobTypeProvision, Target: string(dispatcher.ManagerRoleFabric), State: osacv1alpha1.JobStateSucceeded, Timestamp: metav1.NewTime(time.Now().UTC())},
				{JobID: "k8s-1", Type: osacv1alpha1.JobTypeProvision, Target: string(dispatcher.ManagerRoleK8s), State: osacv1alpha1.JobStateRunning, Timestamp: metav1.NewTime(time.Now().UTC())},
			}
			mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: osacv1alpha1.JobStateFailed, Message: "k8s overlay role failed"}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, subnet, dualPlan)
			Expect(err).NotTo(HaveOccurred())

			Expect(subnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseFailed))
			cond := apimeta.FindStatusCondition(subnet.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Message).To(ContainSubstring("k8s"))
			Expect(cond.Message).To(ContainSubstring("k8s overlay role failed"))

			latestFabricJob := provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision, string(dispatcher.ManagerRoleFabric))
			Expect(latestFabricJob.State).To(Equal(osacv1alpha1.JobStateSucceeded))
		})
	})

	Context("backoff on failure", func() {
		It("should backoff when latest job failed with matching ConfigVersion", func() {
			subnet.Status.DesiredConfigVersion = testConfigVersion
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "provision failed",
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(result.RequeueAfter).To(BeNumerically("<=", provisioning.BackoffMaxDelay))
		})

		It("should skip when config already applied", func() {
			subnet.Status.DesiredConfigVersion = testConfigVersion
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "succeeded-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateSucceeded,
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Duration(0)))
		})
	})

	Context("Job history management", func() {
		It("should limit job history to MaxJobHistory", func() {
			reconciler.MaxJobHistory = 3

			// Add 5 jobs
			for i := 1; i <= 5; i++ {
				newJob := osacv1alpha1.JobStatus{
					JobID:     "job-" + string(rune('0'+i)),
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC().Add(time.Duration(i) * time.Second)),
					State:     osacv1alpha1.JobStatePending,
					Message:   "Job triggered",
				}
				subnet.Status.ProvisioningJobs = provisioning.AppendJob(subnet.Status.ProvisioningJobs, newJob, reconciler.MaxJobHistory)
			}

			// Should only have last 3 jobs
			Expect(subnet.Status.ProvisioningJobs).To(HaveLen(3))
			Expect(subnet.Status.ProvisioningJobs[0].JobID).To(Equal("job-3"))
			Expect(subnet.Status.ProvisioningJobs[1].JobID).To(Equal("job-4"))
			Expect(subnet.Status.ProvisioningJobs[2].JobID).To(Equal("job-5"))
		})
	})

	Context("handleDeprovisioning", func() {
		It("should trigger deprovisioning when no deprovision job exists", func() {
			mockProvider.triggerDeprovisionFunc = func(ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action:                 provisioning.DeprovisionTriggered,
					JobID:                  "deprovision-job-303",
					BlockDeletionOnFailure: true,
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("deprovision-job-303"))
			Expect(latestJob.BlockDeletionOnFailure).To(BeTrue())
		})

		It("should skip deprovisioning when provider returns DeprovisionSkipped", func() {
			mockProvider.triggerDeprovisionFunc = func(ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionSkipped,
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
		})

		It("should poll deprovision job status", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "deprovision-running-404",
					Type:      osacv1alpha1.JobTypeDeprovision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
					Message:   "Deprovisioning in progress",
				},
			}

			mockProvider.getDeprovisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Deprovision succeeded",
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
		})

		It("should wait for backoff when deprovision fails with BlockDeletionOnFailure", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:                  "deprovision-failed-505",
					Type:                   osacv1alpha1.JobTypeDeprovision,
					Timestamp:              metav1.NewTime(time.Now().UTC()),
					State:                  osacv1alpha1.JobStateRunning,
					Message:                "Deprovisioning in progress",
					BlockDeletionOnFailure: true,
				},
			}

			mockProvider.getDeprovisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateFailed,
					Message: "Deprovision failed",
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically("<=", provisioning.BackoffBaseDelay))
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		})

		It("triggers both fabric and k8s deprovision jobs when both implementation-strategy annotations are persisted", func() {
			subnet.Annotations = map[string]string{
				osacImplementationStrategyAnnotation:    "netris",
				osacK8sImplementationStrategyAnnotation: "cudn_net",
			}

			var seenAnnotations []string
			triggerCount := 0
			mockProvider.triggerDeprovisionFunc = func(_ context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				triggerCount++
				seenAnnotations = append(seenAnnotations, resource.GetAnnotations()[osacImplementationStrategyAnnotation])
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionTriggered,
					JobID:  fmt.Sprintf("deprovision-job-%d", triggerCount),
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			Expect(triggerCount).To(Equal(2))
			Expect(seenAnnotations).To(ConsistOf("netris", "cudn_net"))
			Expect(provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision, string(dispatcher.ManagerRoleFabric))).NotTo(BeNil())
			Expect(provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision, string(dispatcher.ManagerRoleK8s))).NotTo(BeNil())
		})

		It("only removes the finalizer once both dual-dispatch deprovision jobs reach a terminal state", func() {
			subnet.Annotations = map[string]string{
				osacImplementationStrategyAnnotation:    "netris",
				osacK8sImplementationStrategyAnnotation: "cudn_net",
			}
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{JobID: "fabric-deprov-1", Type: osacv1alpha1.JobTypeDeprovision, Target: string(dispatcher.ManagerRoleFabric), State: osacv1alpha1.JobStateSucceeded, Timestamp: metav1.NewTime(time.Now().UTC())},
				{JobID: "k8s-deprov-1", Type: osacv1alpha1.JobTypeDeprovision, Target: string(dispatcher.ManagerRoleK8s), State: osacv1alpha1.JobStateRunning, Timestamp: metav1.NewTime(time.Now().UTC())},
			}
			mockProvider.getDeprovisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: osacv1alpha1.JobStateRunning}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			mockProvider.getDeprovisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: osacv1alpha1.JobStateSucceeded}, nil
			}
			result, err = reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
		})

		It("tags the fabric-only deprovision job with the fabric target, not an untargeted job, when the k8s manager was never dispatched to", func() {
			// Regression test: fabric-only Subnets on the dispatcher path (fabric
			// annotation set, no k8s annotation) must still go through the
			// "fabric"-tagged multi-target lifecycle, matching handleProvisioning's
			// job history. Falling back to the untargeted single-target lifecycle
			// here would blind the provider to the fabric target's existing job
			// history (see handleDeprovisioning's doc comment).
			subnet.Annotations = map[string]string{
				osacImplementationStrategyAnnotation: "netris",
			}

			triggerCount := 0
			mockProvider.triggerDeprovisionFunc = func(_ context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				triggerCount++
				return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "fabric-only-deprovision-job"}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			Expect(triggerCount).To(Equal(1))
			Expect(provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision, "fabric")).NotTo(BeNil())
			Expect(provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision, "")).To(BeNil())
			Expect(provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision, "k8s")).To(BeNil())
		})

		It("absorbs pre-existing untargeted deprovision history into the fabric target instead of triggering a duplicate", func() {
			// A Subnet that was already deprovisioning via the untargeted legacy path
			// (e.g. it started deprovisioning before this fix landed) must not have
			// that in-flight job orphaned and re-triggered once fabric-tagging kicks in.
			subnet.Annotations = map[string]string{
				osacImplementationStrategyAnnotation: "netris",
			}
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{JobID: "legacy-deprov-1", Type: osacv1alpha1.JobTypeDeprovision, Target: "", State: osacv1alpha1.JobStateRunning, Timestamp: metav1.NewTime(time.Now().UTC())},
			}
			mockProvider.getDeprovisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: osacv1alpha1.JobStateSucceeded}, nil
			}
			triggerCount := 0
			mockProvider.triggerDeprovisionFunc = func(_ context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				triggerCount++
				return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "should-not-be-used"}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))

			Expect(triggerCount).To(Equal(0))
			backfilled := provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision, "fabric")
			Expect(backfilled).NotTo(BeNil())
			Expect(backfilled.JobID).To(Equal("legacy-deprov-1"))
			Expect(provisioning.FindLatestJobByTypeAndTarget(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision, "")).To(BeNil())
		})
	})
})

// deleteObjectWithClearedFinalizers deletes obj, first clearing any finalizers it has
// so it doesn't remain stuck in Terminating state in the shared envtest API server.
// Intended for use with Ginkgo's DeferCleanup.
func deleteObjectWithClearedFinalizers(ctx context.Context, obj client.Object) {
	key := client.ObjectKeyFromObject(obj)
	if err := k8sClient.Get(ctx, key, obj); err != nil {
		if errors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
	}
	if len(obj.GetFinalizers()) > 0 {
		obj.SetFinalizers(nil)
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
	}
	Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
}

// mockSubnetProvider implements the ProvisioningProvider interface for Subnet testing
type mockSubnetProvider struct {
	triggerProvisionFunc     func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error)
	getProvisionStatusFunc   func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
	triggerDeprovisionFunc   func(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error)
	getDeprovisionStatusFunc func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
}

func (m *mockSubnetProvider) TriggerProvision(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
	if m.triggerProvisionFunc != nil {
		return m.triggerProvisionFunc(ctx, resource)
	}
	return &provisioning.ProvisionResult{
		JobID:        "mock-job-id",
		InitialState: osacv1alpha1.JobStatePending,
		Message:      "Provisioning job triggered",
	}, nil
}

func (m *mockSubnetProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getProvisionStatusFunc != nil {
		return m.getProvisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   osacv1alpha1.JobStateSucceeded,
		Message: "Job completed successfully",
	}, nil
}

func (m *mockSubnetProvider) TriggerDeprovision(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	if m.triggerDeprovisionFunc != nil {
		return m.triggerDeprovisionFunc(ctx, resource, provisionJobs)
	}
	return &provisioning.DeprovisionResult{
		Action:                 provisioning.DeprovisionTriggered,
		JobID:                  "mock-deprovision-job-id",
		BlockDeletionOnFailure: true,
	}, nil
}

func (m *mockSubnetProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getDeprovisionStatusFunc != nil {
		return m.getDeprovisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   osacv1alpha1.JobStateSucceeded,
		Message: "Deprovision completed successfully",
	}, nil
}

func (m *mockSubnetProvider) Name() string {
	return "mock-subnet-provider"
}
