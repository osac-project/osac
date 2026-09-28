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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("FabricDomain feedback mapping", func() {
	It("syncs backend identifiers, Ready condition, and active members", func() {
		lastTransition := metav1.Now()
		object := &v1alpha1.FabricDomain{
			Status: v1alpha1.FabricDomainStatus{
				Phase:     v1alpha1.FabricDomainPhaseReady,
				BackendID: "cluster-42",
				VPCID:     "vpc-7",
				Conditions: []metav1.Condition{{
					Type: v1alpha1.ConditionReady, Status: metav1.ConditionTrue,
					Reason: "AsExpected", Message: "provisioned", LastTransitionTime: lastTransition,
				}},
				Members: []v1alpha1.FabricDomainMemberStatus{{
					Server: "server-a", State: v1alpha1.FabricDomainMemberStateActive,
				}},
			},
		}
		remote := privatev1.FabricDomain_builder{Status: privatev1.FabricDomainStatus_builder{}.Build()}.Build()

		Expect(syncFabricDomainUpdate(context.Background(), object, remote)).To(Succeed())
		Expect(remote.GetStatus().GetBackendId()).To(Equal("cluster-42"))
		Expect(remote.GetStatus().GetVpcId()).To(Equal("vpc-7"))
		Expect(remote.GetStatus().GetConditions()).To(HaveLen(1))
		Expect(remote.GetStatus().GetConditions()[0].GetType()).To(Equal(privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_READY))
		Expect(remote.GetStatus().GetConditions()[0].GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		Expect(remote.GetStatus().GetMembers()).To(HaveLen(1))
		Expect(remote.GetStatus().GetMembers()[0].GetState()).To(Equal(privatev1.FabricDomainMemberState_FABRIC_DOMAIN_MEMBER_STATE_ACTIVE))
	})

	It("syncs failed conditions and member messages", func() {
		object := &v1alpha1.FabricDomain{
			Status: v1alpha1.FabricDomainStatus{
				Phase: v1alpha1.FabricDomainPhaseFailed,
				Conditions: []metav1.Condition{{
					Type: v1alpha1.ConditionReady, Status: metav1.ConditionFalse,
					Reason: "ProvisioningFailed", Message: "Netris rejected the request",
				}},
				Members: []v1alpha1.FabricDomainMemberStatus{{
					Server: "server-a", State: v1alpha1.FabricDomainMemberStateFailed, Message: "Netris rejected the request",
				}},
			},
		}
		remote := privatev1.FabricDomain_builder{Status: privatev1.FabricDomainStatus_builder{}.Build()}.Build()

		Expect(syncFabricDomainUpdate(context.Background(), object, remote)).To(Succeed())
		Expect(remote.GetStatus().GetConditions()[0].GetType()).To(Equal(privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_FAILED))
		Expect(remote.GetStatus().GetConditions()[0].GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(remote.GetStatus().GetConditions()[0].GetMessage()).To(Equal("Netris rejected the request"))
		Expect(remote.GetStatus().GetMembers()[0].GetMessage()).To(Equal("Netris rejected the request"))
	})
})
