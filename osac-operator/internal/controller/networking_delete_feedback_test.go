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
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// deleteKind adapts one networking kind to the shared delete-feedback cases.
type deleteKind struct {
	name string
	// run builds a CR from the given status inputs and runs the kind's delete sync
	// against a remote carrying remoteTime. It returns whether the reported state
	// is DELETING (true when the CR is not being deleted, where the cases do not
	// check it), the reported time and the sync error.
	run func(conds []metav1.Condition, recorded, deleted *metav1.Time, remoteTime *timestamppb.Timestamp) (bool, *timestamppb.Timestamp, error)
}

func deleteKinds() []deleteKind {
	objectMeta := func(deleted *metav1.Time) metav1.ObjectMeta {
		return metav1.ObjectMeta{DeletionTimestamp: deleted}
	}
	return []deleteKind{
		{
			name: "ExternalIP",
			run: func(conds []metav1.Condition, recorded, deleted *metav1.Time, remoteTime *timestamppb.Timestamp) (bool, *timestamppb.Timestamp, error) {
				remote := &privatev1.ExternalIP{Id: "eip-1", Status: &privatev1.ExternalIPStatus{}}
				if remoteTime != nil {
					remote.GetStatus().SetStateTransitionTime(remoteTime)
				}
				cr := &v1alpha1.ExternalIP{
					ObjectMeta: objectMeta(deleted),
					Status:     v1alpha1.ExternalIPStatus{Conditions: conds, Address: "192.0.2.10", StateTransitionTime: recorded},
				}
				err := syncExternalIPDelete(context.Background(), cr, remote)
				return deleted == nil || remote.GetStatus().GetState() == privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING,
					remote.GetStatus().GetStateTransitionTime(), err
			},
		},
		{
			name: "NATGateway",
			run: func(conds []metav1.Condition, recorded, deleted *metav1.Time, remoteTime *timestamppb.Timestamp) (bool, *timestamppb.Timestamp, error) {
				remote := &privatev1.NATGateway{Id: "nat-1", Status: &privatev1.NATGatewayStatus{}}
				if remoteTime != nil {
					remote.GetStatus().SetStateTransitionTime(remoteTime)
				}
				cr := &v1alpha1.NATGateway{
					ObjectMeta: objectMeta(deleted),
					Status:     v1alpha1.NATGatewayStatus{Conditions: conds, StateTransitionTime: recorded},
				}
				err := syncNATGatewayDelete(context.Background(), cr, remote)
				return deleted == nil || remote.GetStatus().GetState() == privatev1.NATGatewayState_NAT_GATEWAY_STATE_DELETING,
					remote.GetStatus().GetStateTransitionTime(), err
			},
		},
		{
			name: "ExternalIPAttachment",
			run: func(conds []metav1.Condition, recorded, deleted *metav1.Time, remoteTime *timestamppb.Timestamp) (bool, *timestamppb.Timestamp, error) {
				remote := &privatev1.ExternalIPAttachment{Id: "att-1", Status: &privatev1.ExternalIPAttachmentStatus{}}
				if remoteTime != nil {
					remote.GetStatus().SetStateTransitionTime(remoteTime)
				}
				cr := &v1alpha1.ExternalIPAttachment{
					ObjectMeta: objectMeta(deleted),
					Status:     v1alpha1.ExternalIPAttachmentStatus{Conditions: conds, StateTransitionTime: recorded},
				}
				err := syncExternalIPAttachmentDelete(context.Background(), cr, remote)
				return deleted == nil || remote.GetStatus().GetState() == privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING,
					remote.GetStatus().GetStateTransitionTime(), err
			},
		},
	}
}

func timePtr(sec int64) *metav1.Time {
	t := metav1.NewTime(time.Unix(sec, 0))
	return &t
}

// TestNetworkingDeleteFeedback covers the DELETING report of every networking kind
// that feeds back its deletion (ExternalIP, NATGateway, ExternalIPAttachment).
func TestNetworkingDeleteFeedback(t *testing.T) {
	ready := readyCondition(metav1.ConditionTrue, v1alpha1.ReasonSucceeded, "")
	failed := readyCondition(metav1.ConditionFalse, v1alpha1.ReasonFailed, "boom")

	cases := []struct {
		name       string
		conds      []metav1.Condition
		recorded   *metav1.Time
		deleted    *metav1.Time
		remoteTime *timestamppb.Timestamp
		wantAt     *metav1.Time
		wantErr    string
	}{
		{
			// The recorded time dates the Ready state, not the deletion: reporting
			// DELETING at it would end billing at that time and erase the span up to
			// the deletion.
			name: "ready resource is dated at the deletion", conds: ready,
			recorded: timePtr(1000), deleted: timePtr(2000), wantAt: timePtr(2000),
		},
		{
			// Every condition reports DELETING (D1): there is no Failed branch.
			name: "failed resource is dated at the deletion", conds: failed,
			recorded: timePtr(1000), deleted: timePtr(2000), wantAt: timePtr(2000),
		},
		{
			name: "no CR time is dated at the deletion", conds: ready,
			deleted: timePtr(2000), wantAt: timePtr(2000),
		},
		{
			// A status write after the deletion must not be rejected as backdated.
			name: "a CR time later than the deletion wins", conds: ready,
			recorded: timePtr(3000), deleted: timePtr(2000), wantAt: timePtr(3000),
		},
		{
			name: "remote time is kept when the CR has none", conds: ready,
			remoteTime: timestamppb.New(time.Unix(500, 0)), wantAt: timePtr(500),
		},
		{
			name: "no time anywhere is an error", conds: ready,
			wantErr: "no state transition time",
		},
	}

	for _, kind := range deleteKinds() {
		for _, tc := range cases {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				deleting, at, err := kind.run(tc.conds, tc.recorded, tc.deleted, tc.remoteTime)
				if tc.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
						t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !deleting {
					t.Fatalf("state is not DELETING")
				}
				if want := timestamppb.New(tc.wantAt.Time); !proto.Equal(at, want) {
					t.Fatalf("time = %v, want %v", at.AsTime(), want.AsTime())
				}
			})
		}
	}
}
