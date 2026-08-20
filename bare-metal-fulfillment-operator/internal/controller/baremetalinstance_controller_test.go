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
	"errors"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// mockInventoryClient implements inventory.Client for testing
type mockInventoryClient struct {
	findFreeHostFunc  func(ctx context.Context, matchExpressions map[string]string) (*inventory.Host, error)
	assignHostFunc    func(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, labels map[string]string) (*inventory.Host, error)
	unassignHostFunc  func(ctx context.Context, inventoryHostID string, labels []string) error
	getHostNICsFunc   func(ctx context.Context, inventoryHostID string) ([]inventory.HostNIC, error)
	getHostNICsCalled int
}

func (m *mockInventoryClient) FindFreeHost(ctx context.Context, matchExpressions map[string]string) (*inventory.Host, error) {
	if m.findFreeHostFunc != nil {
		return m.findFreeHostFunc(ctx, matchExpressions)
	}
	return nil, nil
}

func (m *mockInventoryClient) AssignHost(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, labels map[string]string) (*inventory.Host, error) {
	if m.assignHostFunc != nil {
		return m.assignHostFunc(ctx, inventoryHostID, bareMetalInstanceID, labels)
	}
	return nil, nil
}

func (m *mockInventoryClient) UnassignHost(ctx context.Context, inventoryHostID string, labels []string) error {
	if m.unassignHostFunc != nil {
		return m.unassignHostFunc(ctx, inventoryHostID, labels)
	}
	return nil
}

func (m *mockInventoryClient) GetHostNICs(ctx context.Context, inventoryHostID string) ([]inventory.HostNIC, error) {
	m.getHostNICsCalled++
	if m.getHostNICsFunc != nil {
		return m.getHostNICsFunc(ctx, inventoryHostID)
	}
	return nil, nil
}

// mockManagementClient implements management.Client for testing
type mockManagementClient struct {
	getPowerStateFunc     func(ctx context.Context, hostID string) (*management.PowerStatus, error)
	setPowerStateFunc     func(ctx context.Context, hostID string, target management.PowerState) error
	triggerRestartFunc    func(ctx context.Context, hostID string) error
	isRestartCompleteFunc func(ctx context.Context, hostID string) (bool, error)
}

func (m *mockManagementClient) GetPowerState(ctx context.Context, hostID string) (*management.PowerStatus, error) {
	if m.getPowerStateFunc != nil {
		return m.getPowerStateFunc(ctx, hostID)
	}
	return &management.PowerStatus{State: management.PowerOff}, nil
}

func (m *mockManagementClient) SetPowerState(ctx context.Context, hostID string, target management.PowerState) error {
	if m.setPowerStateFunc != nil {
		return m.setPowerStateFunc(ctx, hostID, target)
	}
	return nil
}

func (m *mockManagementClient) TriggerRestart(ctx context.Context, hostID string) error {
	if m.triggerRestartFunc != nil {
		return m.triggerRestartFunc(ctx, hostID)
	}
	return nil
}

func (m *mockManagementClient) IsRestartComplete(ctx context.Context, hostID string) (bool, error) {
	if m.isRestartCompleteFunc != nil {
		return m.isRestartCompleteFunc(ctx, hostID)
	}
	return true, nil
}

// mockProvisioningProvider implements provisioning.ProvisioningProvider for testing
type mockProvisioningProvider struct {
	triggerProvisionFunc     func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error)
	getProvisionStatusFunc   func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
	triggerDeprovisionFunc   func(ctx context.Context, resource client.Object, provisionJobs []opv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error)
	getDeprovisionStatusFunc func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
	nameFunc                 func() string
}

func (m *mockProvisioningProvider) TriggerProvision(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
	if m.triggerProvisionFunc != nil {
		return m.triggerProvisionFunc(ctx, resource)
	}
	return &provisioning.ProvisionResult{
		JobID:        "test-job-id",
		InitialState: opv1alpha1.JobStatePending,
		Message:      "Provision triggered",
	}, nil
}

func (m *mockProvisioningProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getProvisionStatusFunc != nil {
		return m.getProvisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   opv1alpha1.JobStateSucceeded,
		Message: "Provision completed",
	}, nil
}

func (m *mockProvisioningProvider) TriggerDeprovision(ctx context.Context, resource client.Object, provisionJobs []opv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	if m.triggerDeprovisionFunc != nil {
		return m.triggerDeprovisionFunc(ctx, resource, provisionJobs)
	}
	return &provisioning.DeprovisionResult{
		Action:                 provisioning.DeprovisionTriggered,
		JobID:                  "test-deprovision-job-id",
		BlockDeletionOnFailure: false,
	}, nil
}

func (m *mockProvisioningProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getDeprovisionStatusFunc != nil {
		return m.getDeprovisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   opv1alpha1.JobStateSucceeded,
		Message: "Deprovision completed",
	}, nil
}

func (m *mockProvisioningProvider) Name() string {
	if m.nameFunc != nil {
		return m.nameFunc()
	}
	return "mock-provider"
}

