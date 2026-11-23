/*
Copyright (c) 2026 Red Hat Inc.

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

package servers

import (
	"sort"

	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func withSubscriptionStatus(object *publicv1.Subscription, reports ObjectReports) *publicv1.Subscription {
	phase := subscriptionPhase(reports, subscriptionID(object))
	if phase == "" {
		return object
	}
	if object.Status == nil {
		object.Status = &publicv1.SubscriptionStatus{}
	}
	object.Status.Phase = phase
	object.Status.ModelRefStatuses = object.Status.ModelRefStatuses[:0]
	for _, ref := range object.GetSpec().GetModelRefs() {
		ready, pending, failed, missing := subscriptionObjectClusters(reports, ref.GetName())
		if len(ready)+len(pending)+len(failed)+len(missing) == 0 {
			continue
		}
		condition := subscriptionReadyCondition(failed, pending, ready, missing)
		object.Status.ModelRefStatuses = append(object.Status.ModelRefStatuses, &publicv1.ResourceRefStatus{
			Name: ref.GetName(), Namespace: ref.GetNamespace(), Ready: condition.GetStatus() == publicv1.ConditionStatus_CONDITION_STATUS_TRUE,
			Reason: condition.GetReason(), Message: condition.GetMessage(),
		})
	}
	return object
}

func subscriptionPhase(reports ObjectReports, name string) string {
	if len(reports) == 0 {
		return ""
	}
	ready, pending, failed, missing := subscriptionObjectClusters(reports, name)
	switch {
	case len(failed) > 0:
		return "Degraded"
	case len(pending)+len(missing) > 0:
		return "Pending"
	case len(ready) > 0:
		return "Active"
	default:
		return ""
	}
}

func withAuthPolicyStatus(policy *publicv1.AuthPolicy, reports ObjectReports) *publicv1.AuthPolicy {
	if policy == nil || len(reports) == 0 {
		return policy
	}
	ready, pending, failed, missing := subscriptionObjectClusters(reports, policy.GetId())
	condition := subscriptionReadyCondition(failed, pending, ready, missing)
	if condition.GetStatus() == publicv1.ConditionStatus_CONDITION_STATUS_TRUE {
		policy.Phase = "Active"
	} else if condition.GetReason() == "Failed" {
		policy.Phase = "Degraded"
	} else {
		policy.Phase = "Pending"
	}
	policy.Conditions = []*publicv1.SubscriptionCondition{condition}
	return policy
}

func subscriptionModelRegistrations(sub *publicv1.Subscription, namespace string, reports ObjectReports) []*publicv1.ModelRegistration {
	result := make([]*publicv1.ModelRegistration, 0, len(sub.GetSpec().GetModelRefs()))
	for _, ref := range sub.GetSpec().GetModelRefs() {
		refNamespace := ref.GetNamespace()
		if refNamespace == "" {
			refNamespace = namespace
		}
		ready, pending, failed, missing := []string(nil), []string(nil), []string(nil), []string(nil)
		if refNamespace == namespace && len(reports) > 0 {
			ready, pending, failed, missing = subscriptionObjectClusters(reports, ref.GetName())
		}
		registration := &publicv1.ModelRegistration{Name: ref.GetName(), Namespace: refNamespace}
		if len(ready)+len(pending)+len(failed)+len(missing) == 0 {
			registration.Conditions = []*publicv1.SubscriptionCondition{{Type: "Ready", Status: publicv1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED, Reason: proto.String("SavedToGit"), Message: proto.String("Saved to Git; no cluster has reported the model's status")}}
		} else {
			condition := subscriptionReadyCondition(failed, pending, ready, missing)
			registration.Conditions = []*publicv1.SubscriptionCondition{condition}
			switch {
			case condition.GetStatus() == publicv1.ConditionStatus_CONDITION_STATUS_TRUE:
				registration.Phase = "Ready"
			case condition.GetReason() == "Failed":
				registration.Phase = "Failed"
			default:
				registration.Phase = "Pending"
			}
		}
		result = append(result, registration)
	}
	return result
}

func subscriptionReadyCondition(failed, pending, ready, missing []string) *publicv1.SubscriptionCondition {
	condition := &publicv1.SubscriptionCondition{Type: "Ready", Status: publicv1.ConditionStatus_CONDITION_STATUS_TRUE, Reason: proto.String("Ready"), Message: proto.String(subscriptionClusterSummary(failed, pending, ready, missing))}
	switch {
	case len(failed) > 0:
		condition.Status = publicv1.ConditionStatus_CONDITION_STATUS_FALSE
		condition.Reason = proto.String("Failed")
	case len(pending)+len(missing) > 0:
		condition.Status = publicv1.ConditionStatus_CONDITION_STATUS_FALSE
		condition.Reason = proto.String("NotReady")
	}
	return condition
}

func subscriptionClusterSummary(failed, pending, ready, missing []string) string {
	parts := make([]string, 0, 4)
	for _, item := range []struct {
		label string
		names []string
	}{{"failed", failed}, {"pending", pending}, {"ready", ready}, {"not yet reported", missing}} {
		if len(item.names) == 0 {
			continue
		}
		names := append([]string(nil), item.names...)
		sort.Strings(names)
		parts = append(parts, item.label+": "+joinSubscriptionNames(names))
	}
	return joinSubscriptionNames(parts)
}

func joinSubscriptionNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	result := names[0]
	for _, name := range names[1:] {
		result += ", " + name
	}
	return result
}

func subscriptionObjectClusters(reports ObjectReports, name string) (ready, pending, failed, missing []string) {
	for cluster, objects := range reports {
		report, ok := objects[name]
		if !ok {
			missing = append(missing, cluster)
			continue
		}
		if report.State == StateFailed || report.Sync == SyncFailed {
			failed = append(failed, cluster)
		} else if report.State == StateReady && report.Sync == SyncSynced {
			ready = append(ready, cluster)
		} else {
			pending = append(pending, cluster)
		}
	}
	return
}
