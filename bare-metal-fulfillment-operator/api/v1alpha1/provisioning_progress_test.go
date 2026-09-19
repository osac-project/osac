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
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// condTrue builds a True condition of the given type. The reason is irrelevant to
// the derivation once a condition is True, so a fixed placeholder is used.
func condTrue(ct BareMetalInstanceConditionType) metav1.Condition {
	return metav1.Condition{Type: string(ct), Status: metav1.ConditionTrue, Reason: "Succeeded"}
}

// condFalse builds a False condition of the given type carrying the given reason.
func condFalse(ct BareMetalInstanceConditionType, reason string) metav1.Condition {
	return metav1.Condition{Type: string(ct), Status: metav1.ConditionFalse, Reason: reason}
}

// condTrueReason builds a True condition of the given type carrying an explicit reason.
// Used to assert that once PowerSynced is True the derivation is reason-independent:
// the end power state (PowerOn vs PowerOff/Halted) does not change the Ready result.
func condTrueReason(ct BareMetalInstanceConditionType, reason string) metav1.Condition {
	return metav1.Condition{Type: string(ct), Status: metav1.ConditionTrue, Reason: reason}
}

// allProvisioningSteps enumerates every ProvisioningStep constant. Add a new step
// here when one is introduced; the Stage() totality spec asserts each maps to a
// non-empty stage.
var allProvisioningSteps = []ProvisioningStep{
	StepHostAllocation,
	StepProvisioning,
	StepNetworkSetupAttachment,
	StepNetworkSetupHandoff,
	StepNetworkSetupIPDiscovery,
	StepReadyPowerSync,
}

// hostConditionsNotSurfaced are the conditions with no provisioning-stage meaning
// (deprovisioning and host-availability state). This set is test-only: the
// derivation never references it (it simply ignores any condition outside
// hostConditionStageOrder). The exhaustiveness spec pairs it with
// hostConditionStageOrder to prove every HostCondition* constant is classified as
// either surfaced or intentionally not surfaced.
var hostConditionsNotSurfaced = map[BareMetalInstanceConditionType]struct{}{
	HostConditionAvailable:                   {},
	HostConditionDeprovisionTemplateComplete: {},
	HostConditionNetworkOffboardComplete:     {},
}

