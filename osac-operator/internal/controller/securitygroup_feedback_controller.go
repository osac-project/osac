/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
)

// SecurityGroupFeedbackReconciler sends updates to the fulfillment service.
type SecurityGroupFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.SecurityGroup, *privatev1.SecurityGroup]
	networkingNamespace string
}

// NewSecurityGroupFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about security groups.
func NewSecurityGroupFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, networkingNamespace string) *SecurityGroupFeedbackReconciler {
	sgClient := privatev1.NewSecurityGroupsClient(grpcConn)
	r := &SecurityGroupFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.SecurityGroup, *privatev1.SecurityGroup]{
		Client:    hubClient,
		Finalizer: osacSecurityGroupFeedbackFinalizer,
		IDLabel:   osacSecurityGroupIDLabel,
		Kind:      "SecurityGroup",
		IDKey:     "securityGroupID",
		NewObject: func() *v1alpha1.SecurityGroup { return &v1alpha1.SecurityGroup{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.SecurityGroup, error) {
			response, err := sgClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			sg := response.GetObject()
			if sg == nil {
				return nil, errors.New("security group response contained nil object")
			}
			if !sg.HasSpec() {
				sg.SetSpec(&privatev1.SecurityGroupSpec{})
			}
			if !sg.HasStatus() {
				sg.SetStatus(&privatev1.SecurityGroupStatus{})
			}
			return sg, nil
		},
		Save: func(ctx context.Context, remote *privatev1.SecurityGroup) error {
			_, err := sgClient.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
				Object: remote,
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := sgClient.Signal(ctx, privatev1.SecurityGroupsSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: syncSecurityGroupUpdate,
		SyncDelete: syncSecurityGroupDelete,
	}
	return r
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *SecurityGroupFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("securitygroup-feedback").
		For(&v1alpha1.SecurityGroup{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *SecurityGroupFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

func syncSecurityGroupUpdate(ctx context.Context, obj *v1alpha1.SecurityGroup, remote *privatev1.SecurityGroup) error {
	syncSecurityGroupPhase(ctx, obj, remote)
	return nil
}

func syncSecurityGroupDelete(_ context.Context, obj *v1alpha1.SecurityGroup, remote *privatev1.SecurityGroup) error {
	if obj.Status.Phase == v1alpha1.SecurityGroupPhaseFailed {
		remote.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_DELETE_FAILED)
		return nil
	}
	remote.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_DELETING)
	return nil
}

func syncSecurityGroupPhase(ctx context.Context, obj *v1alpha1.SecurityGroup, remote *privatev1.SecurityGroup) {
	switch obj.Status.Phase {
	case v1alpha1.SecurityGroupPhaseProgressing:
		remote.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING)
	case v1alpha1.SecurityGroupPhaseFailed:
		remote.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED)
	case v1alpha1.SecurityGroupPhaseReady:
		remote.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)
	case v1alpha1.SecurityGroupPhaseDeleting:
		remote.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_DELETING)
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown phase, will ignore it", "phase", obj.Status.Phase)
	}
}
