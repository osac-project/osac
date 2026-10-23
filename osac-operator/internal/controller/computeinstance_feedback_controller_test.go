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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// mockComputeInstancesClient is a mock implementation of ComputeInstancesClient for testing.
type mockComputeInstancesClient struct {
	getResponse    *privatev1.ComputeInstancesGetResponse
	getError       error
	updateResponse *privatev1.ComputeInstancesUpdateResponse
	updateError    error
	updateCalled   bool
	updateCount    int
	lastUpdate     *privatev1.ComputeInstance
	lastUpdateMask *fieldmaskpb.FieldMask
	signalCalled   bool
	signalCount    int
	signalID       string
	signalError    error
}

func (m *mockComputeInstancesClient) List(ctx context.Context, in *privatev1.ComputeInstancesListRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesListResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockComputeInstancesClient) Get(ctx context.Context, in *privatev1.ComputeInstancesGetRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesGetResponse, error) {
	if m.getError != nil {
		return nil, m.getError
	}
	return m.getResponse, nil
}

func (m *mockComputeInstancesClient) Create(ctx context.Context, in *privatev1.ComputeInstancesCreateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesCreateResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockComputeInstancesClient) Delete(ctx context.Context, in *privatev1.ComputeInstancesDeleteRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockComputeInstancesClient) Update(ctx context.Context, in *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
	m.updateCalled = true
	m.updateCount++
	m.lastUpdate = in.GetObject()
	m.lastUpdateMask = in.GetUpdateMask()
	if m.updateError != nil {
		return nil, m.updateError
	}
	return m.updateResponse, nil
}

func (m *mockComputeInstancesClient) Signal(ctx context.Context, in *privatev1.ComputeInstancesSignalRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesSignalResponse, error) {
	m.signalCalled = true
	m.signalCount++
	m.signalID = in.GetId()
	if m.signalError != nil {
		return nil, m.signalError
	}
	return &privatev1.ComputeInstancesSignalResponse{}, nil
}

