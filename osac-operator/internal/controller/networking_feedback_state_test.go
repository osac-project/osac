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
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func readyCondition(status metav1.ConditionStatus, reason, message string) []metav1.Condition {
	var conditions []metav1.Condition
	meta.SetStatusCondition(&conditions, metav1.Condition{Type: v1alpha1.NetworkConditionReady, Status: status, Reason: reason, Message: message})
	return conditions
}

// TestNetworkFeedbackMapping covers the Unified design Status table.
func TestNetworkFeedbackMapping(t *testing.T) {
	now := metav1.Now()
	cases := []struct {
		name        string
		conditions  []metav1.Condition
		deleting    bool
		jobs        []v1alpha1.NetworkJobStatus
		wantState   networkFeedbackState
		wantMessage string
	}{
		{name: "no Ready yet", wantState: networkFeedbackPending},
		{name: "Progressing", conditions: readyCondition(metav1.ConditionFalse, v1alpha1.ReasonProgressing, "create_subnet (fabric) running in AAP job 7"),
			wantState: networkFeedbackPending, wantMessage: "create_subnet (fabric) running in AAP job 7"},
		{name: "Succeeded", conditions: readyCondition(metav1.ConditionTrue, v1alpha1.ReasonSucceeded, "ignored"),
			wantState: networkFeedbackReady},
		{name: "Updating", conditions: readyCondition(metav1.ConditionTrue, v1alpha1.ReasonUpdating, "Updating"),
			wantState: networkFeedbackReady, wantMessage: "Updating"},
		{name: "ProvisioningDisabled", conditions: readyCondition(metav1.ConditionTrue, v1alpha1.ReasonProvisioningDisabled, "provider networking is disabled: create_subnet not run"),
			wantState: networkFeedbackReady, wantMessage: "provider networking is disabled: create_subnet not run"},
		{name: "Failed", conditions: readyCondition(metav1.ConditionFalse, v1alpha1.ReasonFailed, "no free VNI"),
			wantState: networkFeedbackFailed, wantMessage: "no free VNI"},
		{name: "deleting, retrying delete", deleting: true, conditions: readyCondition(metav1.ConditionFalse, v1alpha1.ReasonFailed, "x"),
			jobs: []v1alpha1.NetworkJobStatus{
				{Task: "create_subnet", Manager: v1alpha1.NetworkJobManagerFabric, Attempt: 1, State: v1alpha1.NetworkJobStateFailed, Message: "x"},
				{Task: "delete_subnet", Manager: v1alpha1.NetworkJobManagerFabric, Attempt: 2, State: v1alpha1.NetworkJobStateRetrying,
					Message: "delete_subnet (fabric) attempt 2 failed: busy; next retry at 2026-10-01T12:04:00Z"},
			},
			wantState: networkFeedbackDeleting, wantMessage: "delete_subnet (fabric) attempt 2 failed: busy; next retry at 2026-10-01T12:04:00Z"},
		{name: "deleting, running delete", deleting: true,
			jobs:      []v1alpha1.NetworkJobStatus{{Task: "delete_subnet", Manager: v1alpha1.NetworkJobManagerKubernetes, Attempt: 1, State: v1alpha1.NetworkJobStateRunning, JobID: "9"}},
			wantState: networkFeedbackDeleting, wantMessage: "delete_subnet (kubernetes) running in AAP job 9"},
		{name: "deleting, waiting", deleting: true, wantState: networkFeedbackDeleting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subnet := &v1alpha1.Subnet{Status: v1alpha1.SubnetStatus{Conditions: tc.conditions, Jobs: tc.jobs}}
			if tc.deleting {
				subnet.DeletionTimestamp = &now
			}
			state, message := networkFeedback(subnet, subnet.Status.Conditions)
			if state != tc.wantState || message != tc.wantMessage {
				t.Fatalf("got (%v, %q), want (%v, %q)", state, message, tc.wantState, tc.wantMessage)
			}
		})
	}
}

