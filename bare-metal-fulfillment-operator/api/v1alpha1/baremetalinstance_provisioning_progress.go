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

// This file defines the operator-owned provisioning-progress vocabulary and the
// pure DeriveProvisioningProgress function that maps a BareMetalInstance's
// lifecycle conditions to a machine-readable progress value. It returns enums
// only — no display text. The fulfillment reconciler (OSAC-5347) owns the curated
// message strings keyed off these enums.

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProvisioningState is the single lifecycle discriminator for a BareMetalInstance's
// provisioning progress. It replaces the Provisioned/Ready/Failed booleans: the
// lifecycle is sequential, so one discriminator removes illegal combinations (e.g.
// Ready without Provisioned).
type ProvisioningState string

const (
	// StateInProgress means the provisioning axis is still live (PROVISIONED False).
	StateInProgress ProvisioningState = "InProgress"
	// StateProvisioned means PROVISIONED is True and READY is False; the Ready axis is live.
	StateProvisioned ProvisioningState = "Provisioned"
	// StateReady means READY is True; all four steps are done.
	StateReady ProvisioningState = "Ready"
	// StateFailed means a stage failed; see ProvisioningProgress.Failure.
	StateFailed ProvisioningState = "Failed"
)

// ProvisioningStage is a user-facing step and the value stamped as the live
// condition's reason: PROVISIONED for the first three stages, READY for Ready.
type ProvisioningStage string

const (
	// StageHostAllocation is the Host Allocation stage (PROVISIONED axis).
	StageHostAllocation ProvisioningStage = "HostAllocation"
	// StageProvisioning is the Provisioning stage (PROVISIONED axis).
	StageProvisioning ProvisioningStage = "Provisioning"
	// StageNetworkSetup is the Network Setup stage (PROVISIONED axis).
	StageNetworkSetup ProvisioningStage = "NetworkSetup"
	// StageReady is the Ready stage (READY axis).
	StageReady ProvisioningStage = "Ready"
)

// ProvisioningStep is the fine-grained milestone in flight. The identifier encodes
// its stage so the grouping is self-evident and future additions self-classify.
type ProvisioningStep string

const (
	// StepHostAllocation is the Host Allocation step.
	StepHostAllocation ProvisioningStep = "HostAllocation"
	// StepProvisioning is the Provisioning step.
	StepProvisioning ProvisioningStep = "Provisioning"
	// StepNetworkSetupAttachment is the network-attachment step of Network Setup.
	StepNetworkSetupAttachment ProvisioningStep = "NetworkSetupAttachment"
	// StepNetworkSetupHandoff is the network-handoff step of Network Setup.
	StepNetworkSetupHandoff ProvisioningStep = "NetworkSetupHandoff"
	// StepNetworkSetupIPDiscovery is the IP-discovery step of Network Setup.
	StepNetworkSetupIPDiscovery ProvisioningStep = "NetworkSetupIPDiscovery"
	// StepReadyPowerSync is the in-flight power-sync milestone of the Ready stage: the
	// host is converging to its desired power state (PROVISIONED is True, READY is not
	// yet True). It is the only Ready-stage step because Step names in-flight work; once
	// READY is True nothing is in flight and Step is empty. The desired end power state
	// (on, or off for RunStrategy=Halted) is steady-state, not provisioning progress, so
	// it is deliberately not encoded here.
	StepReadyPowerSync ProvisioningStep = "ReadyPowerSync"
)

// FailureClassification is the machine-readable failure vocabulary returned by
// DeriveProvisioningProgress (never a display string). Each value maps exactly one
// failing condition/reason to a curated message and carrier condition in the
// fulfillment presentation layer; the mapping is fixed and deterministic.
type FailureClassification string

const (
	// FailureNoMatchingHosts means no host matched the selector (PROVISIONED axis).
	FailureNoMatchingHosts FailureClassification = "NoMatchingHosts"
	// FailureHostAllocation means host allocation failed for any other reason (PROVISIONED axis).
	FailureHostAllocation FailureClassification = "HostAllocationFailed"
	// FailureProvisionJob means the provision template workflow failed (PROVISIONED axis).
	FailureProvisionJob FailureClassification = "ProvisionJobFailed"
	// FailureNetworkAttachment means network attachment failed (PROVISIONED axis).
	FailureNetworkAttachment FailureClassification = "NetworkAttachmentFailed"
	// FailureNetworkHandoff means the network handoff reboot failed (PROVISIONED axis).
	FailureNetworkHandoff FailureClassification = "NetworkHandoffFailed"
	// FailureIPDiscovery means DHCP lease discovery failed (PROVISIONED axis).
	FailureIPDiscovery FailureClassification = "IPDiscoveryFailed"
	// FailureReadyTimeout means the host never reached the ready power state (READY axis).
	FailureReadyTimeout FailureClassification = "ReadyTimeout"
)

