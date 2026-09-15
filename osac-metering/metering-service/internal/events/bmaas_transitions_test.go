package events_test

import (
	"errors"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/internal/events"
)

var _ = Describe("BMaaS meter transition contracts", func() {
	It("classifies allocation and consumption billable states independently", func() {
		for _, state := range []string{"RUNNING", "STOPPED", "STARTING", "STOPPING", "DELETING"} {
			Expect(events.IsAllocationBillableState(state)).To(BeTrue(), state)
		}
		for _, state := range []string{"PROVISIONING", "FAILED", "UNSPECIFIED"} {
			Expect(events.IsAllocationBillableState(state)).To(BeFalse(), state)
		}

		Expect(events.IsConsumptionBillableState("RUNNING")).To(BeTrue())
		for _, state := range []string{"PROVISIONING", "STOPPED", "STARTING", "STOPPING", "DELETING", "FAILED", "UNSPECIFIED"} {
			Expect(events.IsConsumptionBillableState(state)).To(BeFalse(), state)
		}
	})

	DescribeTable("resolves every accepted allocation state pair",
		func(from, to, expected string) {
			result, err := events.ResolveAllocationTransition(from, to)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(expected))
		},
		Entry("initial provisioning", "", "PROVISIONING", events.BMaaSEffectSkip),
		Entry("initial running", "", "RUNNING", events.BMaaSEffectStart),
		Entry("initial stopped", "", "STOPPED", events.BMaaSEffectStart),
		Entry("initial starting", "", "STARTING", events.BMaaSEffectStart),
		Entry("initial stopping", "", "STOPPING", events.BMaaSEffectStart),
		Entry("initial failed", "", "FAILED", events.BMaaSEffectSkip),
		Entry("initial deleting", "", "DELETING", events.BMaaSEffectStart),
		Entry("initial unspecified", "", "UNSPECIFIED", events.BMaaSEffectSkip),
		Entry("provisioning repeated", "PROVISIONING", "PROVISIONING", events.BMaaSEffectSkip),
		Entry("provisioning to running", "PROVISIONING", "RUNNING", events.BMaaSEffectStart),
		Entry("provisioning to stopped", "PROVISIONING", "STOPPED", events.BMaaSEffectStart),
		Entry("provisioning to starting", "PROVISIONING", "STARTING", events.BMaaSEffectStart),
		Entry("provisioning to stopping", "PROVISIONING", "STOPPING", events.BMaaSEffectStart),
		Entry("provisioning to failed", "PROVISIONING", "FAILED", events.BMaaSEffectSkip),
		Entry("provisioning to deleting", "PROVISIONING", "DELETING", events.BMaaSEffectSkip),
		Entry("running repeated", "RUNNING", "RUNNING", events.BMaaSEffectSkip),
		Entry("running to stopped", "RUNNING", "STOPPED", events.BMaaSEffectSkip),
		Entry("running to starting", "RUNNING", "STARTING", events.BMaaSEffectSkip),
		Entry("running to stopping", "RUNNING", "STOPPING", events.BMaaSEffectSkip),
		Entry("running to failed", "RUNNING", "FAILED", events.BMaaSEffectSuspend),
		Entry("running to deleting", "RUNNING", "DELETING", events.BMaaSEffectSkip),
		Entry("stopped repeated", "STOPPED", "STOPPED", events.BMaaSEffectSkip),
		Entry("stopped to running", "STOPPED", "RUNNING", events.BMaaSEffectSkip),
		Entry("stopped to starting", "STOPPED", "STARTING", events.BMaaSEffectSkip),
		Entry("stopped to failed", "STOPPED", "FAILED", events.BMaaSEffectSuspend),
		Entry("stopped to deleting", "STOPPED", "DELETING", events.BMaaSEffectSkip),
		Entry("starting repeated", "STARTING", "STARTING", events.BMaaSEffectSkip),
		Entry("starting to running", "STARTING", "RUNNING", events.BMaaSEffectSkip),
		Entry("starting to stopped", "STARTING", "STOPPED", events.BMaaSEffectSkip),
		Entry("starting to failed", "STARTING", "FAILED", events.BMaaSEffectSuspend),
		Entry("starting to deleting", "STARTING", "DELETING", events.BMaaSEffectSkip),
		Entry("stopping repeated", "STOPPING", "STOPPING", events.BMaaSEffectSkip),
		Entry("stopping to stopped", "STOPPING", "STOPPED", events.BMaaSEffectSkip),
		Entry("stopping to running", "STOPPING", "RUNNING", events.BMaaSEffectSkip),
		Entry("stopping to failed", "STOPPING", "FAILED", events.BMaaSEffectSuspend),
		Entry("stopping to deleting", "STOPPING", "DELETING", events.BMaaSEffectSkip),
		Entry("failed repeated", "FAILED", "FAILED", events.BMaaSEffectSkip),
		Entry("failed to running", "FAILED", "RUNNING", events.BMaaSEffectResume),
		Entry("failed to deleting", "FAILED", "DELETING", events.BMaaSEffectSkip),
		Entry("deleting repeated", "DELETING", "DELETING", events.BMaaSEffectSkip),
	)

	DescribeTable("resolves every accepted consumption state pair",
		func(from, to, expected string) {
			result, err := events.ResolveConsumptionTransition(from, to)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(expected))
		},
		Entry("initial provisioning", "", "PROVISIONING", events.BMaaSEffectSkip),
		Entry("initial running", "", "RUNNING", events.BMaaSEffectStart),
		Entry("initial stopped", "", "STOPPED", events.BMaaSEffectSkip),
		Entry("initial starting", "", "STARTING", events.BMaaSEffectSkip),
		Entry("initial stopping", "", "STOPPING", events.BMaaSEffectSkip),
		Entry("initial failed", "", "FAILED", events.BMaaSEffectSkip),
		Entry("initial deleting", "", "DELETING", events.BMaaSEffectSkip),
		Entry("initial unspecified", "", "UNSPECIFIED", events.BMaaSEffectSkip),
		Entry("provisioning repeated", "PROVISIONING", "PROVISIONING", events.BMaaSEffectSkip),
		Entry("provisioning to running", "PROVISIONING", "RUNNING", events.BMaaSEffectStart),
		Entry("provisioning to stopped", "PROVISIONING", "STOPPED", events.BMaaSEffectSkip),
		Entry("provisioning to starting", "PROVISIONING", "STARTING", events.BMaaSEffectSkip),
		Entry("provisioning to stopping", "PROVISIONING", "STOPPING", events.BMaaSEffectSkip),
		Entry("provisioning to failed", "PROVISIONING", "FAILED", events.BMaaSEffectSkip),
		Entry("provisioning to deleting", "PROVISIONING", "DELETING", events.BMaaSEffectSkip),
		Entry("running repeated", "RUNNING", "RUNNING", events.BMaaSEffectSkip),
		Entry("running to stopped", "RUNNING", "STOPPED", events.BMaaSEffectSuspend),
		Entry("running to starting", "RUNNING", "STARTING", events.BMaaSEffectSuspend),
		Entry("running to stopping", "RUNNING", "STOPPING", events.BMaaSEffectSuspend),
		Entry("running to failed", "RUNNING", "FAILED", events.BMaaSEffectSuspend),
		Entry("running to deleting", "RUNNING", "DELETING", events.BMaaSEffectSuspend),
		Entry("stopped repeated", "STOPPED", "STOPPED", events.BMaaSEffectSkip),
		Entry("stopped to running", "STOPPED", "RUNNING", events.BMaaSEffectStart),
		Entry("stopped to starting", "STOPPED", "STARTING", events.BMaaSEffectSkip),
		Entry("stopped to failed", "STOPPED", "FAILED", events.BMaaSEffectSkip),
		Entry("stopped to deleting", "STOPPED", "DELETING", events.BMaaSEffectSkip),
		Entry("starting repeated", "STARTING", "STARTING", events.BMaaSEffectSkip),
		Entry("starting to running", "STARTING", "RUNNING", events.BMaaSEffectStart),
		Entry("starting to stopped", "STARTING", "STOPPED", events.BMaaSEffectSkip),
		Entry("starting to failed", "STARTING", "FAILED", events.BMaaSEffectSkip),
		Entry("starting to deleting", "STARTING", "DELETING", events.BMaaSEffectSkip),
		Entry("stopping repeated", "STOPPING", "STOPPING", events.BMaaSEffectSkip),
		Entry("stopping to stopped", "STOPPING", "STOPPED", events.BMaaSEffectSkip),
		Entry("stopping to running", "STOPPING", "RUNNING", events.BMaaSEffectStart),
		Entry("stopping to failed", "STOPPING", "FAILED", events.BMaaSEffectSkip),
		Entry("stopping to deleting", "STOPPING", "DELETING", events.BMaaSEffectSkip),
		Entry("failed repeated", "FAILED", "FAILED", events.BMaaSEffectSkip),
		Entry("failed to running", "FAILED", "RUNNING", events.BMaaSEffectStart),
		Entry("failed to deleting", "FAILED", "DELETING", events.BMaaSEffectSkip),
		Entry("deleting repeated", "DELETING", "DELETING", events.BMaaSEffectSkip),
	)

	It("rejects an unregistered state pair instead of applying a wildcard", func() {
		_, err := events.ResolveAllocationTransition("RUNNING", "PROVISIONING")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrInvalidBMaaSTransition)).To(BeTrue())
	})
})

