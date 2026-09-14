package events

import (
	"errors"
	"fmt"
	"maps"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

const (
	BMaaSMeterAllocation  = "allocation"
	BMaaSMeterConsumption = "consumption"

	BMaaSEffectStart   = "start"
	BMaaSEffectResume  = "resume"
	BMaaSEffectSuspend = "suspend"
	BMaaSEffectSkip    = "skip"
)

var ErrInvalidBMaaSTransition = errors.New("invalid BMaaS state transition")

const (
	bmaasStateProvisioning = "PROVISIONING"
	bmaasStateRunning      = "RUNNING"
	bmaasStateStopped      = "STOPPED"
	bmaasStateStarting     = "STARTING"
	bmaasStateStopping     = "STOPPING"
	bmaasStateFailed       = "FAILED"
	bmaasStateDeleting     = "DELETING"
	bmaasStateUnspecified  = "UNSPECIFIED"
)

func IsAllocationBillableState(state string) bool {
	switch state {
	case bmaasStateRunning, bmaasStateStopped, bmaasStateStarting, bmaasStateStopping, bmaasStateDeleting:
		return true
	default:
		return false
	}
}

func IsConsumptionBillableState(state string) bool {
	return state == bmaasStateRunning
}

type bmaasTransitionKey struct {
	from string
	to   string
}

var acceptedBMaaSTransitions = map[bmaasTransitionKey]struct{}{
	{"", bmaasStateProvisioning}: {}, {"", bmaasStateRunning}: {}, {"", bmaasStateStopped}: {},
	{"", bmaasStateStarting}: {}, {"", bmaasStateStopping}: {}, {"", bmaasStateFailed}: {},
	{"", bmaasStateDeleting}: {}, {"", bmaasStateUnspecified}: {},
	{bmaasStateProvisioning, bmaasStateProvisioning}: {}, {bmaasStateProvisioning, bmaasStateRunning}: {},
	{bmaasStateProvisioning, bmaasStateStopped}: {}, {bmaasStateProvisioning, bmaasStateStarting}: {},
	{bmaasStateProvisioning, bmaasStateStopping}: {}, {bmaasStateProvisioning, bmaasStateFailed}: {},
	{bmaasStateProvisioning, bmaasStateDeleting}: {},
	{bmaasStateRunning, bmaasStateRunning}:       {}, {bmaasStateRunning, bmaasStateStopped}: {},
	{bmaasStateRunning, bmaasStateStarting}: {}, {bmaasStateRunning, bmaasStateStopping}: {},
	{bmaasStateRunning, bmaasStateFailed}: {}, {bmaasStateRunning, bmaasStateDeleting}: {},
	{bmaasStateStopped, bmaasStateStopped}: {}, {bmaasStateStopped, bmaasStateRunning}: {},
	{bmaasStateStopped, bmaasStateStarting}: {}, {bmaasStateStopped, bmaasStateFailed}: {},
	{bmaasStateStopped, bmaasStateDeleting}:  {},
	{bmaasStateStarting, bmaasStateStarting}: {}, {bmaasStateStarting, bmaasStateRunning}: {},
	{bmaasStateStarting, bmaasStateStopped}: {}, {bmaasStateStarting, bmaasStateFailed}: {},
	{bmaasStateStarting, bmaasStateDeleting}: {},
	{bmaasStateStopping, bmaasStateStopping}: {}, {bmaasStateStopping, bmaasStateStopped}: {},
	{bmaasStateStopping, bmaasStateRunning}: {}, {bmaasStateStopping, bmaasStateFailed}: {},
	{bmaasStateStopping, bmaasStateDeleting}: {},
	{bmaasStateFailed, bmaasStateFailed}:     {}, {bmaasStateFailed, bmaasStateRunning}: {},
	{bmaasStateFailed, bmaasStateDeleting}: {}, {bmaasStateDeleting, bmaasStateDeleting}: {},
}

var allocationTransitions = map[bmaasTransitionKey]string{
	{"", bmaasStateRunning}: BMaaSEffectStart, {"", bmaasStateStopped}: BMaaSEffectStart,
	{"", bmaasStateStarting}: BMaaSEffectStart, {"", bmaasStateStopping}: BMaaSEffectStart,
	{"", bmaasStateDeleting}:                     BMaaSEffectStart,
	{bmaasStateProvisioning, bmaasStateRunning}:  BMaaSEffectStart,
	{bmaasStateProvisioning, bmaasStateStopped}:  BMaaSEffectStart,
	{bmaasStateProvisioning, bmaasStateStarting}: BMaaSEffectStart,
	{bmaasStateProvisioning, bmaasStateStopping}: BMaaSEffectStart,
	{bmaasStateRunning, bmaasStateFailed}:        BMaaSEffectSuspend,
	{bmaasStateStopped, bmaasStateFailed}:        BMaaSEffectSuspend,
	{bmaasStateStarting, bmaasStateFailed}:       BMaaSEffectSuspend,
	{bmaasStateStopping, bmaasStateFailed}:       BMaaSEffectSuspend,
	{bmaasStateFailed, bmaasStateRunning}:        BMaaSEffectResume,
}

var consumptionTransitions = map[bmaasTransitionKey]string{
	{"", bmaasStateRunning}:                     BMaaSEffectStart,
	{bmaasStateProvisioning, bmaasStateRunning}: BMaaSEffectStart,
	{bmaasStateRunning, bmaasStateStopped}:      BMaaSEffectSuspend,
	{bmaasStateRunning, bmaasStateStarting}:     BMaaSEffectSuspend,
	{bmaasStateRunning, bmaasStateStopping}:     BMaaSEffectSuspend,
	{bmaasStateRunning, bmaasStateFailed}:       BMaaSEffectSuspend,
	{bmaasStateRunning, bmaasStateDeleting}:     BMaaSEffectSuspend,
	{bmaasStateStopped, bmaasStateRunning}:      BMaaSEffectStart,
	{bmaasStateStarting, bmaasStateRunning}:     BMaaSEffectStart,
	{bmaasStateStopping, bmaasStateRunning}:     BMaaSEffectStart,
	{bmaasStateFailed, bmaasStateRunning}:       BMaaSEffectStart,
}

func resolveBMaaSTransition(table map[bmaasTransitionKey]string, from, to string) (string, error) {
	key := bmaasTransitionKey{from: from, to: to}
	if _, ok := acceptedBMaaSTransitions[key]; !ok {
		return "", fmt.Errorf("%w: %s -> %s", ErrInvalidBMaaSTransition, from, to)
	}
	if effect, ok := table[key]; ok {
		return effect, nil
	}
	return BMaaSEffectSkip, nil
}

func ResolveAllocationTransition(from, to string) (string, error) {
	return resolveBMaaSTransition(allocationTransitions, from, to)
}

func ResolveConsumptionTransition(from, to string) (string, error) {
	return resolveBMaaSTransition(consumptionTransitions, from, to)
}

type BMaaSMeterIntervals struct {
	AllocationSince  *time.Time
	ConsumptionSince *time.Time
}

type BMaaSEventBuildRequest struct {
	MeterType       string
	EventType       string
	EventID         string
	BillingDims     map[string]any
	DurationSeconds *float64
}

type BMaaSEventBuilder func(BMaaSEventBuildRequest) (cloudevents.Event, error)

func DecomposeBMIEvents(
	billingDims map[string]any,
	baseID string,
	transitionTime time.Time,
	intervals BMaaSMeterIntervals,
	buildFn BMaaSEventBuilder,
	allocationType string,
	consumptionType string,
) ([]cloudevents.Event, error) {
	requests := make([]BMaaSEventBuildRequest, 0, 2)
	appendRequest := func(meterType, eventType, suffix string, since *time.Time) {
		if eventType == "" {
			return
		}
		if eventType == EventSuspended && since == nil {
			return
		}
		var duration *float64
		if since != nil {
			seconds := transitionTime.Sub(*since).Seconds()
			duration = &seconds
		}
		dims := maps.Clone(billingDims)
		if dims == nil {
			dims = make(map[string]any, 1)
		}
		dims["meter_type"] = meterType
		requests = append(requests, BMaaSEventBuildRequest{
			MeterType:       meterType,
			EventType:       eventType,
			EventID:         baseID + "/" + suffix,
			BillingDims:     dims,
			DurationSeconds: duration,
		})
	}
	appendRequest(BMaaSMeterAllocation, allocationType, "allocation", intervals.AllocationSince)
	appendRequest(BMaaSMeterConsumption, consumptionType, "consumption", intervals.ConsumptionSince)

	result := make([]cloudevents.Event, 0, len(requests))
	for _, request := range requests {
		ce, err := buildFn(request)
		if err != nil {
			return nil, err
		}
		result = append(result, ce)
	}
	return result, nil
}