func TestSyncSubnetMessage(t *testing.T) {
	remote := &privatev1.Subnet{}
	remote.SetStatus(&privatev1.SubnetStatus{})
	remote.GetStatus().SetMessage("old")
	obj := &v1alpha1.Subnet{Status: v1alpha1.SubnetStatus{Conditions: readyCondition(metav1.ConditionFalse, v1alpha1.ReasonFailed, "bad\xffsegment")}}
	if err := syncSubnetUpdate(context.Background(), obj, remote); err != nil {
		t.Fatal(err)
	}
	if remote.GetStatus().GetState() != privatev1.SubnetState_SUBNET_STATE_FAILED || remote.GetStatus().GetMessage() != "bad�segment" {
		t.Fatalf("got %v %q", remote.GetStatus().GetState(), remote.GetStatus().GetMessage())
	}
	obj.Status.Conditions = readyCondition(metav1.ConditionTrue, v1alpha1.ReasonSucceeded, "")
	if err := syncSubnetUpdate(context.Background(), obj, remote); err != nil {
		t.Fatal(err)
	}
	if remote.GetStatus().GetState() != privatev1.SubnetState_SUBNET_STATE_READY || remote.GetStatus().HasMessage() {
		t.Fatalf("got %v, message set %v", remote.GetStatus().GetState(), remote.GetStatus().HasMessage())
	}
}

func TestSyncVirtualNetworkDeletingNeverFails(t *testing.T) {
	now := metav1.Now()
	remote := &privatev1.VirtualNetwork{}
	remote.SetStatus(&privatev1.VirtualNetworkStatus{})
	obj := &v1alpha1.VirtualNetwork{Status: v1alpha1.VirtualNetworkStatus{Conditions: readyCondition(metav1.ConditionFalse, v1alpha1.ReasonFailed, "x")}}
	obj.DeletionTimestamp = &now
	if err := syncVirtualNetworkDelete(context.Background(), obj, remote); err != nil {
		t.Fatal(err)
	}
	if remote.GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_DELETING {
		t.Fatalf("state %v, want DELETING", remote.GetStatus().GetState())
	}
}

func TestSyncExternalIPAllocatedOnlyWithAddress(t *testing.T) {
	remote := &privatev1.ExternalIP{}
	remote.SetStatus(&privatev1.ExternalIPStatus{})
	obj := &v1alpha1.ExternalIP{Status: v1alpha1.ExternalIPStatus{Conditions: readyCondition(metav1.ConditionTrue, v1alpha1.ReasonSucceeded, "")}}
	if err := syncExternalIPUpdate(context.Background(), obj, remote); err != nil {
		t.Fatal(err)
	}
	if got := remote.GetStatus().GetState(); got != privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING {
		t.Fatalf("state without address %v, want PENDING", got)
	}
	obj.Status.Address = "198.51.100.19"
	if err := syncExternalIPUpdate(context.Background(), obj, remote); err != nil {
		t.Fatal(err)
	}
	if got := remote.GetStatus().GetState(); got != privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED || remote.GetStatus().GetAddress() != "198.51.100.19" {
		t.Fatalf("state %v address %q, want ALLOCATED", got, remote.GetStatus().GetAddress())
	}
}

// TestSyncExternalIPKeepsDeletingAcceptedByTheAPI covers the window between the API accepting a
// delete (the record is DELETING) and the CR getting its deletion timestamp: the CR still reports
// ALLOCATED, which the API would reject, so the feedback leaves the record alone.
func TestSyncExternalIPKeepsDeletingAcceptedByTheAPI(t *testing.T) {
	remote := &privatev1.ExternalIP{}
	remote.SetStatus(&privatev1.ExternalIPStatus{})
	remote.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING)
	remote.GetStatus().SetStateTransitionTime(timestamppb.New(time.Unix(100, 0)))
	obj := &v1alpha1.ExternalIP{Status: v1alpha1.ExternalIPStatus{
		Conditions: readyCondition(metav1.ConditionTrue, v1alpha1.ReasonSucceeded, ""), Address: "198.51.100.19",
		StateTransitionTime: timePtr(50),
	}}
	if err := syncExternalIPUpdate(context.Background(), obj, remote); err != nil {
		t.Fatal(err)
	}
	if got := remote.GetStatus().GetState(); got != privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING {
		t.Fatalf("state %v, want DELETING", got)
	}
	if got := remote.GetStatus().GetStateTransitionTime().GetSeconds(); got != 100 {
		t.Fatalf("time %d, want 100", got)
	}

	obj.DeletionTimestamp = timePtr(200)
	if err := syncExternalIPDelete(context.Background(), obj, remote); err != nil {
		t.Fatal(err)
	}
	if got := remote.GetStatus().GetState(); got != privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING {
		t.Fatalf("state with a deletion timestamp %v, want DELETING", got)
	}
}