// Stage returns the user-facing stage a step belongs to. It is total over the
// defined steps, with StepReadyPowerSync -> StageReady. The result is the reason for
// the currently-live condition (PROVISIONED while InProgress, READY once Provisioned).
// The empty step (State Ready, nothing in flight) maps to the empty stage.
func (s ProvisioningStep) Stage() ProvisioningStage {
	switch s {
	case StepHostAllocation:
		return StageHostAllocation
	case StepProvisioning:
		return StageProvisioning
	case StepNetworkSetupAttachment, StepNetworkSetupHandoff, StepNetworkSetupIPDiscovery:
		return StageNetworkSetup
	case StepReadyPowerSync:
		return StageReady
	default:
		return ""
	}
}

// ProvisioningProgress is the result of DeriveProvisioningProgress. It carries
// machine-readable enums only; the fulfillment reconciler (OSAC-5347) maps them to
// curated message text and the carrier condition.
//
// State and Step describe orthogonal axes. State is a lifecycle discriminator that
// mirrors the two carrier conditions (InProgress = PROVISIONED False, Provisioned =
// PROVISIONED True/READY False, Ready = READY True). Step names the granular work in
// flight. They can legitimately differ: while State is Provisioned the in-flight step
// is already the Ready stage (the host is syncing to its desired power state), so
// Step.Stage() returns StageReady before State reaches Ready. Once State is Ready
// nothing is in flight, so Step is empty.
type ProvisioningProgress struct {
	// State is the lifecycle discriminator; always set.
	State ProvisioningState
	// Step is the milestone in flight (State InProgress/Provisioned) or the milestone
	// that failed (State Failed). It is empty when State is Ready — provisioning is
	// complete and nothing is in flight. It keys the curated message in fulfillment.
	Step ProvisioningStep
	// Failure is the classification when State == StateFailed; empty otherwise.
	Failure FailureClassification
}

// hostConditionStageOrder is the fixed, ordered list of the conditions surfaced as
// provisioning progress, from earliest to latest. Two independent walks use it:
// in-progress selection takes the furthest-advanced True entry, and failure
// detection takes the earliest hard failure — so failures on the PROVISIONED axis
// (the first five) win over a READY-axis failure (PowerSynced), then stage order.
//
// NetworkHandoffComplete precedes IPDiscoveryComplete because the tenant-network
// reboot must finish before DHCP lease discovery can run.
var hostConditionStageOrder = []BareMetalInstanceConditionType{
	HostConditionAllocated,
	HostConditionProvisionTemplateComplete,
	HostConditionNetworkAttachmentsReady,
	HostConditionNetworkHandoffComplete,
	HostConditionIPDiscoveryComplete,
	HostConditionPowerSynced,
}

// AllHostConditionTypes lists every exported HostCondition* constant. The
// exhaustiveness test iterates this slice to assert each constant is classified by
// the derivation (surfaced or not-surfaced). Add a new HostCondition* here when one
// is introduced, or the exhaustiveness test's count assertion fails.
var AllHostConditionTypes = []BareMetalInstanceConditionType{
	HostConditionAllocated,
	HostConditionAvailable,
	HostConditionPowerSynced,
	HostConditionProvisionTemplateComplete,
	HostConditionDeprovisionTemplateComplete,
	HostConditionNetworkAttachmentsReady,
	HostConditionIPDiscoveryComplete,
	HostConditionNetworkHandoffComplete,
	HostConditionNetworkOffboardComplete,
}

