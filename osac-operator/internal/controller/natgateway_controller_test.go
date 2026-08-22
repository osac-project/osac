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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/dispatcheradapter"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("NATGatewayReconciler", func() {
	var (
		reconciler   *NATGatewayReconciler
		mockProvider *mockNATGatewayProvider
		ctx          context.Context
		natgw        *osacv1alpha1.NATGateway
		vnet         *osacv1alpha1.VirtualNetwork
	)

	BeforeEach(func() {
		ctx = context.TODO()
		mockProvider = &mockNATGatewayProvider{}
		reconciler = &NATGatewayReconciler{
			Client:               k8sClient,
			APIReader:            k8sClient,
			Scheme:               k8sClient.Scheme(),
			NetworkingNamespace:  "default",
			ProvisioningProvider: mockProvider,
			StatusPollInterval:   1 * time.Second,
			MaxJobHistory:        10,
		}

		// Create VirtualNetwork fixture with ImplementationStrategy set
		vnet = &osacv1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-vnet-natgw",
				Namespace: "default",
				Labels: map[string]string{
					osacVirtualNetworkIDLabel: "test-vnet-natgw-uuid",
				},
			},
			Spec: osacv1alpha1.VirtualNetworkSpec{
				Region:                 "us-west-1",
				IPv4CIDR:               "10.0.0.0/16",
				NetworkClass:           "cudn-net",
				ImplementationStrategy: "cudn-net",
			},
		}
		Expect(k8sClient.Create(ctx, vnet)).To(Succeed())
		vnet.Status.Phase = osacv1alpha1.VirtualNetworkPhaseReady
		Expect(k8sClient.Status().Update(ctx, vnet)).To(Succeed())

		// Create ExternalIP fixture referenced by the NATGateway (SNAT source)
		eip := &osacv1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-eip-natgw",
				Namespace: "default",
				Labels: map[string]string{
					osacExternalIPIDLabel: "test-eip-uuid",
				},
			},
			Spec: osacv1alpha1.ExternalIPSpec{
				Pool: "test-pool-natgw-uuid",
			},
		}
		Expect(k8sClient.Create(ctx, eip)).To(Succeed())
		eip.Status.Address = "198.51.100.20"
		Expect(k8sClient.Status().Update(ctx, eip)).To(Succeed())

		// Create NATGateway fixture
		natgw = &osacv1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-natgateway",
				Namespace: "default",
			},
			Spec: osacv1alpha1.NATGatewaySpec{
				VirtualNetwork: "test-vnet-natgw-uuid",
				ExternalIP:     "test-eip-uuid",
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

		// Cleanup NATGateway if it exists
		natgwKey := types.NamespacedName{Name: natgw.Name, Namespace: natgw.Namespace}
		existingNATGW := &osacv1alpha1.NATGateway{}
		if err := k8sClient.Get(ctx, natgwKey, existingNATGW); err == nil {
			existingNATGW.Finalizers = nil
			_ = k8sClient.Update(ctx, existingNATGW)
			_ = k8sClient.Delete(ctx, existingNATGW)
		}

		// Cleanup ExternalIP fixture
		eipKey := types.NamespacedName{Name: "test-eip-natgw", Namespace: "default"}
		existingEIP := &osacv1alpha1.ExternalIP{}
		if err := k8sClient.Get(ctx, eipKey, existingEIP); err == nil {
			existingEIP.Finalizers = nil
			_ = k8sClient.Update(ctx, existingEIP)
			_ = k8sClient.Delete(ctx, existingEIP)
		}
	})

	Context("Reconcile", func() {
		It("should add finalizer on first reconcile", func() {
			Expect(k8sClient.Create(ctx, natgw)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natgw.Name,
					Namespace: natgw.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.NATGateway{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: natgw.Name, Namespace: natgw.Namespace}, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(osacNATGatewayFinalizer))
		})

		It("should set virtual-network-name, externalip-name and vn-ipv4-cidr annotations for the SNAT job", func() {
			Expect(k8sClient.Create(ctx, natgw)).To(Succeed())
			req := mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{Name: natgw.Name, Namespace: natgw.Namespace},
			}}
			// Reconcile a few times to settle the finalizer and annotation writes.
			for range 3 {
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
			}

			updated := &osacv1alpha1.NATGateway{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: natgw.Name, Namespace: natgw.Namespace}, updated)).To(Succeed())
			Expect(updated.Annotations).To(HaveKeyWithValue(osacVirtualNetworkNameAnnotation, "test-vnet-natgw"))
			Expect(updated.Annotations).To(HaveKeyWithValue(osacExternalIPNameAnnotation, "test-eip-natgw"))
			Expect(updated.Annotations).To(HaveKeyWithValue(osacVNIPv4CIDRAnnotation, "10.0.0.0/16"))
		})

		It("should set phase to Progressing on first reconcile", func() {
			Expect(k8sClient.Create(ctx, natgw)).To(Succeed())

			req := mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natgw.Name,
					Namespace: natgw.Namespace,
				},
			}}

			// First reconcile sets annotation and requeues
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Second reconcile persists the Progressing phase
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.NATGateway{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: natgw.Name, Namespace: natgw.Namespace}, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.NATGatewayPhaseProgressing))
		})

		It("should requeue when parent VirtualNetwork not found", func() {
			natgwNoParent := &osacv1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "natgw-no-parent",
					Namespace: "default",
				},
				Spec: osacv1alpha1.NATGatewaySpec{
					VirtualNetwork: "missing-vnet-uuid",
					ExternalIP:     "test-eip-uuid",
				},
			}
			Expect(k8sClient.Create(ctx, natgwNoParent)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natgwNoParent.Name,
					Namespace: natgwNoParent.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			_ = k8sClient.Delete(ctx, natgwNoParent)
		})

		It("should requeue when parent VirtualNetwork has no ImplementationStrategy", func() {
			vnetNoStrategy := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vnet-no-strategy-natgw",
					Namespace: "default",
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: "vnet-no-strategy-natgw-uuid",
					},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.0.0.0/16",
					NetworkClass: "some-class",
				},
			}
			Expect(k8sClient.Create(ctx, vnetNoStrategy)).To(Succeed())

			natgwNoStrategy := &osacv1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "natgw-no-strategy",
					Namespace: "default",
				},
				Spec: osacv1alpha1.NATGatewaySpec{
					VirtualNetwork: "vnet-no-strategy-natgw-uuid",
					ExternalIP:     "test-eip-uuid",
				},
			}
			Expect(k8sClient.Create(ctx, natgwNoStrategy)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      natgwNoStrategy.Name,
					Namespace: natgwNoStrategy.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			_ = k8sClient.Delete(ctx, natgwNoStrategy)
			_ = k8sClient.Delete(ctx, vnetNoStrategy)
		})

		It("should ignore NATGateway with unmanaged annotation", func() {
			unmanagedNATGW := &osacv1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unmanaged-natgw",
					Namespace: "default",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
				},
				Spec: osacv1alpha1.NATGatewaySpec{
					VirtualNetwork: "test-vnet-natgw-uuid",
					ExternalIP:     "test-eip-uuid",
				},
			}
			Expect(k8sClient.Create(ctx, unmanagedNATGW)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      unmanagedNATGW.Name,
					Namespace: unmanagedNATGW.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.NATGateway{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: unmanagedNATGW.Name, Namespace: unmanagedNATGW.Namespace}, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(BeEmpty())

			_ = k8sClient.Delete(ctx, unmanagedNATGW)
		})

		It("should still handle delete for unmanaged NATGateway with finalizer", func() {
			managedThenUnmanaged := &osacv1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-then-unmanaged-natgw",
					Namespace: "default",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
					Finalizers: []string{osacNATGatewayFinalizer},
				},
				Spec: osacv1alpha1.NATGatewaySpec{
					VirtualNetwork: "test-vnet-natgw-uuid",
					ExternalIP:     "test-eip-uuid",
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
				return errors.IsNotFound(k8sClient.Get(ctx, key, &osacv1alpha1.NATGateway{}))
			}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())
		})
	})

	Context("handleProvisioning", func() {
		BeforeEach(func() {
			natgw.Status.Phase = osacv1alpha1.NATGatewayPhaseProgressing
		})

		It("should set phase to Ready when job succeeds", func() {
			natgw.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			result, err := reconciler.handleProvisioning(ctx, natgw)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
			Expect(natgw.Status.Phase).To(Equal(osacv1alpha1.NATGatewayPhaseReady))
		})

		It("should set phase to Failed when job fails", func() {
			natgw.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			result, err := reconciler.handleProvisioning(ctx, natgw)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
			Expect(natgw.Status.Phase).To(Equal(osacv1alpha1.NATGatewayPhaseFailed))
		})
	})

	Context("provisioning condition updates", func() {
		BeforeEach(func() {
			natgw.Status.Phase = osacv1alpha1.NATGatewayPhaseProgressing
		})

		It("should set Ready=False condition with error message when job fails", func() {
			natgw.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, natgw)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(natgw.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(cond.Message).To(ContainSubstring("Ansible traceback"))
		})

		It("should set Ready=True condition when job succeeds", func() {
			natgw.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, natgw)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(natgw.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
		})

		It("should clear stale Ready=False condition on provisioning recovery", func() {
			natgw.Status.Conditions = []metav1.Condition{
				{
					Type:               osacv1alpha1.ConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             osacv1alpha1.ReasonProvisioningFailed,
					Message:            "previous failure",
					LastTransitionTime: metav1.Now(),
				},
			}
			natgw.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, natgw)
			Expect(err).NotTo(HaveOccurred())

			Expect(natgw.Status.Phase).To(Equal(osacv1alpha1.NATGatewayPhaseReady))
			cond := apimeta.FindStatusCondition(natgw.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
			Expect(cond.Message).To(BeEmpty())
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

			result, err := reconciler.handleDeprovisioning(ctx, natgw)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(natgw.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("deprovision-job-303"))
		})

		It("should skip deprovisioning when provider returns DeprovisionSkipped", func() {
			mockProvider.triggerDeprovisionFunc = func(ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionSkipped,
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, natgw)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
		})
	})

	Context("dispatcher path", func() {
		It("uses the resolved fabric manager name from the parent VirtualNetwork's NetworkClass", func() {
			testScheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
			Expect(scheme.AddToScheme(testScheme)).To(Succeed())

			dispatchVnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dispatch-vnet-natgw",
					Namespace: "test-ns",
					Labels:    map[string]string{osacVirtualNetworkIDLabel: "dispatch-vnet-uuid"},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:                 "us-west-1",
					IPv4CIDR:               "10.0.0.0/16",
					NetworkClass:           "nc-netris",
					ImplementationStrategy: "cudn_net",
				},
				Status: osacv1alpha1.VirtualNetworkStatus{Phase: osacv1alpha1.VirtualNetworkPhaseReady},
			}

			dispatchEIP := &osacv1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dispatch-eip-natgw",
					Namespace: "test-ns",
					Labels:    map[string]string{osacExternalIPIDLabel: "dispatch-eip-uuid"},
				},
				Spec:   osacv1alpha1.ExternalIPSpec{Pool: "some-pool"},
				Status: osacv1alpha1.ExternalIPStatus{Address: "198.51.100.1"},
			}

			dispatchNATGW := &osacv1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dispatch-natgw",
					Namespace: "test-ns",
				},
				Spec: osacv1alpha1.NATGatewaySpec{
					VirtualNetwork: "dispatch-vnet-uuid",
					ExternalIP:     "dispatch-eip-uuid",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(
					dispatchVnet, dispatchEIP, dispatchNATGW,
					newFabricManagerConfigMap("fm-netris", "test-ns", "netris"),
				).
				WithStatusSubresource(
					&osacv1alpha1.VirtualNetwork{},
					&osacv1alpha1.ExternalIP{},
					&osacv1alpha1.NATGateway{},
				).
				Build()

			// Status must be set after creation when using status subresource
			dispatchVnet.Status.Phase = osacv1alpha1.VirtualNetworkPhaseReady
			Expect(fakeClient.Status().Update(ctx, dispatchVnet)).To(Succeed())
			dispatchEIP.Status.Address = "198.51.100.1"
			Expect(fakeClient.Status().Update(ctx, dispatchEIP)).To(Succeed())

			disc, err := networkmanager.NewDiscovery(fakeClient, "test-ns")
			Expect(err).NotTo(HaveOccurred())
			resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
				[]*privatev1.NetworkClass{{Id: "nc-netris", FabricManager: ptr.To("netris")}}, &[]*privatev1.NetworkClass{},
			)), disc)

			dispatchMock := &mockNATGatewayProvider{}
			dispatchMock.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{JobID: "job-dispatch", InitialState: osacv1alpha1.JobStatePending}, nil
			}

			r := &NATGatewayReconciler{
				Client:               fakeClient,
				APIReader:            fakeClient,
				Scheme:               testScheme,
				NetworkingNamespace:  "test-ns",
				ProvisioningProvider: dispatchMock,
				StatusPollInterval:   1 * time.Second,
				MaxJobHistory:        10,
				Resolver:             resolver,
			}

			key := types.NamespacedName{Name: dispatchNATGW.Name, Namespace: dispatchNATGW.Namespace}
			// First reconcile adds finalizer, second sets annotation
			_, err = r.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.NATGateway{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal("netris"))
		})

		It("falls back to VNet implementation strategy when no dispatcher is configured", func() {
			testScheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
			Expect(scheme.AddToScheme(testScheme)).To(Succeed())

			fallbackVnet := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fallback-vnet-natgw",
					Namespace: "test-ns",
					Labels:    map[string]string{osacVirtualNetworkIDLabel: "fallback-vnet-uuid"},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:                 "us-west-1",
					IPv4CIDR:               "10.0.0.0/16",
					NetworkClass:           "some-class",
					ImplementationStrategy: "cudn_net",
				},
			}

			fallbackEIP := &osacv1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fallback-eip-natgw",
					Namespace: "test-ns",
					Labels:    map[string]string{osacExternalIPIDLabel: "fallback-eip-uuid"},
				},
				Spec: osacv1alpha1.ExternalIPSpec{Pool: "some-pool"},
			}

			fallbackNATGW := &osacv1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fallback-natgw",
					Namespace: "test-ns",
				},
				Spec: osacv1alpha1.NATGatewaySpec{
					VirtualNetwork: "fallback-vnet-uuid",
					ExternalIP:     "fallback-eip-uuid",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(fallbackVnet, fallbackEIP, fallbackNATGW).
				WithStatusSubresource(
					&osacv1alpha1.VirtualNetwork{},
					&osacv1alpha1.ExternalIP{},
					&osacv1alpha1.NATGateway{},
				).
				Build()

			fallbackVnet.Status.Phase = osacv1alpha1.VirtualNetworkPhaseReady
			Expect(fakeClient.Status().Update(ctx, fallbackVnet)).To(Succeed())
			fallbackEIP.Status.Address = "198.51.100.2"
			Expect(fakeClient.Status().Update(ctx, fallbackEIP)).To(Succeed())

			fallbackMock := &mockNATGatewayProvider{}
			fallbackMock.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{JobID: "job-fallback", InitialState: osacv1alpha1.JobStatePending}, nil
			}

			r := &NATGatewayReconciler{
				Client:               fakeClient,
				APIReader:            fakeClient,
				Scheme:               testScheme,
				NetworkingNamespace:  "test-ns",
				ProvisioningProvider: fallbackMock,
				StatusPollInterval:   1 * time.Second,
				MaxJobHistory:        10,
				// No Resolver — falls back to legacy strategy
			}

			key := types.NamespacedName{Name: fallbackNATGW.Name, Namespace: fallbackNATGW.Namespace}
			_, err := r.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.NATGateway{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal("cudn_net"))
		})
	})
})