// TestSyncExternalIPUpdatesDeletingRecordOfADeletingCR covers the other half of the guard: a CR that has
// its deletion timestamp reports through the normal update, even when the record is already DELETING.
func TestSyncExternalIPUpdatesDeletingRecordOfADeletingCR(t *testing.T) {
	remote := &privatev1.ExternalIP{}
	remote.SetStatus(&privatev1.ExternalIPStatus{})
	remote.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING)
	obj := &v1alpha1.ExternalIP{Status: v1alpha1.ExternalIPStatus{
		Conditions: readyCondition(metav1.ConditionFalse, v1alpha1.ReasonProgressing, "releasing the address"),
		Address:    "198.51.100.19",
	}}
	obj.DeletionTimestamp = timePtr(200)
	if err := syncExternalIPUpdate(context.Background(), obj, remote); err != nil {
		t.Fatal(err)
	}
	if got := remote.GetStatus().GetAddress(); got != "198.51.100.19" {
		t.Fatalf("address %q: the update of a deleting CR was skipped", got)
	}
}

// TestSyncSecurityGroupWaitsForStoredRules covers the status barrier: the
// record keeps the state the Update set until the stored input holds the
// record's rules, even before the fulfillment controller patched the CR.
func TestSyncSecurityGroupWaitsForStoredRules(t *testing.T) {
	sg := &v1alpha1.SecurityGroup{Spec: v1alpha1.SecurityGroupSpec{VirtualNetwork: "vn-1",
		IngressRules: []v1alpha1.SecurityRule{{Protocol: v1alpha1.SecurityGroupProtocolAll, SourceCIDR: "10.0.0.0/8"}}}}
	sg.Status.Conditions = readyCondition(metav1.ConditionTrue, v1alpha1.ReasonSucceeded, "")
	stored, err := securityGroupResource(sg)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"resource": stored})
	sg.Status.TaskInput = &runtime.RawExtension{Raw: data}

	rule := func(cidr string) []*privatev1.SecurityRule {
		r := &privatev1.SecurityRule{}
		r.SetProtocol(privatev1.Protocol_PROTOCOL_ALL)
		r.SetIpv4Cidr(cidr)
		return []*privatev1.SecurityRule{r}
	}
	remote := &privatev1.SecurityGroup{}
	remote.SetSpec(&privatev1.SecurityGroupSpec{})
	remote.SetStatus(&privatev1.SecurityGroupStatus{})
	remote.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)
	remote.GetStatus().SetMessage("Updating")

	// The tenant Update changed the record; the CR and the stored input still
	// hold the old rules.
	remote.GetSpec().SetIngress(rule("10.1.0.0/16"))
	if err := syncSecurityGroupUpdate(context.Background(), sg, remote); err != nil {
		t.Fatal(err)
	}
	if remote.GetStatus().GetMessage() != "Updating" {
		t.Fatalf("message %q changed before the rules were stored", remote.GetStatus().GetMessage())
	}
	remote.GetSpec().SetIngress(rule("10.0.0.0/8"))
	if err := syncSecurityGroupUpdate(context.Background(), sg, remote); err != nil {
		t.Fatal(err)
	}
	if remote.GetStatus().HasMessage() {
		t.Fatalf("message %q not cleared once Succeeded", remote.GetStatus().GetMessage())
	}
}
