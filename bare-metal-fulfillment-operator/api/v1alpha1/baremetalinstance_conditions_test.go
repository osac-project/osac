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

package v1alpha1

import (
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("BareMetalInstance conditions", func() {
	It("refreshes the PowerSynced transition time when the observation changes", func() {
		bmi := &BareMetalInstance{}
		bmi.SetStatusCondition(HostConditionPowerSynced, metav1.ConditionFalse,
			HostConditionReasonPowerReadFailed, "failed to sync power status")
		cond := bmi.GetStatusCondition(HostConditionPowerSynced)
		Expect(cond).NotTo(BeNil())
		cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-time.Hour))

		bmi.SetStatusCondition(HostConditionPowerSynced, metav1.ConditionFalse,
			HostConditionReasonProgressing, "node power state is transitioning")

		cond = bmi.GetStatusCondition(HostConditionPowerSynced)
		Expect(cond).NotTo(BeNil())
		Expect(cond.LastTransitionTime.Time.After(time.Now().Add(-time.Minute))).To(BeTrue())
	})

	It("keeps the PowerSynced transition time for an unchanged observation", func() {
		bmi := &BareMetalInstance{}
		bmi.SetStatusCondition(HostConditionPowerSynced, metav1.ConditionFalse,
			HostConditionReasonProgressing, "node power state is transitioning")
		stored := metav1.NewTime(time.Now().Add(-time.Hour))
		bmi.GetStatusCondition(HostConditionPowerSynced).LastTransitionTime = stored

		bmi.SetStatusCondition(HostConditionPowerSynced, metav1.ConditionFalse,
			HostConditionReasonProgressing, "node power state is transitioning")

		Expect(bmi.GetStatusCondition(HostConditionPowerSynced).LastTransitionTime.Equal(&stored)).To(BeTrue())
	})

	It("keeps the PowerSynced transition time when only the message changes", func() {
		bmi := &BareMetalInstance{}
		bmi.SetStatusCondition(HostConditionPowerSynced, metav1.ConditionFalse,
			HostConditionReasonProgressing, "powering off node")
		stored := metav1.NewTime(time.Now().Add(-time.Hour))
		bmi.GetStatusCondition(HostConditionPowerSynced).LastTransitionTime = stored

		bmi.SetStatusCondition(HostConditionPowerSynced, metav1.ConditionFalse,
			HostConditionReasonProgressing, "node power state is transitioning")

		Expect(bmi.GetStatusCondition(HostConditionPowerSynced).LastTransitionTime.Equal(&stored)).To(BeTrue())
	})
})
