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

package v1alpha1_test

import (
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac-operator/api/v1alpha1"
)

var _ = Describe("NATGatewaySpec", func() {
	It("should accept a valid spec with virtualNetwork and externalIP", func() {
		spec := v1alpha1.NATGatewaySpec{
			VirtualNetwork: "my-vnet",
			ExternalIP:     "my-eip",
		}

		Expect(spec.VirtualNetwork).To(Equal("my-vnet"))
		Expect(spec.ExternalIP).To(Equal("my-eip"))
	})
})

var _ = Describe("NATGatewayPhaseType", func() {
	DescribeTable("should have correct string values",
		func(phase v1alpha1.NATGatewayPhaseType, expected string) {
			Expect(string(phase)).To(Equal(expected))
		},
		Entry("Progressing phase", v1alpha1.NATGatewayPhaseProgressing, "Progressing"),
		Entry("Failed phase", v1alpha1.NATGatewayPhaseFailed, "Failed"),
		Entry("Ready phase", v1alpha1.NATGatewayPhaseReady, "Ready"),
		Entry("Deleting phase", v1alpha1.NATGatewayPhaseDeleting, "Deleting"),
	)
})

var _ = Describe("NATGatewayConditionType", func() {
	It("should have ConfigurationApplied condition type", func() {
		Expect(string(v1alpha1.NATGatewayConditionConfigurationApplied)).To(Equal("ConfigurationApplied"))
	})
})

var _ = Describe("NATGateway", func() {
	Describe("GetName", func() {
		It("should return the name", func() {
			gw := &v1alpha1.NATGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-natgw",
				},
			}

			Expect(gw.GetName()).To(Equal("test-natgw"))
		})
	})
})

var _ = Describe("NATGateway Condition Helpers", func() {
	It("should set and get a condition", func() {
		gw := &v1alpha1.NATGateway{}

		condition := metav1.Condition{
			Type:               string(v1alpha1.NATGatewayConditionConfigurationApplied),
			Status:             metav1.ConditionTrue,
			Reason:             "ConfigurationApplied",
			Message:            "applied",
			LastTransitionTime: metav1.Now(),
		}

		v1alpha1.SetNATGatewayStatusCondition(gw, condition)

		got := v1alpha1.GetNATGatewayStatusCondition(gw, v1alpha1.NATGatewayConditionConfigurationApplied)
		Expect(got).ToNot(BeNil())
		Expect(got.Status).To(Equal(metav1.ConditionTrue))
	})

	It("should return nil for missing condition", func() {
		gw := &v1alpha1.NATGateway{}

		got := v1alpha1.GetNATGatewayStatusCondition(gw, v1alpha1.NATGatewayConditionConfigurationApplied)
		Expect(got).To(BeNil())
	})
})