var _ = Describe("ComputeInstanceFeedbackReconciler", func() {
	const (
		resourceName      = "test-ci"
		vmNamespace       = "default"
		ciID              = "test-ci-id"
		computeInstanceNS = "osac-computeinstance"
	)

	var (
		ctx                context.Context
		typeNamespacedName types.NamespacedName
		mockClient         *mockComputeInstancesClient
		reconciler         *ComputeInstanceFeedbackReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		typeNamespacedName = types.NamespacedName{
			Name:      resourceName,
			Namespace: computeInstanceNS,
		}
		mockClient = &mockComputeInstancesClient{}
		reconciler = &ComputeInstanceFeedbackReconciler{
			bridge:                   newComputeInstanceFeedbackBridge(k8sClient, mockClient),
			computeInstanceNamespace: computeInstanceNS,
		}

		// Create the namespace if it doesn't exist
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: computeInstanceNS,
			},
		}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: computeInstanceNS}, namespace)
		if err != nil && apierrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		}
	})

	Context("state transition time feedback", func() {
		remoteWithState := func(state privatev1.ComputeInstanceState, transitionTime *timestamppb.Timestamp) *privatev1.ComputeInstance {
			return privatev1.ComputeInstance_builder{
				Status: privatev1.ComputeInstanceStatus_builder{
					State:               state,
					StateTransitionTime: transitionTime,
				}.Build(),
			}.Build()
		}

		It("copies the CR's phase transition time when the state changes", func() {
			oldStamp := timestamppb.New(time.Now().Add(-2 * time.Hour))
			phaseStamp := metav1.NewTime(time.Now().Add(-time.Hour).UTC())
			remote := remoteWithState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, oldStamp)
			obj := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
				Phase:               osacv1alpha1.ComputeInstancePhaseFailed,
				PhaseTransitionTime: &phaseStamp,
			}}

			syncCIPhase(ctx, obj, remote)

			Expect(remote.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
			// The copied time is the CR's recorded transition, not the feedback moment:
			// feedback can run long after the phase moved.
			Expect(remote.GetStatus().GetStateTransitionTime().AsTime()).To(Equal(phaseStamp.Time))
		})

		It("bumps the stored time by one microsecond when the CR stamp equals it", func() {
			// Two phase changes within one second carry the same whole-second CR stamp; the
			// source contract rejects a changed value with a time equal to the stored one,
			// so the write must land past the stored time instead.
			phaseStamp := metav1.NewTime(time.Now().Add(-time.Hour).UTC().Truncate(time.Second))
			stored := timestamppb.New(phaseStamp.Time)
			remote := remoteWithState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, stored)
			obj := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
				Phase:               osacv1alpha1.ComputeInstancePhaseFailed,
				PhaseTransitionTime: &phaseStamp,
			}}

			syncCIPhase(ctx, obj, remote)

			Expect(remote.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
			Expect(remote.GetStatus().GetStateTransitionTime().AsTime()).To(
				Equal(phaseStamp.Time.Add(time.Microsecond)))
		})

		It("falls back to the observation time when the CR carries no phase transition time", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, oldStamp)
			obj := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
				Phase: osacv1alpha1.ComputeInstancePhaseFailed,
			}}

			syncCIPhase(ctx, obj, remote)

			Expect(remote.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
			Expect(remote.GetStatus().GetStateTransitionTime().AsTime()).To(BeTemporally(">", oldStamp.AsTime()))
		})

		It("keeps the existing transition time when the state is unchanged", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, oldStamp)
			obj := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
				Phase: osacv1alpha1.ComputeInstancePhaseRunning,
			}}

			syncCIPhase(ctx, obj, remote)

			Expect(remote.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
			Expect(remote.GetStatus().GetStateTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("leaves the state and time untouched for an unknown phase", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, oldStamp)
			obj := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
				Phase: osacv1alpha1.ComputeInstancePhaseType("unexpected"),
			}}

			syncCIPhase(ctx, obj, remote)

			Expect(remote.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
			Expect(remote.GetStatus().GetStateTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})
	})

	Context("delete path feedback", func() {
		remoteWithState := func(state privatev1.ComputeInstanceState, transitionTime *timestamppb.Timestamp) *privatev1.ComputeInstance {
			return privatev1.ComputeInstance_builder{
				Status: privatev1.ComputeInstanceStatus_builder{
					State:               state,
					StateTransitionTime: transitionTime,
				}.Build(),
			}.Build()
		}

		It("copies the CR's phase transition time when the delete path forces DELETING", func() {
			oldStamp := timestamppb.New(time.Now().Add(-2 * time.Hour))
			phaseStamp := metav1.NewTime(time.Now().Add(-time.Hour).UTC())
			remote := remoteWithState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, oldStamp)
			obj := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
				Phase:               osacv1alpha1.ComputeInstancePhaseDeleting,
				PhaseTransitionTime: &phaseStamp,
			}}

			Expect(syncComputeInstanceDelete(ctx, obj, remote)).To(Succeed())

			Expect(remote.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING))
			// The copied time is the CR's recorded transition, not the feedback moment.
			Expect(remote.GetStatus().GetStateTransitionTime().AsTime()).To(Equal(phaseStamp.Time))
		})

		It("dates DELETING at the deletionTimestamp when the CR phase has not moved yet", func() {
			// The delete path can sync before the ComputeInstance CR records Phase=Deleting.
			// The CR's PhaseTransitionTime then still belongs to the previous phase, and
			// copying it (or a one-microsecond bump of it) would date DELETING at the old
			// phase's moment, billing the entire RUNNING span as DELETING.
			runningStamp := metav1.NewTime(time.Now().Add(-2 * time.Hour).UTC())
			deletedAt := metav1.NewTime(time.Now().Add(-5 * time.Minute).UTC())
			remote := remoteWithState(
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
				timestamppb.New(runningStamp.Time))
			obj := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					DeletionTimestamp: &deletedAt,
				},
				Status: osacv1alpha1.ComputeInstanceStatus{
					Phase:               osacv1alpha1.ComputeInstancePhaseRunning,
					PhaseTransitionTime: &runningStamp,
				},
			}

			Expect(syncComputeInstanceDelete(ctx, obj, remote)).To(Succeed())

			Expect(remote.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING))
			Expect(remote.GetStatus().GetStateTransitionTime().AsTime()).To(Equal(deletedAt.Time))
			Expect(remote.GetStatus().GetStateTransitionTime().AsTime()).To(BeTemporally(">", runningStamp.Time))
		})

		It("stamps an observation-time transition when the deletion carries no timestamp", func() {
			runningStamp := metav1.NewTime(time.Now().Add(-2 * time.Hour).UTC())
			remote := remoteWithState(
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
				timestamppb.New(runningStamp.Time))
			obj := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
				Phase:               osacv1alpha1.ComputeInstancePhaseRunning,
				PhaseTransitionTime: &runningStamp,
			}}

			Expect(syncComputeInstanceDelete(ctx, obj, remote)).To(Succeed())

			Expect(remote.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING))
			Expect(remote.GetStatus().GetStateTransitionTime().AsTime()).To(BeTemporally(">", runningStamp.Time))
		})
	})

	Context("network attachment IP feedback", func() {
		It("copies attachment Subnet ID and IP into the Fulfillment status", func() {
			obj := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
				ComputeNetworkAttachmentStatuses: []osacv1alpha1.ComputeNetworkAttachmentStatus{{
					SubnetRef: "subnet-id-123",
					IPAddress: "10.0.0.42",
				}},
			}}

			remote := privatev1.ComputeInstance_builder{
				Status: privatev1.ComputeInstanceStatus_builder{}.Build(),
			}.Build()
			syncCIIPAddresses(obj, remote)

			statuses := remote.GetStatus().GetComputeNetworkAttachmentStatuses()
			Expect(statuses).To(HaveLen(1))
			Expect(statuses[0].GetSubnetRef()).To(Equal("subnet-id-123"))
			Expect(statuses[0].GetIpAddress()).To(Equal("10.0.0.42"))
		})

		It("clears stale attachment status when the operator reports none", func() {
			obj := &osacv1alpha1.ComputeInstance{}
			remote := privatev1.ComputeInstance_builder{
				Status: privatev1.ComputeInstanceStatus_builder{
					ComputeNetworkAttachmentStatuses: []*privatev1.ComputeNetworkAttachmentStatus{
						privatev1.ComputeNetworkAttachmentStatus_builder{
							SubnetRef: "subnet-id-old",
							IpAddress: "10.0.0.41",
						}.Build(),
					},
				}.Build(),
			}.Build()

			syncCIIPAddresses(obj, remote)

			Expect(remote.GetStatus().GetComputeNetworkAttachmentStatuses()).To(BeEmpty())
		})
	})

	Context("running instance type feedback", func() {
		typeRef := func(id, name string) *privatev1.InstanceTypeReference {
			return privatev1.InstanceTypeReference_builder{Id: id, Name: name}.Build()
		}

		runningCR := func(restartRequired, configApplied bool) *osacv1alpha1.ComputeInstance {
			conditionStatus := func(value bool) metav1.ConditionStatus {
				if value {
					return metav1.ConditionTrue
				}
				return metav1.ConditionFalse
			}
			conditions := []metav1.Condition{
				{
					Type:               string(osacv1alpha1.ComputeInstanceConditionRestartRequired),
					Status:             conditionStatus(restartRequired),
					Reason:             "AsExpected",
					LastTransitionTime: metav1.NewTime(time.Now().UTC()),
				},
				{
					Type:               string(osacv1alpha1.ComputeInstanceConditionConfigurationApplied),
					Status:             conditionStatus(configApplied),
					Reason:             "AsExpected",
					LastTransitionTime: metav1.NewTime(time.Now().UTC()),
				},
			}
			version, err := provisioning.ComputeDesiredConfigVersion(osacv1alpha1.ComputeInstanceSpec{})
			Expect(err).ToNot(HaveOccurred())
			runningSince := metav1.NewTime(time.Now().Add(-time.Hour).UTC())
			return &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						osacComputeInstanceTypeNameLabel: "standard-8-32",
					},
				},
				Status: osacv1alpha1.ComputeInstanceStatus{
					Phase:                   osacv1alpha1.ComputeInstancePhaseRunning,
					DesiredConfigVersion:    version,
					RunningConfigVersion:    version,
					RunningInstanceTypeName: "standard-8-32",
					RunningConfigSince:      &runningSince,
					Conditions:              conditions,
				}}
		}

		remoteWithRequestedType := func(running *privatev1.InstanceTypeReference, transitionTime *timestamppb.Timestamp) *privatev1.ComputeInstance {
			return privatev1.ComputeInstance_builder{
				Spec: privatev1.ComputeInstanceSpec_builder{
					InstanceType: typeRef("standard-8-32", "standard-8-32"),
				}.Build(),
				Status: privatev1.ComputeInstanceStatus_builder{
					InstanceType:               running,
					InstanceTypeTransitionTime: transitionTime,
				}.Build(),
			}.Build()
		}

		It("stamps the requested type when the resize completes", func() {
			oldStamp := timestamppb.New(time.Now().Add(-2 * time.Hour))
			remote := remoteWithRequestedType(typeRef("standard-4-16", "standard-4-16"), oldStamp)
			cr := runningCR(false, true)

			syncCIRunningInstanceType(cr, remote)

			Expect(remote.GetStatus().GetInstanceType().GetId()).To(Equal("standard-8-32"))
			Expect(remote.GetStatus().GetInstanceType().GetName()).To(Equal("standard-8-32"))
			// The transition time is the CR's recorded observation of the running
			// configuration changing, not the time of this reconciliation.
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(cr.Status.RunningConfigSince.Time))
		})

		It("stamps the type on the first running observation", func() {
			remote := remoteWithRequestedType(nil, nil)
			cr := runningCR(false, true)

			syncCIRunningInstanceType(cr, remote)

			Expect(remote.GetStatus().GetInstanceType().GetId()).To(Equal("standard-8-32"))
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(cr.Status.RunningConfigSince.Time))
		})

		It("does not stamp while the CR label still names the previous type", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithRequestedType(typeRef("standard-4-16", "standard-4-16"), oldStamp)
			cr := runningCR(false, true)
			// The reconciler has not yet caught up to the resize: the CR label still
			// names the type the CR spec was last reconciled toward.
			cr.Labels[osacComputeInstanceTypeNameLabel] = "standard-4-16"

			syncCIRunningInstanceType(cr, remote)

			Expect(remote.GetStatus().GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("does not stamp when the CR carries no instance type label", func() {
			remote := remoteWithRequestedType(nil, nil)
			cr := runningCR(false, true)
			cr.Labels = nil

			syncCIRunningInstanceType(cr, remote)

			Expect(remote.GetStatus().GetInstanceType()).To(BeNil())
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime()).To(BeNil())
		})

		It("does not stamp when the label is missing and the request carries no name", func() {
			// A request reference without a name and a CR without the label both read as
			// the empty string; comparing them would pass the gate vacuously, without the
			// reconciler ever having recorded the applied type.
			remote := privatev1.ComputeInstance_builder{
				Spec: privatev1.ComputeInstanceSpec_builder{
					InstanceType: typeRef("standard-8-32", ""),
				}.Build(),
				Status: privatev1.ComputeInstanceStatus_builder{}.Build(),
			}.Build()
			cr := runningCR(false, true)
			cr.Labels = nil

			syncCIRunningInstanceType(cr, remote)

			Expect(remote.GetStatus().GetInstanceType()).To(BeNil())
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime()).To(BeNil())
		})

		It("does not stamp when the restart condition is absent", func() {
			// KubeVirt reports RestartRequired asynchronously: an absent condition
			// vouches for nothing, so the recorded observation cannot confirm the
			// requested type running yet.
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithRequestedType(typeRef("standard-4-16", "standard-4-16"), oldStamp)
			cr := runningCR(false, true)
			cr.Status.Conditions = []metav1.Condition{
				{
					Type:               string(osacv1alpha1.ComputeInstanceConditionConfigurationApplied),
					Status:             metav1.ConditionTrue,
					Reason:             "AsExpected",
					LastTransitionTime: metav1.NewTime(time.Now().UTC()),
				},
			}

			syncCIRunningInstanceType(cr, remote)

			Expect(remote.GetStatus().GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("does not stamp when the restart condition is unknown", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithRequestedType(typeRef("standard-4-16", "standard-4-16"), oldStamp)
			cr := runningCR(false, true)
			cr.Status.Conditions[0].Status = metav1.ConditionUnknown

			syncCIRunningInstanceType(cr, remote)

			Expect(remote.GetStatus().GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("does not stamp while the recorded name lags the label", func() {
			// After the label moves to the new type but before recordRunningConfig
			// records it, the recorded observation still describes the previous type:
			// accepting it would assign the previous type's running time to the new
			// one, and the fulfillment contract would keep the wrong time forever.
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithRequestedType(typeRef("standard-4-16", "standard-4-16"), oldStamp)
			cr := runningCR(false, true)
			cr.Status.RunningInstanceTypeName = "standard-4-16"

			syncCIRunningInstanceType(cr, remote)

			Expect(remote.GetStatus().GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("does not stamp while a restart is still required", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithRequestedType(typeRef("standard-4-16", "standard-4-16"), oldStamp)

			syncCIRunningInstanceType(runningCR(true, true), remote)

			Expect(remote.GetStatus().GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("does not stamp when the running-config observation carries no time", func() {
			remote := remoteWithRequestedType(nil, nil)
			noSince := runningCR(false, true)
			noSince.Status.RunningConfigSince = nil

			syncCIRunningInstanceType(noSince, remote)

			Expect(remote.GetStatus().GetInstanceType()).To(BeNil())
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime()).To(BeNil())
		})

		It("keeps the existing stamp when already running the requested type", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithRequestedType(typeRef("standard-8-32", "standard-8-32"), oldStamp)

			syncCIRunningInstanceType(runningCR(false, true), remote)

			Expect(remote.GetStatus().GetInstanceType().GetId()).To(Equal("standard-8-32"))
			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("keeps the stamp when the same type is referenced by id under a different name", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithRequestedType(typeRef("standard-8-32", "old-name"), oldStamp)

			syncCIRunningInstanceType(runningCR(false, true), remote)

			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("keeps the stamp when the recorded type carries no id and only the name matches", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithRequestedType(typeRef("", "standard-8-32"), oldStamp)

			syncCIRunningInstanceType(runningCR(false, true), remote)

			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("keeps the stamp when only the resolution hints differ", func() {
			oldStamp := timestamppb.New(time.Now().Add(-time.Hour))
			remote := remoteWithRequestedType(privatev1.InstanceTypeReference_builder{
				Id:      "standard-8-32",
				Name:    "standard-8-32",
				Project: "other-project",
				Shared:  true,
			}.Build(), oldStamp)

			syncCIRunningInstanceType(runningCR(false, true), remote)

			Expect(remote.GetStatus().GetInstanceTypeTransitionTime().AsTime()).To(Equal(oldStamp.AsTime()))
		})

		It("sends the running instance type paths in the update mask", func() {
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel:       ciID,
						osacComputeInstanceTypeNameLabel: "standard-8-32",
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
			DeferCleanup(func() {
				updated := &osacv1alpha1.ComputeInstance{}
				err := k8sClient.Get(ctx, typeNamespacedName, updated)
				if apierrors.IsNotFound(err) {
					return
				}
				Expect(err).ToNot(HaveOccurred())
				updated.Finalizers = nil
				Expect(k8sClient.Update(ctx, updated)).To(Succeed())
				Expect(k8sClient.Delete(ctx, updated)).To(Succeed())
			})
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status = runningCR(false, true).Status
			// The desired config version must hash the CR's actual spec, as the CR
			// controller would compute it, and the running-config observation must
			// record that same spec, so the status vouches for this spec.
			version, err := provisioning.ComputeDesiredConfigVersion(vm.Spec)
			Expect(err).ToNot(HaveOccurred())
			vm.Status.DesiredConfigVersion = version
			vm.Status.RunningConfigVersion = version
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: remoteWithRequestedType(nil, nil),
			}
			mockClient.updateResponse = &privatev1.ComputeInstancesUpdateResponse{}

			_, reconcileErr := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(reconcileErr).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdateMask.GetPaths()).To(ContainElement("status.instance_type"))
			Expect(mockClient.lastUpdateMask.GetPaths()).To(ContainElement("status.instance_type_transition_time"))
			// The state changes from unspecified to running, so the sync must also stamp the
			// state transition time and its mask path must be sent along.
			Expect(mockClient.lastUpdateMask.GetPaths()).To(ContainElement("status.state_transition_time"))
			Expect(mockClient.lastUpdate.GetStatus().GetInstanceType().GetId()).To(Equal("standard-8-32"))
			Expect(mockClient.lastUpdate.GetStatus().GetInstanceTypeTransitionTime()).ToNot(BeNil())
			Expect(mockClient.lastUpdate.GetStatus().GetStateTransitionTime()).ToNot(BeNil())
		})
	})

	Context("When reconciling a resource that doesn't exist", func() {
		It("should return without error and not signal", func() {
			request := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent",
					Namespace: computeInstanceNS,
				},
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeFalse())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource without the VM ID label", func() {
		BeforeEach(func() {
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				_ = k8sClient.Update(ctx, vm)
				_ = k8sClient.Delete(ctx, vm)
			}
		})

		It("should skip reconciliation", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeFalse())
		})

		It("should remove feedback finalizer from CR without CI ID label being deleted", func() {
			// Add the feedback finalizer and trigger deletion
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Finalizers = []string{osacComputeInstanceFeedbackFinalizer}
			Expect(k8sClient.Update(ctx, vm)).To(Succeed())

			// Delete triggers DeletionTimestamp
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			// Verify CR is gone (finalizer removed, GC proceeded)
			updated := &osacv1alpha1.ComputeInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource that is being deleted", func() {
		BeforeEach(func() {
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFeedbackFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			// Set the phase to Deleting (as the main controller would do in handleDelete)
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			// Delete the CR (sets DeletionTimestamp, CR stays because of finalizer)
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: &privatev1.ComputeInstance{
					Id:   ciID,
					Spec: &privatev1.ComputeInstanceSpec{},
					Status: &privatev1.ComputeInstanceStatus{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
						Volumes: []*privatev1.ComputeInstanceVolume{
							privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-delete-feedback"}.Build(),
						},
						VolumeRegistration: []*privatev1.ComputeInstanceVolumeRegistration{
							privatev1.ComputeInstanceVolumeRegistration_builder{
								DiskKey: "boot",
								State:   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
							}.Build(),
							privatev1.ComputeInstanceVolumeRegistration_builder{
								DiskKey: "disk-1",
								State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN,
							}.Build(),
						},
					},
				},
			}
			mockClient.updateResponse = &privatev1.ComputeInstancesUpdateResponse{}
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				// Force delete by removing finalizers
				vm.Finalizers = nil
				Expect(k8sClient.Update(ctx, vm)).To(Succeed())
			}
		})

		It("should sync Deleting state to fulfillment service", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate).NotTo(BeNil())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING))
			Expect(mockClient.lastUpdateMask.GetPaths()).ToNot(ContainElement("status.volumes"))
			Expect(mockClient.lastUpdateMask.GetPaths()).ToNot(ContainElement("status.volume_registration"))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumes()).To(HaveLen(1))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumes()[0].GetDiskKey()).To(Equal("boot"))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-delete-feedback"))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumeRegistration()).To(HaveLen(2))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumeRegistration()[0].GetDiskKey()).To(Equal("boot"))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumeRegistration()[0].GetState()).To(
				Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumeRegistration()[1].GetDiskKey()).To(Equal("disk-1"))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumeRegistration()[1].GetState()).To(
				Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN))
		})

		It("should signal and remove finalizer when it's the last one", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeTrue())
			Expect(mockClient.signalID).To(Equal(ciID))

			// CR should be gone (finalizer removed, GC proceeds)
			updated := &osacv1alpha1.ComputeInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should still remove finalizer when Signal fails", func() {
			mockClient.signalError = errors.New("already archived")

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeTrue())

			// Finalizer should still be removed despite Signal error
			updated := &osacv1alpha1.ComputeInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

	})

	Context("When reconciling a resource being deleted with multiple finalizers", func() {
		const deletingResourceName = "test-ci-multi-fin"

		var deletingNamespacedName types.NamespacedName

		BeforeEach(func() {
			deletingNamespacedName = types.NamespacedName{
				Name:      deletingResourceName,
				Namespace: computeInstanceNS,
			}

			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deletingResourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFinalizer, osacComputeInstanceFeedbackFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, deletingNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, deletingNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: &privatev1.ComputeInstance{
					Id:   ciID,
					Spec: &privatev1.ComputeInstanceSpec{},
					Status: &privatev1.ComputeInstanceStatus{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					},
				},
			}
			mockClient.updateResponse = &privatev1.ComputeInstancesUpdateResponse{}
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, deletingNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				Expect(k8sClient.Update(ctx, vm)).To(Succeed())
			}
		})

		It("should not signal when other finalizers are still present", func() {
			request := reconcile.Request{
				NamespacedName: deletingNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			// Should sync state but NOT signal
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())

			// Finalizer should still be present
			updated := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, deletingNamespacedName, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacComputeInstanceFeedbackFinalizer)).To(BeTrue())
		})
	})

	Context("When reconciling a resource being deleted without feedback finalizer", func() {
		const deletingResourceName = "test-ci-no-fb-fin"

		var deletingNamespacedName types.NamespacedName

		BeforeEach(func() {
			deletingNamespacedName = types.NamespacedName{
				Name:      deletingResourceName,
				Namespace: computeInstanceNS,
			}

			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deletingResourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, deletingNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, deletingNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: &privatev1.ComputeInstance{
					Id:   ciID,
					Spec: &privatev1.ComputeInstanceSpec{},
					Status: &privatev1.ComputeInstanceStatus{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					},
				},
			}
			mockClient.updateResponse = &privatev1.ComputeInstancesUpdateResponse{}
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, deletingNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				Expect(k8sClient.Update(ctx, vm)).To(Succeed())
			}
		})

		It("should not add finalizer to CR being deleted without feedback finalizer", func() {
			request := reconcile.Request{
				NamespacedName: deletingNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			// Verify feedback finalizer was NOT added
			updated := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, deletingNamespacedName, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacComputeInstanceFeedbackFinalizer)).To(BeFalse())
			// Signal should NOT be called (our finalizer isn't present)
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource being deleted and fulfillment-service returns NotFound", func() {
		BeforeEach(func() {
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFeedbackFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			mockClient.getError = grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", ciID)
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				Expect(k8sClient.Update(ctx, vm)).To(Succeed())
			}
		})

		It("should remove feedback finalizer and return without error", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeFalse())
			Expect(mockClient.signalCalled).To(BeFalse())

			updated := &osacv1alpha1.ComputeInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When fulfillment-service returns NotFound for a resource that is NOT being deleted", func() {
		BeforeEach(func() {
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFeedbackFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			mockClient.getError = grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", ciID)
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				_ = k8sClient.Update(ctx, vm)
				_ = k8sClient.Delete(ctx, vm)
			}
		})

		It("should propagate the NotFound error", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(codes.NotFound))
		})
	})

	Context("When reconciling a valid resource", func() {
		BeforeEach(func() {
			// Reset mock client state
			mockClient.updateCalled = false
			mockClient.lastUpdate = nil

			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
			// Update status separately since Status is a subresource - need to get fresh copy
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			Expect(err).NotTo(HaveOccurred())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseRunning
			vm.Status.Conditions = []metav1.Condition{
				{
					Type:               string(osacv1alpha1.ComputeInstanceConditionConfigurationApplied),
					Status:             metav1.ConditionFalse,
					Reason:             "AsExpected",
					Message:            "Applying configuration",
					LastTransitionTime: metav1.NewTime(time.Now().UTC()),
				},
			}
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			// Setup mock response
			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: &privatev1.ComputeInstance{
					Id:   ciID,
					Spec: &privatev1.ComputeInstanceSpec{},
					Status: &privatev1.ComputeInstanceStatus{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED,
						Volumes: []*privatev1.ComputeInstanceVolume{
							privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-feedback"}.Build(),
						},
						VolumeRegistration: []*privatev1.ComputeInstanceVolumeRegistration{
							privatev1.ComputeInstanceVolumeRegistration_builder{
								DiskKey: "boot",
								State:   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
							}.Build(),
							privatev1.ComputeInstanceVolumeRegistration_builder{
								DiskKey: "disk-1",
								State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN,
							}.Build(),
						},
					},
				},
			}
			mockClient.updateResponse = &privatev1.ComputeInstancesUpdateResponse{}
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				// Remove finalizers so the CR can be deleted
				vm.Finalizers = nil
				_ = k8sClient.Update(ctx, vm)
				Expect(k8sClient.Delete(ctx, vm)).To(Succeed())
			}
		})

		It("should add feedback finalizer on first reconcile", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			// Verify feedback finalizer was added
			updated := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacComputeInstanceFeedbackFinalizer)).To(BeTrue())
		})

		It("should successfully sync conditions and phase", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate).NotTo(BeNil())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
			Expect(mockClient.lastUpdateMask).NotTo(BeNil())
			Expect(mockClient.lastUpdateMask.GetPaths()).To(ContainElement("status.compute_network_attachment_statuses"))
			Expect(mockClient.lastUpdateMask.GetPaths()).ToNot(ContainElement("status.volumes"))
			Expect(mockClient.lastUpdateMask.GetPaths()).ToNot(ContainElement("status.volume_registration"))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumes()).To(HaveLen(1))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumes()[0].GetDiskKey()).To(Equal("boot"))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-feedback"))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumeRegistration()).To(HaveLen(2))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumeRegistration()[0].GetDiskKey()).To(Equal("boot"))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumeRegistration()[0].GetState()).To(
				Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
			Expect(mockClient.lastUpdate.GetStatus().GetVolumeRegistration()[1].GetState()).To(
				Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_UNCERTAIN))
		})

		It("should sync ConfigurationApplied condition", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			// Check that the condition was synced
			vm := mockClient.lastUpdate
			found := false
			for _, cond := range vm.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
					Expect(cond.GetMessage()).To(Equal("Applying configuration"))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Starting phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseStarting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
		})

		It("should sync Failed phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseFailed
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
		})

		It("should sync Deleting phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING))
		})

		It("should update only once when reconciliation is run twice with same data", func() {
			// Reset update count
			mockClient.updateCount = 0
			mockClient.updateCalled = false

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}

			// First reconciliation - should trigger an update
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCount).To(Equal(1))
			Expect(mockClient.updateCalled).To(BeTrue())

			// Second reconciliation with same data - should NOT trigger another update
			// because the VM object in the fulfillment service now matches what we're trying to sync
			// We need to update the mock's getResponse to reflect the state after the first update
			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: mockClient.lastUpdate,
			}

			// Run reconciliation again
			result, err = reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			// Update count should still be 1, not 2, because only the timestamp changed, not the status
			Expect(mockClient.updateCount).To(Equal(1))
		})

		It("should sync lastRestartedAt when set in K8s CR", func() {
			// Get the ComputeInstance and update its status with lastRestartedAt
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			// Use time without nanoseconds to match protobuf precision
			restartTime := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
			computeInstance.Status.LastRestartedAt = &restartTime
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate).NotTo(BeNil())

			// Verify lastRestartedAt was synced
			Expect(mockClient.lastUpdate.GetStatus().HasLastRestartedAt()).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetLastRestartedAt().AsTime()).To(Equal(restartTime.Time))
		})

		It("should not set lastRestartedAt when nil in K8s CR", func() {
			// Get the ComputeInstance - lastRestartedAt should be nil by default
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			Expect(computeInstance.Status.LastRestartedAt).To(BeNil())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate).NotTo(BeNil())

			// Verify lastRestartedAt was NOT set (still using default from mock)
			Expect(mockClient.lastUpdate.GetStatus().HasLastRestartedAt()).To(BeFalse())
		})

		It("should sync RestartInProgress condition when set to True", func() {
			// Get the ComputeInstance and add RestartInProgress condition
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			restartInProgressMessage := "Restart initiated at 2026-02-01T10:20:58Z"
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionRestartInProgress),
				Status:             metav1.ConditionTrue,
				Reason:             "RestartInProgress",
				Message:            restartInProgressMessage,
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			// Verify RestartInProgress condition was synced
			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					Expect(cond.GetMessage()).To(Equal(restartInProgressMessage))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync RestartFailed condition when set to True", func() {
			// Get the ComputeInstance and add RestartFailed condition
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			restartFailedMessage := "No VirtualMachine reference found"
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionRestartFailed),
				Status:             metav1.ConditionTrue,
				Reason:             "NoVMReference",
				Message:            restartFailedMessage,
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			// Verify RestartFailed condition was synced
			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_FAILED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					Expect(cond.GetMessage()).To(Equal(restartFailedMessage))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Stopping phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseStopping
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING))
		})

		It("should sync Stopped phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseStopped
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED))
		})

		It("should sync Paused phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhasePaused
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED))
		})

		It("should sync Provisioned condition when True", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionProvisioned),
				Status:             metav1.ConditionTrue,
				Reason:             osacv1alpha1.ReasonInfrastructureReady,
				Message:            "All infrastructure resources provisioned successfully",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					Expect(cond.GetReason()).To(Equal(osacv1alpha1.ReasonInfrastructureReady))
					Expect(cond.GetMessage()).To(Equal("All infrastructure resources provisioned successfully"))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Provisioned condition when False", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionProvisioned),
				Status:             metav1.ConditionFalse,
				Reason:             osacv1alpha1.ReasonProvisioningStorage,
				Message:            "Creating DataVolumes for boot disk (20GiB)",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
					Expect(cond.GetMessage()).To(Equal("Creating DataVolumes for boot disk (20GiB)"))
					Expect(cond.GetReason()).To(Equal(osacv1alpha1.ReasonProvisioningStorage))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Ready condition with Reason", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionReady),
				Status:             metav1.ConditionTrue,
				Reason:             osacv1alpha1.ReasonAsExpected,
				Message:            "",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					Expect(cond.GetReason()).To(Equal(osacv1alpha1.ReasonAsExpected))
					Expect(cond.GetMessage()).To(Equal(""))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Ready condition when False with reason and message", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			msg := "VirtualMachine not yet created, waiting for provisioning"
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionReady),
				Status:             metav1.ConditionFalse,
				Reason:             osacv1alpha1.ReasonWaitingForVM,
				Message:            msg,
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
					Expect(cond.GetReason()).To(Equal(osacv1alpha1.ReasonWaitingForVM))
					Expect(cond.GetMessage()).To(Equal(msg))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync RestartRequired condition when True", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionRestartRequired),
				Status:             metav1.ConditionTrue,
				Reason:             "AsExpected",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_REQUIRED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync RestartRequired condition when False", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionRestartRequired),
				Status:             metav1.ConditionFalse,
				Reason:             "AsExpected",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_REQUIRED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync external IP address from status", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.ExternalIPAddress = "10.0.0.100"
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(Equal("10.0.0.100"))
		})

		It("should sync internal IP address from CR status", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.IPAddress = "192.168.1.50"
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetInternalIpAddress()).To(Equal("192.168.1.50"))
		})

		It("should sync both external and internal IP addresses when both are set", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.ExternalIPAddress = "10.0.0.100"
			computeInstance.Status.IPAddress = "192.168.1.50"
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(Equal("10.0.0.100"))
			Expect(mockClient.lastUpdate.GetStatus().GetInternalIpAddress()).To(Equal("192.168.1.50"))
		})

		It("should clear IP addresses when neither external IP status nor internal IP is set", func() {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(BeEmpty())
			Expect(mockClient.lastUpdate.GetStatus().GetInternalIpAddress()).To(BeEmpty())
		})

		It("should clear external IP when status field is cleared after detach", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.ExternalIPAddress = "10.0.0.100"
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(Equal("10.0.0.100"))

			// Simulate detach: clear the status field
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.ExternalIPAddress = ""
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			mockClient.updateCalled = false
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(BeEmpty())
		})

		It("should not crash when restart conditions are not present", func() {
			// RestartInProgress and RestartFailed conditions should not be present by default
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			// Verify conditions don't include restart conditions
			for _, cond := range computeInstance.Status.Conditions {
				Expect(cond.Type).NotTo(Equal(string(osacv1alpha1.ComputeInstanceConditionRestartInProgress)))
				Expect(cond.Type).NotTo(Equal(string(osacv1alpha1.ComputeInstanceConditionRestartFailed)))
			}

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
		})
	})
})