var _ = Describe("DeriveProvisioningProgress", func() {
	DescribeTable("maps conditions to the furthest-advanced progress",
		func(conds []metav1.Condition, want ProvisioningProgress) {
			Expect(DeriveProvisioningProgress(conds)).To(Equal(want))
		},

		// --- In-progress progression (PROVISIONED axis) ---
		Entry("no conditions -> host allocation in progress",
			[]metav1.Condition{},
			ProvisioningProgress{State: StateInProgress, Step: StepHostAllocation}),
		Entry("Allocated True -> provisioning in progress",
			[]metav1.Condition{condTrue(HostConditionAllocated)},
			ProvisioningProgress{State: StateInProgress, Step: StepProvisioning}),
		Entry("ProvisionTemplateComplete True -> network attachment in progress",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condTrue(HostConditionProvisionTemplateComplete),
			},
			ProvisioningProgress{State: StateInProgress, Step: StepNetworkSetupAttachment}),
		Entry("NetworkAttachmentsReady True -> network handoff in progress",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condTrue(HostConditionProvisionTemplateComplete),
				condTrue(HostConditionNetworkAttachmentsReady),
			},
			ProvisioningProgress{State: StateInProgress, Step: StepNetworkSetupHandoff}),
		Entry("NetworkHandoffComplete True -> IP discovery in progress",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condTrue(HostConditionProvisionTemplateComplete),
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
			},
			ProvisioningProgress{State: StateInProgress, Step: StepNetworkSetupIPDiscovery}),

		// A False condition carrying the in-progress reason is not a failure and
		// does not advance the step.
		Entry("ProvisionTemplateComplete False+Progressing -> still provisioning, not failed",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condFalse(HostConditionProvisionTemplateComplete, HostConditionReasonProgressing),
			},
			ProvisioningProgress{State: StateInProgress, Step: StepProvisioning}),

		// --- Terminal states ---
		Entry("network trio all True -> Provisioned, Ready axis live (power-neutral in-flight step)",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condTrue(HostConditionProvisionTemplateComplete),
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionIPDiscoveryComplete),
			},
			ProvisioningProgress{State: StateProvisioned, Step: StepReadyPowerSync}),
		Entry("no-work variant: network trio trivially True with no earlier conditions -> Provisioned",
			[]metav1.Condition{
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionIPDiscoveryComplete),
			},
			ProvisioningProgress{State: StateProvisioned, Step: StepReadyPowerSync}),
		Entry("Provisioned + PowerSynced False+Progressing -> still Provisioned (power-syncing), not failed",
			[]metav1.Condition{
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionIPDiscoveryComplete),
				condFalse(HostConditionPowerSynced, HostConditionReasonProgressing),
			},
			ProvisioningProgress{State: StateProvisioned, Step: StepReadyPowerSync}),
		// PowerSyncRequired ("a restart is required") is a benign in-flight power
		// transition, not a hard failure: it must not be misclassified as Failed even
		// though it is a non-Progressing False reason.
		Entry("Provisioned + PowerSynced False+PowerSyncRequired -> still Provisioned (restart pending), not failed",
			[]metav1.Condition{
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionIPDiscoveryComplete),
				condFalse(HostConditionPowerSynced, HostConditionReasonPowerSyncRequired),
			},
			ProvisioningProgress{State: StateProvisioned, Step: StepReadyPowerSync}),
		// PowerSynced True is the READY axis: provisioning is complete, nothing is in
		// flight, so Step is empty.
		Entry("PowerSynced True -> Ready, nothing in flight (step empty)",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condTrue(HostConditionProvisionTemplateComplete),
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionIPDiscoveryComplete),
				condTrue(HostConditionPowerSynced),
			},
			ProvisioningProgress{State: StateReady}),
		// The end power state does not change the Ready result: even RunStrategy=Halted
		// (PowerSynced True+PowerOff) yields an empty step, not a misleading terminal step.
		Entry("PowerSynced True+PowerOff (RunStrategy=Halted) -> Ready, step still empty",
			[]metav1.Condition{
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionIPDiscoveryComplete),
				condTrueReason(HostConditionPowerSynced, HostConditionReasonPowerOff),
			},
			ProvisioningProgress{State: StateReady}),

		// --- Order-independence: same logical state, shuffled slice order ---
		Entry("in-progress result is independent of slice order",
			[]metav1.Condition{
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionAllocated),
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionProvisionTemplateComplete),
			},
			ProvisioningProgress{State: StateInProgress, Step: StepNetworkSetupIPDiscovery}),
		Entry("Provisioned result is independent of slice order",
			[]metav1.Condition{
				condTrue(HostConditionIPDiscoveryComplete),
				condTrue(HostConditionAllocated),
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionProvisionTemplateComplete),
				condTrue(HostConditionNetworkAttachmentsReady),
			},
			ProvisioningProgress{State: StateProvisioned, Step: StepReadyPowerSync}),

		// --- Failures ---
		Entry("Allocated False+NoMatchingHosts -> NoMatchingHosts failure",
			[]metav1.Condition{condFalse(HostConditionAllocated, HostConditionReasonNoMatchingHosts)},
			ProvisioningProgress{State: StateFailed, Step: StepHostAllocation, Failure: FailureNoMatchingHosts}),
		Entry("Allocated False+InvalidSelector -> generic host allocation failure",
			[]metav1.Condition{condFalse(HostConditionAllocated, HostConditionReasonInvalidSelector)},
			ProvisioningProgress{State: StateFailed, Step: StepHostAllocation, Failure: FailureHostAllocation}),
		Entry("Allocated False+TemplateFailed -> generic host allocation failure",
			[]metav1.Condition{condFalse(HostConditionAllocated, HostConditionReasonTemplateFailed)},
			ProvisioningProgress{State: StateFailed, Step: StepHostAllocation, Failure: FailureHostAllocation}),
		Entry("ProvisionTemplateComplete False+TemplateFailed -> provision job failure",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condFalse(HostConditionProvisionTemplateComplete, HostConditionReasonTemplateFailed),
			},
			ProvisioningProgress{State: StateFailed, Step: StepProvisioning, Failure: FailureProvisionJob}),
		Entry("NetworkAttachmentsReady False+TemplateFailed -> network attachment failure",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condTrue(HostConditionProvisionTemplateComplete),
				condFalse(HostConditionNetworkAttachmentsReady, HostConditionReasonTemplateFailed),
			},
			ProvisioningProgress{State: StateFailed, Step: StepNetworkSetupAttachment, Failure: FailureNetworkAttachment}),
		// Forward-compat: the operator has no failure reason for NetworkHandoffComplete
		// today (it only ever sets Progressing; handoff failures return an error without
		// stamping the condition), so FailureNetworkHandoff is not yet reachable in
		// production. The reason here is a placeholder standing in for a future hard
		// failure — any non-benign reason exercises the classification path.
		Entry("NetworkHandoffComplete False+failure reason -> network handoff failure",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condTrue(HostConditionProvisionTemplateComplete),
				condTrue(HostConditionNetworkAttachmentsReady),
				condFalse(HostConditionNetworkHandoffComplete, "HandoffFailed"),
			},
			ProvisioningProgress{State: StateFailed, Step: StepNetworkSetupHandoff, Failure: FailureNetworkHandoff}),
		Entry("IPDiscoveryComplete False+TemplateFailed -> IP discovery failure",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condTrue(HostConditionProvisionTemplateComplete),
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
				condFalse(HostConditionIPDiscoveryComplete, HostConditionReasonTemplateFailed),
			},
			ProvisioningProgress{State: StateFailed, Step: StepNetworkSetupIPDiscovery, Failure: FailureIPDiscovery}),
		Entry("PowerSynced False+IronicAPIFailure (trio done) -> ready-axis failure",
			[]metav1.Condition{
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionIPDiscoveryComplete),
				condFalse(HostConditionPowerSynced, HostConditionReasonIronicAPIFailure),
			},
			ProvisioningProgress{State: StateFailed, Step: StepReadyPowerSync, Failure: FailureReadyTimeout}),
		Entry("PowerSynced False+PowerSyncFailed -> ready-axis failure",
			[]metav1.Condition{
				condTrue(HostConditionNetworkAttachmentsReady),
				condTrue(HostConditionNetworkHandoffComplete),
				condTrue(HostConditionIPDiscoveryComplete),
				condFalse(HostConditionPowerSynced, HostConditionReasonPowerSyncFailed),
			},
			ProvisioningProgress{State: StateFailed, Step: StepReadyPowerSync, Failure: FailureReadyTimeout}),

		// --- Failure priority and tie-break ---
		Entry("failure takes priority over the in-progress step",
			[]metav1.Condition{
				condTrue(HostConditionAllocated),
				condFalse(HostConditionProvisionTemplateComplete, HostConditionReasonTemplateFailed),
			},
			ProvisioningProgress{State: StateFailed, Step: StepProvisioning, Failure: FailureProvisionJob}),
		Entry("simultaneous PROVISIONED-axis and READY-axis failures -> PROVISIONED axis wins",
			[]metav1.Condition{
				condFalse(HostConditionPowerSynced, HostConditionReasonIronicAPIFailure),
				condTrue(HostConditionAllocated),
				condFalse(HostConditionProvisionTemplateComplete, HostConditionReasonTemplateFailed),
			},
			ProvisioningProgress{State: StateFailed, Step: StepProvisioning, Failure: FailureProvisionJob}),
		Entry("simultaneous failures within PROVISIONED axis -> earliest stage wins",
			[]metav1.Condition{
				condFalse(HostConditionIPDiscoveryComplete, HostConditionReasonTemplateFailed),
				condTrue(HostConditionAllocated),
				condTrue(HostConditionProvisionTemplateComplete),
				condFalse(HostConditionNetworkAttachmentsReady, HostConditionReasonTemplateFailed),
			},
			ProvisioningProgress{State: StateFailed, Step: StepNetworkSetupAttachment, Failure: FailureNetworkAttachment}),
	)

	It("ignores unrecognized condition types", func() {
		Expect(func() {
			progress := DeriveProvisioningProgress([]metav1.Condition{
				{Type: "SomethingCompletelyUnknown", Status: metav1.ConditionTrue, Reason: "Whatever"},
			})
			Expect(progress).To(Equal(ProvisioningProgress{State: StateInProgress, Step: StepHostAllocation}))
		}).NotTo(Panic())
	})
})