// DeriveProvisioningProgress maps a BareMetalInstance's lifecycle conditions to the
// furthest-advanced provisioning progress, order-independently (each condition is
// read by type, never by slice position). It is the authoritative,
// operator-owned condition->progress contract, co-located with the HostCondition*
// constants it reads, and returns machine-readable enums only. The fulfillment
// reconciler (OSAC-5347) calls this and applies the presentation layer.
//
// Precedence: failure (earliest in stage order) > Ready > Provisioned > in-progress.
func DeriveProvisioningProgress(conds []metav1.Condition) ProvisioningProgress {
	// 1. Failure takes priority. Walk the surfaced conditions in stage order; the
	//    first one that is False while carrying a hard-failure reason wins.
	//
	//    Failure detection is a denylist: a False condition is a hard failure unless
	//    its reason is a known benign one. Benign reasons are the in-progress marker
	//    (Progressing), the empty reason, and PowerSyncRequired — a restart is
	//    required, a normal in-flight power transition, not a provisioning failure.
	//    A new benign False reason MUST be added to this skip set, or it will be
	//    misclassified as a hard failure (the exhaustiveness test guards condition
	//    types, not reason values).
	for _, ct := range hostConditionStageOrder {
		cond := apimeta.FindStatusCondition(conds, string(ct))
		if cond == nil || cond.Status != metav1.ConditionFalse {
			continue
		}
		if cond.Reason == HostConditionReasonProgressing ||
			cond.Reason == HostConditionReasonPowerSyncRequired ||
			cond.Reason == "" {
			continue
		}
		step, failure := classifyHostConditionFailure(ct, cond.Reason)
		return ProvisioningProgress{State: StateFailed, Step: step, Failure: failure}
	}

	// 2. Ready: PowerSynced True is the READY axis — provisioning is complete and the
	//    host has reached its desired power state. Nothing is in flight, so Step is
	//    empty. The end power state (on, or off for RunStrategy=Halted) is steady-state,
	//    not provisioning progress; callers read the PowerSynced condition directly if a
	//    message needs it.
	if apimeta.IsStatusConditionTrue(conds, string(HostConditionPowerSynced)) {
		return ProvisioningProgress{State: StateReady}
	}

	// 3. Provisioned: the network trio all True marks PROVISIONED True; the READY axis
	//    is now in flight (the host is converging to its desired power state), so the
	//    in-flight step is StepReadyPowerSync.
	if apimeta.IsStatusConditionTrue(conds, string(HostConditionNetworkAttachmentsReady)) &&
		apimeta.IsStatusConditionTrue(conds, string(HostConditionNetworkHandoffComplete)) &&
		apimeta.IsStatusConditionTrue(conds, string(HostConditionIPDiscoveryComplete)) {
		return ProvisioningProgress{State: StateProvisioned, Step: StepReadyPowerSync}
	}

	// 4. In progress: the furthest-advanced True condition selects the step in flight.
	//    Later stages override earlier ones, so the furthest True wins.
	step := StepHostAllocation
	if apimeta.IsStatusConditionTrue(conds, string(HostConditionAllocated)) {
		step = StepProvisioning
	}
	if apimeta.IsStatusConditionTrue(conds, string(HostConditionProvisionTemplateComplete)) {
		step = StepNetworkSetupAttachment
	}
	if apimeta.IsStatusConditionTrue(conds, string(HostConditionNetworkAttachmentsReady)) {
		step = StepNetworkSetupHandoff
	}
	if apimeta.IsStatusConditionTrue(conds, string(HostConditionNetworkHandoffComplete)) {
		step = StepNetworkSetupIPDiscovery
	}
	return ProvisioningProgress{State: StateInProgress, Step: step}
}

// classifyHostConditionFailure maps a surfaced condition (and, for Host Allocation,
// its operator reason) to the failed step and the corresponding FailureClassification.
// Only HostConditionAllocated needs the reason, to distinguish "no host matched the
// selector" from any other allocation failure.
func classifyHostConditionFailure(
	ct BareMetalInstanceConditionType, reason string,
) (ProvisioningStep, FailureClassification) {
	switch ct {
	case HostConditionAllocated:
		if reason == HostConditionReasonNoMatchingHosts {
			return StepHostAllocation, FailureNoMatchingHosts
		}
		return StepHostAllocation, FailureHostAllocation
	case HostConditionProvisionTemplateComplete:
		return StepProvisioning, FailureProvisionJob
	case HostConditionNetworkAttachmentsReady:
		return StepNetworkSetupAttachment, FailureNetworkAttachment
	case HostConditionNetworkHandoffComplete:
		return StepNetworkSetupHandoff, FailureNetworkHandoff
	case HostConditionIPDiscoveryComplete:
		return StepNetworkSetupIPDiscovery, FailureIPDiscovery
	case HostConditionPowerSynced:
		return StepReadyPowerSync, FailureReadyTimeout
	default:
		return "", ""
	}
}
