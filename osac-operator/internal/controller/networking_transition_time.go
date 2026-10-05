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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
)

func setExternalIPState(status *v1alpha1.ExternalIPStatus, state v1alpha1.ExternalIPStateType) {
	setState(&status.State, &status.StateTransitionTime, state, metav1.Now())
}

func setExternalIPDeleting(status *v1alpha1.ExternalIPStatus) bool {
	return setState(&status.Phase, &status.StateTransitionTime, v1alpha1.ExternalIPPhaseDeleting, metav1.Now())
}

func setExternalIPAttachmentPhase(status *v1alpha1.ExternalIPAttachmentStatus, phase v1alpha1.ExternalIPAttachmentPhaseType) bool {
	return setState(&status.Phase, &status.StateTransitionTime, phase, metav1.Now())
}

func setNATGatewayPhase(status *v1alpha1.NATGatewayStatus, phase v1alpha1.NATGatewayPhaseType) bool {
	return setState(&status.Phase, &status.StateTransitionTime, phase, metav1.Now())
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