var _ = Describe("ProvisioningStep.Stage", func() {
	DescribeTable("maps each step to its stage",
		func(step ProvisioningStep, want ProvisioningStage) {
			Expect(step.Stage()).To(Equal(want))
		},
		Entry("host allocation", StepHostAllocation, StageHostAllocation),
		Entry("provisioning", StepProvisioning, StageProvisioning),
		Entry("network attachment", StepNetworkSetupAttachment, StageNetworkSetup),
		Entry("network handoff", StepNetworkSetupHandoff, StageNetworkSetup),
		Entry("IP discovery", StepNetworkSetupIPDiscovery, StageNetworkSetup),
		Entry("ready power sync", StepReadyPowerSync, StageReady),
		Entry("unrecognized step -> empty stage", ProvisioningStep("Bogus"), ProvisioningStage("")),
	)

	It("is total over every ProvisioningStep", func() {
		for _, step := range allProvisioningSteps {
			Expect(step.Stage()).NotTo(BeEmpty(), "step %q has no stage; add a case to Stage()", step)
		}
	})
})

var _ = Describe("classifyHostConditionFailure", func() {
	// Defensive default: the failure walk only ever calls this with a condition in
	// hostConditionStageOrder, but an unrecognized type must yield no step or
	// classification rather than a spurious one.
	It("returns no step or classification for an unrecognized condition type", func() {
		step, failure := classifyHostConditionFailure("SomethingUnknown", "AnyReason")
		Expect(step).To(BeEmpty())
		Expect(failure).To(BeEmpty())
	})
})