var _ = Describe("DecomposeBMIEvents", func() {
	var (
		transitionTime   = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
		allocationSince  = time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
		consumptionSince = time.Date(2026, 9, 14, 11, 30, 0, 0, time.UTC)
	)

	build := func(request events.BMaaSEventBuildRequest) (cloudevents.Event, error) {
		ce := cloudevents.NewEvent()
		ce.SetID(request.EventID)
		ce.SetType(request.EventType)
		if err := ce.SetData(cloudevents.ApplicationJSON, request); err != nil {
			return ce, err
		}
		return ce, nil
	}

	It("emits independent allocation and consumption events with independent durations and IDs", func() {
		eventsOut, err := events.DecomposeBMIEvents(
			map[string]any{"bm_instance_type": "bm.large"},
			"evt-running",
			transitionTime,
			events.BMaaSMeterIntervals{AllocationSince: &allocationSince, ConsumptionSince: &consumptionSince},
			build,
			events.EventStarted,
			events.EventStarted,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(eventsOut).To(HaveLen(2))
		Expect(eventsOut[0].ID()).To(Equal("evt-running/allocation"))
		Expect(eventsOut[1].ID()).To(Equal("evt-running/consumption"))
		Expect(eventsOut[0].Type()).To(Equal(events.EventStarted))
		Expect(eventsOut[1].Type()).To(Equal(events.EventStarted))

		var allocationRequest, consumptionRequest events.BMaaSEventBuildRequest
		Expect(eventsOut[0].DataAs(&allocationRequest)).To(Succeed())
		Expect(eventsOut[1].DataAs(&consumptionRequest)).To(Succeed())
		Expect(allocationRequest.MeterType).To(Equal(events.BMaaSMeterAllocation))
		Expect(consumptionRequest.MeterType).To(Equal(events.BMaaSMeterConsumption))
		Expect(*allocationRequest.DurationSeconds).To(Equal(3600.0))
		Expect(*consumptionRequest.DurationSeconds).To(Equal(1800.0))
	})

	It("emits only active meter closures", func() {
		eventsOut, err := events.DecomposeBMIEvents(
			map[string]any{"bm_instance_type": "bm.large"},
			"evt-delete",
			transitionTime,
			events.BMaaSMeterIntervals{AllocationSince: &allocationSince},
			build,
			events.EventSuspended,
			events.EventSuspended,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(eventsOut).To(HaveLen(1))
		Expect(eventsOut[0].ID()).To(Equal("evt-delete/allocation"))
	})

	It("emits no events when neither meter crossed a boundary", func() {
		eventsOut, err := events.DecomposeBMIEvents(
			map[string]any{"bm_instance_type": "bm.large"},
			"evt-stop-transition",
			transitionTime,
			events.BMaaSMeterIntervals{},
			build,
			"",
			"",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(eventsOut).To(BeEmpty())
	})
})