// mockNATGatewayProvider implements the ProvisioningProvider interface for NATGateway testing
type mockNATGatewayProvider struct {
	triggerProvisionFunc     func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error)
	getProvisionStatusFunc   func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
	triggerDeprovisionFunc   func(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error)
	getDeprovisionStatusFunc func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
}

func (m *mockNATGatewayProvider) TriggerProvision(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
	if m.triggerProvisionFunc != nil {
		return m.triggerProvisionFunc(ctx, resource)
	}
	return &provisioning.ProvisionResult{
		JobID:        "mock-job-id",
		InitialState: osacv1alpha1.JobStatePending,
		Message:      "Provisioning job triggered",
	}, nil
}

func (m *mockNATGatewayProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getProvisionStatusFunc != nil {
		return m.getProvisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   osacv1alpha1.JobStateSucceeded,
		Message: "Job completed successfully",
	}, nil
}

func (m *mockNATGatewayProvider) TriggerDeprovision(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	if m.triggerDeprovisionFunc != nil {
		return m.triggerDeprovisionFunc(ctx, resource, provisionJobs)
	}
	return &provisioning.DeprovisionResult{
		Action:                 provisioning.DeprovisionTriggered,
		JobID:                  "mock-deprovision-job-id",
		BlockDeletionOnFailure: true,
	}, nil
}

func (m *mockNATGatewayProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getDeprovisionStatusFunc != nil {
		return m.getDeprovisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   osacv1alpha1.JobStateSucceeded,
		Message: "Deprovision completed successfully",
	}, nil
}

func (m *mockNATGatewayProvider) Name() string {
	return "mock-natgateway-provider"
}