var _ = Describe("HostCondition classification exhaustiveness", func() {
	It("classifies every HostCondition* constant as exactly one of surfaced / not-surfaced", func() {
		surfaced := map[BareMetalInstanceConditionType]struct{}{}
		for _, ct := range hostConditionStageOrder {
			surfaced[ct] = struct{}{}
		}

		inAll := map[BareMetalInstanceConditionType]struct{}{}
		for _, ct := range AllHostConditionTypes {
			inAll[ct] = struct{}{}
		}

		// Every enumerated constant is classified as exactly one of surfaced /
		// not-surfaced. A new HostCondition* added to AllHostConditionTypes but left
		// unclassified fails here.
		for _, ct := range AllHostConditionTypes {
			_, isSurfaced := surfaced[ct]
			_, isNotSurfaced := hostConditionsNotSurfaced[ct]
			classifications := 0
			if isSurfaced {
				classifications++
			}
			if isNotSurfaced {
				classifications++
			}
			Expect(classifications).To(Equal(1),
				"condition %q must be classified as exactly one of surfaced/not-surfaced", ct)
		}

		// Both classification sets are subsets of the enumeration — no stray or
		// duplicate entries.
		Expect(inAll).To(HaveLen(len(AllHostConditionTypes)), "AllHostConditionTypes contains duplicates")
		for _, ct := range hostConditionStageOrder {
			Expect(inAll).To(HaveKey(ct), "surfaced condition %q missing from AllHostConditionTypes", ct)
		}
		for ct := range hostConditionsNotSurfaced {
			Expect(inAll).To(HaveKey(ct), "not-surfaced condition %q missing from AllHostConditionTypes", ct)
		}

		// The two sets partition the enumeration exactly.
		Expect(AllHostConditionTypes).To(HaveLen(len(hostConditionStageOrder)+len(hostConditionsNotSurfaced)),
			"surfaced + not-surfaced must cover AllHostConditionTypes exactly")
	})

	// Backstop for a newly-added HostCondition* constant: bump this count and
	// classify the new constant. A pure compile-time enumeration of consts is not
	// possible in Go, so this length gate plus review is the guard for AC-5.
	It("enumerates every HostCondition* constant defined in the package", func() {
		Expect(AllHostConditionTypes).To(HaveLen(9))
	})
})