var _ = Describe("BareMetalInstance Controller", func() {
	var (
		ctx               context.Context
		reconciler        *BareMetalInstanceReconciler
		mockK8sClient     *mockClient
		mockInvClient     *mockInventoryClient
		mockMgmtClient    *mockManagementClient
		mockProvProvider  *mockProvisioningProvider
		bareMetalInstance *v1alpha1.BareMetalInstance

		namespace string
		hostType  string
		hostClass string
	)

	BeforeEach(func() {
		ctx = context.Background()
		mockK8sClient = &mockClient{Client: k8sClient}
		mockInvClient = &mockInventoryClient{}
		mockMgmtClient = &mockManagementClient{}
		mockProvProvider = nil

		namespace = "default"
		hostType = "fc430"
		hostClass = "external-mgmt"

		reconciler = NewBareMetalInstanceReconciler(
			mockK8sClient,
			k8sClient.Scheme(),
			mockInvClient,
			mockMgmtClient,
			mockProvProvider,
			0,
			0,
			0,
			0,
		)
	})

	Describe("NewBareMetalInstanceReconciler", func() {
		Context("when interval duration parameters are zero or negative", func() {
			BeforeEach(func() {
				reconciler = NewBareMetalInstanceReconciler(
					mockK8sClient,
					k8sClient.Scheme(),
					mockInvClient,
					mockMgmtClient,
					mockProvProvider,
					-1*time.Second,
					0,
					-5*time.Second,
					0,
				)
			})

			It("should set them to the default values", func() {
				Expect(reconciler.NoFreeHostsPollIntervalDuration).To(Equal(DefaultNoFreeHostsPollIntervalDuration))
				Expect(reconciler.TryLockFailPollIntervalDuration).To(Equal(DefaultTryLockFailPollIntervalDuration))
				Expect(reconciler.ManagementRecheckIntervalDuration).To(Equal(DefaultManagementRecheckIntervalDuration))
				Expect(reconciler.ProvisionPollIntervalDuration).To(Equal(DefaultProvisionPollIntervalDuration))
			})
		})

		Context("when interval duration parameters are positive", func() {
			It("should use the provided values", func() {
				customReconciler := NewBareMetalInstanceReconciler(
					mockK8sClient,
					k8sClient.Scheme(),
					mockInvClient,
					mockMgmtClient,
					mockProvProvider,
					45*time.Second,
					2*time.Second,
					15*time.Second,
					60*time.Second,
				)

				Expect(customReconciler.NoFreeHostsPollIntervalDuration).To(Equal(45 * time.Second))
				Expect(customReconciler.TryLockFailPollIntervalDuration).To(Equal(2 * time.Second))
				Expect(customReconciler.ManagementRecheckIntervalDuration).To(Equal(15 * time.Second))
				Expect(customReconciler.ProvisionPollIntervalDuration).To(Equal(60 * time.Second))
			})
		})
	})

	Describe("reconcileInventory", func() {
		BeforeEach(func() {
			bareMetalInstance = &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "reconcileInventory-bareMetalInstance",
					Namespace: namespace,
					UID:       "test-uid-123",
					Finalizers: []string{
						BareMetalInstanceInventoryFinalizer,
					},
				},
				Spec: v1alpha1.BareMetalInstanceSpec{
					HostType: hostType,
				},
			}
		})

		Context("when the finalizer is missing", func() {
			BeforeEach(func() {
				Expect(controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceInventoryFinalizer)).To(BeTrue())
			})

			It("should add the finalizer and requeue", func() {
				mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					hl := obj.(*v1alpha1.BareMetalInstance)
					Expect(controllerutil.ContainsFinalizer(hl, BareMetalInstanceInventoryFinalizer)).To(BeTrue())
					return nil
				}

				result, err := reconciler.reconcileInventory(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseAllocating))
			})
		})

		Context("when no free hosts are available", func() {
			BeforeEach(func() {
				mockInvClient.findFreeHostFunc = func(ctx context.Context, matchExpressions map[string]string) (*inventory.Host, error) {
					return nil, nil
				}
			})

			It("should set phase to Failed and requeue after poll interval", func() {
				result, err := reconciler.reconcileInventory(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(DefaultNoFreeHostsPollIntervalDuration))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
			})
		})

		Context("when a free host is found", func() {
			BeforeEach(func() {
				mockInvClient.findFreeHostFunc = func(ctx context.Context, matchExpressions map[string]string) (*inventory.Host, error) {
					Expect(matchExpressions["hostType"]).To(Equal(hostType))
					Expect(matchExpressions["managedBy"]).To(Equal(shared.OsacDefaultManagedByValue))
					Expect(matchExpressions["provisionState"]).To(Equal(shared.OsacDefaultProvisionStateValue))
					return &inventory.Host{
						InventoryHostID: "host-abc-123",
						HostClass:       hostClass,
						ManagedBy:       shared.OsacDefaultManagedByValue,
						ProvisionState:  shared.OsacDefaultProvisionStateValue,
					}, nil
				}
			})

			It("should update ExternalHostID and requeue", func() {
				updateCalled := false
				mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					updateCalled = true
					hl := obj.(*v1alpha1.BareMetalInstance)
					Expect(hl.Spec.ExternalHostID).To(Equal("host-abc-123"))
					return nil
				}

				result, err := reconciler.reconcileInventory(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(updateCalled).To(BeTrue())
			})
		})

		Context("when selector overrides default managedBy and provisionState", func() {
			BeforeEach(func() {
				bareMetalInstance.Spec.Selector = v1alpha1.HostSelectorSpec{
					HostSelector: map[string]string{
						"managedBy":      "agent",
						"provisionState": "active",
					},
				}
				mockInvClient.findFreeHostFunc = func(ctx context.Context, matchExpressions map[string]string) (*inventory.Host, error) {
					Expect(matchExpressions["managedBy"]).To(Equal("agent"))
					Expect(matchExpressions["provisionState"]).To(Equal("active"))
					return nil, nil
				}
			})

			It("should forward the user-specified selector values to FindFreeHost", func() {
				_, err := reconciler.reconcileInventory(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("when selector contains empty string values for managedBy and provisionState", func() {
			BeforeEach(func() {
				bareMetalInstance.Spec.Selector = v1alpha1.HostSelectorSpec{
					HostSelector: map[string]string{
						"managedBy":      "",
						"provisionState": "",
					},
				}
				mockInvClient.findFreeHostFunc = func(ctx context.Context, matchExpressions map[string]string) (*inventory.Host, error) {
					Expect(matchExpressions["managedBy"]).To(Equal(shared.OsacDefaultManagedByValue))
					Expect(matchExpressions["provisionState"]).To(Equal(shared.OsacDefaultProvisionStateValue))
					return nil, nil
				}
			})

			It("should apply defaults when selector values are empty strings", func() {
				_, err := reconciler.reconcileInventory(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("when assigning an ExternalHostID", func() {
			BeforeEach(func() {
				bareMetalInstance.Spec.ExternalHostID = "host-xyz-456"
				mockInvClient.assignHostFunc = func(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, labels map[string]string) (*inventory.Host, error) {
					Expect(inventoryHostID).To(Equal("host-xyz-456"))
					Expect(bareMetalInstanceID).To(Equal("test-uid-123"))
					return &inventory.Host{
						InventoryHostID: inventoryHostID,
						HostClass:       hostClass,
					}, nil
				}
			})

			It("should assign the host and update HostClass", func() {
				mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					hl := obj.(*v1alpha1.BareMetalInstance)
					Expect(hl.Spec.HostClass).To(Equal(hostClass))
					return nil
				}

				result, err := reconciler.reconcileInventory(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))
			})
		})

		Context("when the host is already assigned to another BareMetalInstance", func() {
			BeforeEach(func() {
				bareMetalInstance.Spec.ExternalHostID = "host-taken-789"
				mockInvClient.assignHostFunc = func(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, labels map[string]string) (*inventory.Host, error) {
					return nil, nil
				}
			})

			It("should unset ExternalHostID and requeue", func() {
				mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					hl := obj.(*v1alpha1.BareMetalInstance)
					Expect(hl.Spec.ExternalHostID).To(Equal(""))
					return nil
				}

				result, err := reconciler.reconcileInventory(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
			})
		})
	})

	Describe("reconcileManagement", func() {
		BeforeEach(func() {
			ctx = context.Background()
			bareMetalInstance = &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "reconcileManagement-bareMetalInstance",
					Namespace: namespace,
					UID:       "test-uid-123",
					Finalizers: []string{
						BareMetalInstanceInventoryFinalizer,
						BareMetalInstanceManagementFinalizer,
					},
				},
				Spec: v1alpha1.BareMetalInstanceSpec{
					HostType: hostType,
				},
			}
		})

		Context("when the finalizer is missing", func() {
			BeforeEach(func() {
				Expect(controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceManagementFinalizer)).To(BeTrue())
			})

			It("should add the finalizer", func() {
				mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					hl := obj.(*v1alpha1.BareMetalInstance)
					Expect(controllerutil.ContainsFinalizer(hl, BareMetalInstanceManagementFinalizer)).To(BeTrue())
					return nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))
			})
		})

		Context("when RunStrategy is unspecified", func() {
			BeforeEach(func() {
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyUnspecified
			})

			It("should skip reconcilePower", func() {
				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOff}, nil
				}

				setPowerStateCalled := false
				mockMgmtClient.setPowerStateFunc = func(ctx context.Context, hostID string, target management.PowerState) error {
					setPowerStateCalled = true
					return nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(setPowerStateCalled).To(BeFalse())

				Expect(bareMetalInstance.Status.RunStrategy).To(Equal(v1alpha1.RunStrategyHalted))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonPowerOff))
			})
		})

		Context("when RunStrategy is Halted", func() {
			BeforeEach(func() {
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyHalted
			})

			It("should update status", func() {
				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOff}, nil
				}

				setPowerStateCalled := false
				mockMgmtClient.setPowerStateFunc = func(ctx context.Context, hostID string, target management.PowerState) error {
					setPowerStateCalled = true
					return nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(setPowerStateCalled).To(BeFalse())

				Expect(bareMetalInstance.Status.RunStrategy).To(Equal(v1alpha1.RunStrategyHalted))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonPowerOff))
			})
		})

		Context("when power is not yet converged", func() {
			It("should requeue to be turned on", func() {
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways

				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOff}, nil
				}

				setPowerStateCalled := false
				mockMgmtClient.setPowerStateFunc = func(ctx context.Context, hostID string, target management.PowerState) error {
					setPowerStateCalled = true
					return nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))
				Expect(setPowerStateCalled).To(BeTrue())
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))
			})

			It("should requeue to be turned off", func() {
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyHalted

				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOn}, nil
				}

				setPowerStateCalled := false
				mockMgmtClient.setPowerStateFunc = func(ctx context.Context, hostID string, target management.PowerState) error {
					setPowerStateCalled = true
					return nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))
				Expect(setPowerStateCalled).To(BeTrue())
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))
			})
		})

		Context("when power state appears converged but was recently transitioning (OSAC-2467)", func() {
			It("should requeue to verify stale read after rapid stop/start", func() {
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways

				// Simulate state from previous reconciliation: PowerSynced was False/Progressing
				// (set during the Halted reconciliation that initiated a power-off)
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionPowerSynced,
					metav1.ConditionFalse,
					v1alpha1.HostConditionReasonProgressing,
					"node power state is transitioning",
				)

				// Management backend returns stale state: reports PowerOn even though
				// a power-off was just initiated by a previous reconciliation
				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOn}, nil
				}

				setPowerStateCalled := false
				mockMgmtClient.setPowerStateFunc = func(ctx context.Context, hostID string, target management.PowerState) error {
					setPowerStateCalled = true
					return nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				// Should NOT call SetPowerState (stale read says already on, matches Always)
				Expect(setPowerStateCalled).To(BeFalse())
				// Must requeue to verify the stale read
				Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))
			})

			It("should reach Ready when PowerSynced was previously True", func() {
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways

				// Simulate verified convergence from previous reconciliation
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionPowerSynced,
					metav1.ConditionTrue,
					v1alpha1.HostConditionReasonPowerOn,
					"",
				)

				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOn}, nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
			})
		})

		Context("when reconcilePower detects transitioning", func() {
			It("should requeue even if second power read appears converged", func() {
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways

				callCount := 0
				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					callCount++
					if callCount == 1 {
						// First read: transitioning (spec.Online=false, status.PoweredOn=true)
						return &management.PowerStatus{State: management.PowerOn, IsTransitioning: true}, nil
					}
					// Second read: Metal3 caught up fast, now shows converged
					return &management.PowerStatus{State: management.PowerOn}, nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))
			})
		})

		Context("rapid stop/start lifecycle (OSAC-2467 regression)", func() {
			It("should not get stuck when runStrategy changes Always→Halted→Always", func() {
				// Step 1: Start in steady state — RunStrategy=Always, power on, PowerSynced=True
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionPowerSynced,
					metav1.ConditionTrue,
					v1alpha1.HostConditionReasonPowerOn,
					"",
				)

				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOn}, nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))

				// Step 2: User sets RunStrategy=Halted (stop). Power is still on.
				// reconcilePower should call SetPowerState(off) and signal requeue.
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyHalted
				setPowerTarget := management.PowerState("")
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, target management.PowerState) error {
					setPowerTarget = target
					return nil
				}
				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOn}, nil
				}

				result, err = reconciler.reconcileManagement(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
				Expect(setPowerTarget).To(Equal(management.PowerOff))
				Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))

				// After syncBareMetalInstanceStatus: spec=Halted, status=Always (power still on),
				// so PowerSynced=False/Progressing
				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionFalse))

				// Step 3: Before power-off completes, user switches back to Always (start).
				// Management backend still reports PowerOn (stale — the power-off hasn't taken effect).
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways
				mockMgmtClient.setPowerStateFunc = nil // Should NOT be called — stale read says already on

				result, err = reconciler.reconcileManagement(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
				// The stale-read guard detects PowerSynced was False and requeues to verify
				Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))

				// Step 4: On the verification requeue, management backend has settled.
				// PowerSynced is now True (set by syncBareMetalInstanceStatus in step 3
				// because spec=Always matches status=Always), so the guard passes.
				result, err = reconciler.reconcileManagement(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
			})

			It("should recover when power-off completes before start is requested", func() {
				// Start with Halted in progress — power-off was initiated but hasn't completed
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyHalted
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionPowerSynced,
					metav1.ConditionFalse,
					v1alpha1.HostConditionReasonProgressing,
					"waiting for node power state to converge",
				)

				// Power-off completes: backend now reports PowerOff
				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOff}, nil
				}

				result, err := reconciler.reconcileManagement(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
				// Stale-read guard fires because PowerSynced was False
				Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))

				// Verification requeue: power is still off, matches Halted
				result, err = reconciler.reconcileManagement(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))

				// User changes to Always: should power on and eventually reach Ready
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways
				setPowerTarget := management.PowerState("")
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, target management.PowerState) error {
					setPowerTarget = target
					return nil
				}

				result, err = reconciler.reconcileManagement(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
				Expect(setPowerTarget).To(Equal(management.PowerOn))
				Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))

				// Power on completes
				mockMgmtClient.getPowerStateFunc = func(ctx context.Context, hostID string) (*management.PowerStatus, error) {
					return &management.PowerStatus{State: management.PowerOn}, nil
				}

				// Stale-read guard fires (PowerSynced was False from the power-on transition)
				result, err = reconciler.reconcileManagement(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))

				// Verification requeue: reaches Ready
				result, err = reconciler.reconcileManagement(ctx, bareMetalInstance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
			})
		})
	})

	Describe("reconcileProvisioning", func() {
		BeforeEach(func() {
			ctx = context.Background()
			mockProvProvider = &mockProvisioningProvider{}
			reconciler.ProvisioningProvider = mockProvProvider
			bareMetalInstance = &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "reconcileProvisioning-bareMetalInstance",
					Namespace: namespace,
					UID:       "test-uid-123",
					Finalizers: []string{
						BareMetalInstanceInventoryFinalizer,
						BareMetalInstanceManagementFinalizer,
					},
				},
				Spec: v1alpha1.BareMetalInstanceSpec{
					HostType:       hostType,
					ExternalHostID: "host-123",
					HostClass:      hostClass,
					TemplateID:     "image-provision",
				},
			}
		})

		Context("when a successful provision job exists", func() {
			BeforeEach(func() {
				bareMetalInstance.Status.ProvisioningJobs = []opv1alpha1.JobStatus{
					{
						JobID:     "123",
						Type:      opv1alpha1.JobTypeProvision,
						State:     opv1alpha1.JobStateSucceeded,
						Message:   "successful",
						Timestamp: metav1.Now(),
					},
				}
			})

			It("should not re-trigger provisioning", func() {
				triggerCalled := false
				mockProvProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
					triggerCalled = true
					return nil, nil
				}

				result, err := reconciler.reconcileProvisioning(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(triggerCalled).To(BeFalse())
				Expect(bareMetalInstance.Status.ProvisioningJobs).To(HaveLen(1))
			})
		})
	})

	Describe("syncBareMetalInstanceStatus", func() {
		var log logr.Logger

		BeforeEach(func() {
			log = logr.Discard()
			bareMetalInstance = &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "syncBareMetalInstanceStatus-bareMetalInstance",
					Namespace: namespace,
				},
				Spec: v1alpha1.BareMetalInstanceSpec{
					ExternalHostID: "host-123",
					HostClass:      hostClass,
				},
			}
		})

		Context("when there is an error", func() {
			It("should set PowerSynced to False", func() {
				reconciler.syncBareMetalInstanceStatus(bareMetalInstance, nil, errors.New("ironic connection failed"), log)

				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonIronicAPIFailure))
				Expect(condition.Message).To(Equal("failed to sync power status"))
			})
		})

		Context("when node is on", func() {
			It("should set PowerSynced to True", func() {
				powerStatus := &management.PowerStatus{State: management.PowerOn}
				reconciler.syncBareMetalInstanceStatus(bareMetalInstance, powerStatus, nil, log)

				Expect(bareMetalInstance.Status.RunStrategy).To(Equal(v1alpha1.RunStrategyAlways))

				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonPowerOn))
			})
		})

		Context("when node is off", func() {
			It("should set PowerSynced to True", func() {
				powerStatus := &management.PowerStatus{State: management.PowerOff}
				reconciler.syncBareMetalInstanceStatus(bareMetalInstance, powerStatus, nil, log)

				Expect(bareMetalInstance.Status.RunStrategy).To(Equal(v1alpha1.RunStrategyHalted))

				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonPowerOff))
			})
		})

		Context("when power state does not match desired", func() {
			BeforeEach(func() {
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways
			})

			It("should set PowerSynced to False", func() {
				powerStatus := &management.PowerStatus{State: management.PowerOff}
				reconciler.syncBareMetalInstanceStatus(bareMetalInstance, powerStatus, nil, log)

				Expect(bareMetalInstance.Status.RunStrategy).To(Equal(v1alpha1.RunStrategyHalted))
				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
			})
		})

		Context("when node is transitioning", func() {
			It("should set PowerSynced to False", func() {
				powerStatus := &management.PowerStatus{State: management.PowerOff, IsTransitioning: true}
				reconciler.syncBareMetalInstanceStatus(bareMetalInstance, powerStatus, nil, log)

				Expect(bareMetalInstance.Status.RunStrategy).To(Equal(v1alpha1.RunStrategyHalted))
				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
				Expect(condition.Message).To(Equal("node power state is transitioning"))
			})
		})

		Context("when powerStatus is nil and no error", func() {
			It("should not modify status", func() {
				reconciler.syncBareMetalInstanceStatus(bareMetalInstance, nil, nil, log)

				Expect(bareMetalInstance.Status.RunStrategy).To(Equal(v1alpha1.RunStrategyUnspecified))
				Expect(bareMetalInstance.Status.Conditions).To(BeEmpty())
			})
		})
	})

	Describe("reconcilePower", func() {
		var log logr.Logger
		var powerStatus *management.PowerStatus

		BeforeEach(func() {
			log = logr.Discard()
			bareMetalInstance = &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "reconcilePower-bareMetalInstance",
					Namespace: namespace,
				},
				Spec: v1alpha1.BareMetalInstanceSpec{
					ExternalHostID: "host-123",
				},
			}
		})

		Context("when its currently off and should be on", func() {
			BeforeEach(func() {
				powerStatus = &management.PowerStatus{State: management.PowerOff}
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways
			})

			It("should power on and signal requeue", func() {
				var calledTarget management.PowerState
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, target management.PowerState) error {
					calledTarget = target
					return nil
				}

				needsRequeue, err := reconciler.reconcilePower(ctx, bareMetalInstance, powerStatus, log)
				Expect(err).NotTo(HaveOccurred())
				Expect(calledTarget).To(Equal(management.PowerOn))
				Expect(needsRequeue).To(BeTrue())
			})
		})

		Context("when its currently on and should be off", func() {
			BeforeEach(func() {
				powerStatus = &management.PowerStatus{State: management.PowerOn}
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyHalted
			})

			It("should power off and signal requeue", func() {
				var calledTarget management.PowerState
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, target management.PowerState) error {
					calledTarget = target
					return nil
				}

				needsRequeue, err := reconciler.reconcilePower(ctx, bareMetalInstance, powerStatus, log)
				Expect(err).NotTo(HaveOccurred())
				Expect(calledTarget).To(Equal(management.PowerOff))
				Expect(needsRequeue).To(BeTrue())
			})
		})

		Context("when power state already matches desired on", func() {
			BeforeEach(func() {
				powerStatus = &management.PowerStatus{State: management.PowerOn}
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways
			})

			It("should not call SetPowerState and not signal requeue", func() {
				called := false
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, _ management.PowerState) error {
					called = true
					return nil
				}

				needsRequeue, err := reconciler.reconcilePower(ctx, bareMetalInstance, powerStatus, log)
				Expect(err).NotTo(HaveOccurred())
				Expect(called).To(BeFalse())
				Expect(needsRequeue).To(BeFalse())
			})
		})

		Context("when power state already matches desired off", func() {
			BeforeEach(func() {
				powerStatus = &management.PowerStatus{State: management.PowerOff}
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyHalted
			})

			It("should not call SetPowerState and not signal requeue", func() {
				called := false
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, _ management.PowerState) error {
					called = true
					return nil
				}

				needsRequeue, err := reconciler.reconcilePower(ctx, bareMetalInstance, powerStatus, log)
				Expect(err).NotTo(HaveOccurred())
				Expect(called).To(BeFalse())
				Expect(needsRequeue).To(BeFalse())
			})
		})

		Context("when node is transitioning", func() {
			BeforeEach(func() {
				powerStatus = &management.PowerStatus{State: management.PowerOff, IsTransitioning: true}
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways
			})

			It("should skip SetPowerState and signal requeue", func() {
				called := false
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, _ management.PowerState) error {
					called = true
					return nil
				}

				needsRequeue, err := reconciler.reconcilePower(ctx, bareMetalInstance, powerStatus, log)
				Expect(err).NotTo(HaveOccurred())
				Expect(called).To(BeFalse())
				Expect(needsRequeue).To(BeTrue())
			})
		})

		Context("when SetPowerState returns ErrTransitioning", func() {
			BeforeEach(func() {
				powerStatus = &management.PowerStatus{State: management.PowerOff}
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways
			})

			It("should not return error and signal requeue", func() {
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, _ management.PowerState) error {
					return management.ErrTransitioning
				}

				needsRequeue, err := reconciler.reconcilePower(ctx, bareMetalInstance, powerStatus, log)
				Expect(err).NotTo(HaveOccurred())
				Expect(needsRequeue).To(BeTrue())
			})
		})

		Context("when setting the power on fails", func() {
			BeforeEach(func() {
				powerStatus = &management.PowerStatus{State: management.PowerOff}
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyAlways
			})

			It("should return error", func() {
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, _ management.PowerState) error {
					return errors.New("ironic API error")
				}

				_, err := reconciler.reconcilePower(ctx, bareMetalInstance, powerStatus, log)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ironic API error"))
			})
		})

		Context("when setting the power off fails", func() {
			BeforeEach(func() {
				powerStatus = &management.PowerStatus{State: management.PowerOn}
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyHalted
			})

			It("should return error", func() {
				mockMgmtClient.setPowerStateFunc = func(_ context.Context, _ string, _ management.PowerState) error {
					return errors.New("ironic API error")
				}

				_, err := reconciler.reconcilePower(ctx, bareMetalInstance, powerStatus, log)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ironic API error"))
			})
		})
	})

	Describe("handleDeletion", func() {
		BeforeEach(func() {
			bareMetalInstance = &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "handleDeletion-bareMetalInstance",
					Namespace:         namespace,
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
				Spec: v1alpha1.BareMetalInstanceSpec{
					ExternalHostID: "host-to-delete",
				},
			}
		})

		Context("when inventory finalizer is present", func() {
			BeforeEach(func() {
				controllerutil.AddFinalizer(bareMetalInstance, BareMetalInstanceInventoryFinalizer)
			})

			It("should unassign the host and remove finalizer", func() {
				unassignCalled := false
				mockInvClient.unassignHostFunc = func(ctx context.Context, inventoryHostID string, labels []string) error {
					unassignCalled = true
					Expect(inventoryHostID).To(Equal("host-to-delete"))
					return nil
				}

				mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					hl := obj.(*v1alpha1.BareMetalInstance)
					Expect(controllerutil.ContainsFinalizer(hl, BareMetalInstanceInventoryFinalizer)).To(BeFalse())
					return nil
				}

				result, err := reconciler.handleDeletion(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(unassignCalled).To(BeTrue())
			})

			Context("when ExternalHostID is empty", func() {
				BeforeEach(func() {
					bareMetalInstance.Spec.ExternalHostID = ""
				})

				It("should remove finalizer without unassigning", func() {
					unassignCalled := false
					mockInvClient.unassignHostFunc = func(ctx context.Context, inventoryHostID string, labels []string) error {
						unassignCalled = true
						return nil
					}

					mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						return nil
					}

					result, err := reconciler.handleDeletion(ctx, bareMetalInstance)

					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(ctrl.Result{}))
					Expect(unassignCalled).To(BeFalse())
				})
			})

			Context("when management finalizer is present", func() {
				BeforeEach(func() {
					controllerutil.AddFinalizer(bareMetalInstance, BareMetalInstanceManagementFinalizer)
					mockProvProvider = &mockProvisioningProvider{}
					reconciler.ProvisioningProvider = mockProvProvider
				})

				Context("when TemplateID is empty", func() {
					BeforeEach(func() {
						bareMetalInstance.Spec.TemplateID = ""
					})

					It("should skip deprovision and remove management finalizer", func() {
						mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
							hl := obj.(*v1alpha1.BareMetalInstance)
							Expect(controllerutil.ContainsFinalizer(hl, BareMetalInstanceManagementFinalizer)).To(BeFalse())
							return nil
						}

						result, err := reconciler.handleDeletion(ctx, bareMetalInstance)

						Expect(err).NotTo(HaveOccurred())
						Expect(result).To(Equal(ctrl.Result{}))
					})
				})

				Context("when TemplateID is noop", func() {
					BeforeEach(func() {
						bareMetalInstance.Spec.TemplateID = shared.OsacNoopTemplate
					})

					It("should skip deprovision and remove management finalizer", func() {
						mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
							hl := obj.(*v1alpha1.BareMetalInstance)
							Expect(controllerutil.ContainsFinalizer(hl, BareMetalInstanceManagementFinalizer)).To(BeFalse())
							return nil
						}

						result, err := reconciler.handleDeletion(ctx, bareMetalInstance)

						Expect(err).NotTo(HaveOccurred())
						Expect(result).To(Equal(ctrl.Result{}))
					})
				})

				It("should trigger deprovision and requeue", func() {
					bareMetalInstance.Spec.TemplateID = "os-provision"

					mockK8sClient.statusUpdateFunc = func(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						return nil
					}

					result, err := reconciler.handleDeletion(ctx, bareMetalInstance)

					Expect(err).NotTo(HaveOccurred())
					Expect(result.RequeueAfter).To(Equal(DefaultProvisionPollIntervalDuration))
				})

				Context("when ProvisioningProvider is nil for a non-noop template", func() {
					BeforeEach(func() {
						reconciler.ProvisioningProvider = nil
						bareMetalInstance.Spec.TemplateID = "os-provision"
					})

					It("should leave the management finalizer stuck", func() {
						// this is so that every provision action is paired with a deprovision action
						updateCalled := false
						mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
							updateCalled = true
							bmi := obj.(*v1alpha1.BareMetalInstance)
							Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceManagementFinalizer)).To(BeFalse())
							return nil
						}

						result, err := reconciler.handleDeletion(ctx, bareMetalInstance)

						Expect(err).NotTo(HaveOccurred())
						Expect(result).To(Equal(ctrl.Result{}))
						Expect(bareMetalInstance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
						Expect(updateCalled).To(BeFalse())
						Expect(controllerutil.ContainsFinalizer(bareMetalInstance, BareMetalInstanceManagementFinalizer)).To(BeTrue())
					})
				})
			})
		})

		Context("when inventory finalizer is not present", func() {
			It("should return immediately", func() {
				result, err := reconciler.handleDeletion(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
			})
		})
	})

	Describe("reconcileRestartTrigger", func() {
		var bareMetalInstance *v1alpha1.BareMetalInstance
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
			bareMetalInstance = &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "restart-test-instance",
					Namespace: namespace,
					UID:       "test-uid-restart",
				},
				Spec: v1alpha1.BareMetalInstanceSpec{
					ExternalHostID: "test-host-123",
					RestartTrigger: 1,
				},
				Status: v1alpha1.BareMetalInstanceStatus{
					RestartTrigger: 0, // Different from spec, should trigger restart
				},
			}
		})

		Context("when restart trigger matches status", func() {
			BeforeEach(func() {
				bareMetalInstance.Status.RestartTrigger = 1 // Same as spec
			})

			It("should not trigger restart and preserve existing PowerSynced condition", func() {
				// Pre-set a PowerSynced condition to verify it's not overwritten
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionPowerSynced,
					metav1.ConditionFalse,
					v1alpha1.HostConditionReasonProgressing,
					"node power state is transitioning",
				)

				result, err := reconciler.reconcileRestartTrigger(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))

				// PowerSynced condition must NOT be overwritten — preserves transition signal
				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
			})
		})

		Context("when restart trigger differs from status and RunStrategy is Halted", func() {
			BeforeEach(func() {
				bareMetalInstance.Spec.RunStrategy = v1alpha1.RunStrategyHalted
				// Restart trigger differs: spec=1, status=0
			})

			It("should sync status without triggering restart", func() {
				triggerRestartCalled := false
				mockMgmtClient.triggerRestartFunc = func(ctx context.Context, hostID string) error {
					triggerRestartCalled = true
					return nil
				}

				result, err := reconciler.reconcileRestartTrigger(ctx, bareMetalInstance)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(triggerRestartCalled).To(BeFalse())

				// Status should be synced to spec
				Expect(bareMetalInstance.Status.RestartTrigger).To(Equal(int64(1)))

				condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				Expect(condition.Reason).To(Equal("Completed"))
			})
		})

		Context("when restart trigger differs from status", func() {
			Context("and no restart is in progress", func() {
				It("should trigger restart and set in progress condition", func() {
					triggerRestartCalled := false
					mockMgmtClient.triggerRestartFunc = func(ctx context.Context, hostID string) error {
						triggerRestartCalled = true
						Expect(hostID).To(Equal("test-host-123"))
						return nil
					}

					result, err := reconciler.reconcileRestartTrigger(ctx, bareMetalInstance)

					Expect(err).NotTo(HaveOccurred())
					Expect(result.RequeueAfter).To(Equal(reconciler.ManagementRecheckIntervalDuration))
					Expect(triggerRestartCalled).To(BeTrue())

					condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
					Expect(condition).NotTo(BeNil())
					Expect(condition.Status).To(Equal(metav1.ConditionFalse))
					Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
				})

				It("should handle trigger restart failure", func() {
					expectedErr := errors.New("restart failed")
					mockMgmtClient.triggerRestartFunc = func(ctx context.Context, hostID string) error {
						return expectedErr
					}

					result, err := reconciler.reconcileRestartTrigger(ctx, bareMetalInstance)

					Expect(err).To(MatchError(ContainSubstring("failed to trigger restart")))
					Expect(result).To(Equal(ctrl.Result{}))

					condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
					Expect(condition).NotTo(BeNil())
					Expect(condition.Status).To(Equal(metav1.ConditionFalse))
					Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonPowerSyncFailed))
				})

				It("should handle transitioning error", func() {
					mockMgmtClient.triggerRestartFunc = func(ctx context.Context, hostID string) error {
						return management.ErrTransitioning
					}

					result, err := reconciler.reconcileRestartTrigger(ctx, bareMetalInstance)

					Expect(err).NotTo(HaveOccurred())
					Expect(result.RequeueAfter).To(Equal(reconciler.ManagementRecheckIntervalDuration))

					condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
					Expect(condition).NotTo(BeNil())
					Expect(condition.Status).To(Equal(metav1.ConditionFalse))
					Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonPowerSyncFailed))
				})
			})

			Context("and restart is already in progress", func() {
				BeforeEach(func() {
					// Set the condition to indicate restart is in progress
					bareMetalInstance.SetStatusCondition(
						v1alpha1.HostConditionPowerSynced,
						metav1.ConditionFalse,
						v1alpha1.HostConditionReasonProgressing,
						"Restart in progress",
					)
				})

				It("should check completion and requeue if not complete", func() {
					isRestartCompleteCalled := false
					mockMgmtClient.isRestartCompleteFunc = func(ctx context.Context, hostID string) (bool, error) {
						isRestartCompleteCalled = true
						Expect(hostID).To(Equal("test-host-123"))
						return false, nil // Not complete
					}

					result, err := reconciler.reconcileRestartTrigger(ctx, bareMetalInstance)

					Expect(err).NotTo(HaveOccurred())
					Expect(result.RequeueAfter).To(Equal(reconciler.ManagementRecheckIntervalDuration))
					Expect(isRestartCompleteCalled).To(BeTrue())
				})

				It("should update status when restart completes", func() {
					mockMgmtClient.isRestartCompleteFunc = func(ctx context.Context, hostID string) (bool, error) {
						return true, nil // Complete
					}

					result, err := reconciler.reconcileRestartTrigger(ctx, bareMetalInstance)

					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(ctrl.Result{}))

					// Status should match spec
					Expect(bareMetalInstance.Status.RestartTrigger).To(Equal(bareMetalInstance.Spec.RestartTrigger))

					condition := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
					Expect(condition).NotTo(BeNil())
					Expect(condition.Status).To(Equal(metav1.ConditionTrue))
					Expect(condition.Reason).To(Equal("Completed"))
				})

				It("should handle completion check failure", func() {
					expectedErr := errors.New("completion check failed")
					mockMgmtClient.isRestartCompleteFunc = func(ctx context.Context, hostID string) (bool, error) {
						return false, expectedErr
					}

					result, err := reconciler.reconcileRestartTrigger(ctx, bareMetalInstance)

					Expect(err).To(HaveOccurred())
					Expect(err).To(Equal(expectedErr))
					Expect(result).To(Equal(ctrl.Result{}))
				})
			})
		})
	})

	Describe("reconcileNICMetadata", func() {
		var (
			ctx               context.Context
			bmi               *v1alpha1.BareMetalInstance
			reconcilerForNICs *BareMetalInstanceReconciler
			invClient         *mockInventoryClient
		)

		BeforeEach(func() {
			ctx = context.Background()
			invClient = &mockInventoryClient{}
			reconcilerForNICs = NewBareMetalInstanceReconciler(
				k8sClient,
				k8sClient.Scheme(),
				invClient,
				&mockManagementClient{},
				nil,
				0, 0, 0, 0,
			)
			bmi = &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nic-test-bmi",
					Namespace: "default",
				},
				Spec: v1alpha1.BareMetalInstanceSpec{
					ExternalHostID: "test-ns/test-host",
					HostClass:      "metal3",
					HostType:       "gpu-node",
					TemplateID:     shared.OsacNoopTemplate,
				},
			}
		})

		Context("when GetHostNICs returns NICs", func() {
			It("populates Status.Hardware.NICs from the inventory client response", func() {
				invClient.getHostNICsFunc = func(_ context.Context, _ string) ([]inventory.HostNIC, error) {
					return []inventory.HostNIC{
						{MAC: "aa:bb:cc:dd:ee:01"},
						{MAC: "ff:00:11:22:33:44"},
					}, nil
				}

				err := reconcilerForNICs.reconcileNICMetadata(ctx, bmi)

				Expect(err).NotTo(HaveOccurred())
				Expect(bmi.Status.Hardware).NotTo(BeNil())
				Expect(bmi.Status.Hardware.NICs).To(HaveLen(2))
				Expect(bmi.Status.Hardware.NICs[0].MAC).To(Equal("aa:bb:cc:dd:ee:01"))
				Expect(bmi.Status.Hardware.NICs[1].MAC).To(Equal("ff:00:11:22:33:44"))
			})
		})

		Context("when GetHostNICs returns an error", func() {
			It("returns the error and leaves Hardware nil", func() {
				backendErr := errors.New("inventory backend unavailable")
				invClient.getHostNICsFunc = func(_ context.Context, _ string) ([]inventory.HostNIC, error) {
					return nil, backendErr
				}

				err := reconcilerForNICs.reconcileNICMetadata(ctx, bmi)

				Expect(err).To(MatchError(backendErr))
				Expect(bmi.Status.Hardware).To(BeNil())
			})

			It("leaves Status.Phase as Progressing (not Ready)", func() {
				invClient.getHostNICsFunc = func(_ context.Context, _ string) ([]inventory.HostNIC, error) {
					return nil, errors.New("transient error")
				}
				bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing

				_ = reconcilerForNICs.reconcileNICMetadata(ctx, bmi)

				Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))
			})
		})

		Context("when Hardware is already set", func() {
			It("skips GetHostNICs when Hardware has NICs (idempotency guard)", func() {
				bmi.Status.Hardware = &v1alpha1.BareMetalHardware{
					NICs: []v1alpha1.BareMetalNICStatus{{MAC: "aa:bb:cc:dd:ee:01"}},
				}

				err := reconcilerForNICs.reconcileNICMetadata(ctx, bmi)

				Expect(err).NotTo(HaveOccurred())
				Expect(invClient.getHostNICsCalled).To(Equal(0))
				Expect(bmi.Status.Hardware.NICs).To(HaveLen(1))
			})

			It("skips GetHostNICs when Hardware is set but NICs is empty (e.g. BCM backend)", func() {
				bmi.Status.Hardware = &v1alpha1.BareMetalHardware{}

				err := reconcilerForNICs.reconcileNICMetadata(ctx, bmi)

				Expect(err).NotTo(HaveOccurred())
				Expect(invClient.getHostNICsCalled).To(Equal(0))
			})
		})

		Context("when GetHostNICs succeeds after a prior transient failure", func() {
			It("populates NICs and does not return an error", func() {
				invClient.getHostNICsFunc = func(_ context.Context, _ string) ([]inventory.HostNIC, error) {
					return []inventory.HostNIC{{MAC: "aa:bb:cc:dd:ee:01"}}, nil
				}

				err := reconcilerForNICs.reconcileNICMetadata(ctx, bmi)

				Expect(err).NotTo(HaveOccurred())
				Expect(bmi.Status.Hardware).NotTo(BeNil())
				Expect(bmi.Status.Hardware.NICs).To(HaveLen(1))
			})
		})
	})
})
