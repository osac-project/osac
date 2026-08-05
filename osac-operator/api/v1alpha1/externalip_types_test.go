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

var _ = Describe("ExternalIPSpec", func() {
	It("should accept a valid spec with pool", func() {
		spec := v1alpha1.ExternalIPSpec{
			Pool: "my-pool",
		}

		Expect(spec.Pool).To(Equal("my-pool"))
	})
})

var _ = Describe("ExternalIPPhaseType", func() {
	DescribeTable("should have correct string values",
		func(phase v1alpha1.ExternalIPPhaseType, expected string) {
			Expect(string(phase)).To(Equal(expected))
		},
		Entry("Progressing phase", v1alpha1.ExternalIPPhaseProgressing, "Progressing"),
		Entry("Failed phase", v1alpha1.ExternalIPPhaseFailed, "Failed"),
		Entry("Ready phase", v1alpha1.ExternalIPPhaseReady, "Ready"),
		Entry("Deleting phase", v1alpha1.ExternalIPPhaseDeleting, "Deleting"),
	)
})

var _ = Describe("ExternalIPStateType", func() {
	DescribeTable("should have correct string values",
		func(state v1alpha1.ExternalIPStateType, expected string) {
			Expect(string(state)).To(Equal(expected))
		},
		Entry("Pending state", v1alpha1.ExternalIPStatePending, "Pending"),
		Entry("Allocated state", v1alpha1.ExternalIPStateAllocated, "Allocated"),
		Entry("Failed state", v1alpha1.ExternalIPStateFailed, "Failed"),
	)
})

var _ = Describe("ExternalIPConditionType", func() {
	It("should have ConfigurationApplied condition type", func() {
		Expect(string(v1alpha1.ExternalIPConditionConfigurationApplied)).To(Equal("ConfigurationApplied"))
	})
})

var _ = Describe("ExternalIP", func() {
	Describe("GetName", func() {
		It("should return the name", func() {
			ip := &v1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ip",
				},
			}

			Expect(ip.GetName()).To(Equal("test-ip"))
		})
	})
})

var _ = Describe("ExternalIP Condition Helpers", func() {
	It("should set and get a condition", func() {
		ip := &v1alpha1.ExternalIP{}

		condition := metav1.Condition{
			Type:               string(v1alpha1.ExternalIPConditionConfigurationApplied),
			Status:             metav1.ConditionTrue,
			Reason:             "ConfigurationApplied",
			Message:            "applied",
			LastTransitionTime: metav1.Now(),
		}

		v1alpha1.SetExternalIPStatusCondition(ip, condition)

		got := v1alpha1.GetExternalIPStatusCondition(ip, v1alpha1.ExternalIPConditionConfigurationApplied)
		Expect(got).ToNot(BeNil())
		Expect(got.Status).To(Equal(metav1.ConditionTrue))
	})

	It("should return nil for missing condition", func() {
		ip := &v1alpha1.ExternalIP{}

		got := v1alpha1.GetExternalIPStatusCondition(ip, v1alpha1.ExternalIPConditionConfigurationApplied)
		Expect(got).To(BeNil())
	})
})
