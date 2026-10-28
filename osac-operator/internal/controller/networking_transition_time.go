/*
Copyright (c) 2026 Red Hat Inc.

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

	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
)

func setExternalIPState(status *v1alpha1.ExternalIPStatus, state v1alpha1.ExternalIPStateType) {
	if status.State == state {
		return
	}
	status.State = state
	now := metav1.Now()
	status.StateTransitionTime = &now
}

// stateTransitionTimeOf returns the time a status change happened. A CR deleted before any
// controller status write carries no transition time; the deletion started at the deletion
// timestamp, so that is then the time of the change. Reporting no time would make
// fulfillment reject the state change as backdated, and the resource could never finish
// deleting.
func stateTransitionTimeOf(at, deleted *metav1.Time) *timestamppb.Timestamp {
	if at == nil {
		at = deleted
	}
	if at == nil {
		return nil
	}
	return timestamppb.New(at.Time)
}

// laterTransitionTimeOf returns the later of the two times: the deletion
// started at the deletion timestamp, but a reconciler status write can land
// after it, and fulfillment rejects a change whose time is not later than the
// stored one. Reporting the later of the two satisfies both: a stale
// pre-deletion phase time never dates the report (the deletion timestamp
// wins), and a post-delete status write never gets rejected (the status time
// wins).
func laterTransitionTimeOf(a, b *metav1.Time) *timestamppb.Timestamp {
	at := stateTransitionTimeOf(a, b)
	if a != nil && b != nil && b.Time.After(a.Time) {
		at = timestamppb.New(b.Time)
	}
	return at
}

func setExternalIPDeleting(status *v1alpha1.ExternalIPStatus) bool {
	if status.Phase == v1alpha1.ExternalIPPhaseDeleting {
		return false
	}
	status.Phase = v1alpha1.ExternalIPPhaseDeleting
	now := metav1.Now()
	status.StateTransitionTime = &now
	return true
}

func setExternalIPAttachmentPhase(status *v1alpha1.ExternalIPAttachmentStatus, phase v1alpha1.ExternalIPAttachmentPhaseType) bool {
	if status.Phase == phase {
		return false
	}
	status.Phase = phase
	now := metav1.Now()
	status.StateTransitionTime = &now
	return true
}

func setNATGatewayPhase(status *v1alpha1.NATGatewayStatus, phase v1alpha1.NATGatewayPhaseType) bool {
	if status.Phase == phase {
		return false
	}
	status.Phase = phase
	now := metav1.Now()
	status.StateTransitionTime = &now
	return true
}

// backfillAttachmentStateTransitionTime stamps a first-observed time on
// attachments that settled before StateTransitionTime existed (upgrade path),
// so settlement does not reject their feedback. The stamp is persisted once,
// subsequent reocnciles observe a non nil time and skip
func backfillAttachmentStateTransitionTime(ctx context.Context, hubClient clnt.Client, obj *v1alpha1.ExternalIPAttachment) error {
	if obj.Status.StateTransitionTime != nil {
		return nil
	}
	now := metav1.Now()
	obj.Status.StateTransitionTime = &now
	return hubClient.Status().Update(ctx, obj)
}
