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

type bmaasTransitionEffects struct {
	allocation  string
	consumption string
}

var bmaasTransitions = map[bmaasTransitionKey]bmaasTransitionEffects{
	{"", bmaasStateProvisioning}:                     {},
	{"", bmaasStateRunning}:                          {allocation: BMaaSEffectStart, consumption: BMaaSEffectStart},
	{"", bmaasStateStopped}:                          {allocation: BMaaSEffectStart},
	{"", bmaasStateStarting}:                         {allocation: BMaaSEffectStart},
	{"", bmaasStateStopping}:                         {allocation: BMaaSEffectStart},
	{"", bmaasStateFailed}:                           {},
	{"", bmaasStateDeleting}:                         {allocation: BMaaSEffectStart},
	{"", bmaasStateUnspecified}:                      {},
	{bmaasStateProvisioning, bmaasStateProvisioning}: {},
	{bmaasStateProvisioning, bmaasStateRunning}:      {allocation: BMaaSEffectStart, consumption: BMaaSEffectStart},
	{bmaasStateProvisioning, bmaasStateStopped}:      {allocation: BMaaSEffectStart},
	{bmaasStateProvisioning, bmaasStateStarting}:     {allocation: BMaaSEffectStart},
	{bmaasStateProvisioning, bmaasStateStopping}:     {allocation: BMaaSEffectStart},
	{bmaasStateProvisioning, bmaasStateFailed}:       {}, {bmaasStateProvisioning, bmaasStateDeleting}: {},
	{bmaasStateRunning, bmaasStateRunning}:   {},
	{bmaasStateRunning, bmaasStateStopped}:   {consumption: BMaaSEffectSuspend},
	{bmaasStateRunning, bmaasStateStarting}:  {consumption: BMaaSEffectSuspend},
	{bmaasStateRunning, bmaasStateStopping}:  {consumption: BMaaSEffectSuspend},
	{bmaasStateRunning, bmaasStateFailed}:    {allocation: BMaaSEffectSuspend, consumption: BMaaSEffectSuspend},
	{bmaasStateRunning, bmaasStateDeleting}:  {consumption: BMaaSEffectSuspend},
	{bmaasStateStopped, bmaasStateStopped}:   {},
	{bmaasStateStopped, bmaasStateRunning}:   {consumption: BMaaSEffectStart},
	{bmaasStateStopped, bmaasStateStarting}:  {},
	{bmaasStateStopped, bmaasStateFailed}:    {allocation: BMaaSEffectSuspend},
	{bmaasStateStopped, bmaasStateDeleting}:  {},
	{bmaasStateStarting, bmaasStateStarting}: {},
	{bmaasStateStarting, bmaasStateRunning}:  {consumption: BMaaSEffectStart},
	{bmaasStateStarting, bmaasStateStopped}:  {},
	{bmaasStateStarting, bmaasStateFailed}:   {allocation: BMaaSEffectSuspend},
	{bmaasStateStarting, bmaasStateDeleting}: {},
	{bmaasStateStopping, bmaasStateStopping}: {},
	{bmaasStateStopping, bmaasStateStopped}:  {},
	{bmaasStateStopping, bmaasStateRunning}:  {consumption: BMaaSEffectStart},
	{bmaasStateStopping, bmaasStateFailed}:   {allocation: BMaaSEffectSuspend},
	{bmaasStateStopping, bmaasStateDeleting}: {},
	{bmaasStateFailed, bmaasStateFailed}:     {},
	{bmaasStateFailed, bmaasStateRunning}:    {allocation: BMaaSEffectResume, consumption: BMaaSEffectStart},
	{bmaasStateFailed, bmaasStateDeleting}:   {},
	{bmaasStateDeleting, bmaasStateDeleting}: {},
}

func resolveBMaaSTransition(from, to string) (bmaasTransitionEffects, error) {
	key := bmaasTransitionKey{from: from, to: to}
	effects, ok := bmaasTransitions[key]
	if !ok {
		return bmaasTransitionEffects{}, fmt.Errorf("%w: %s -> %s", ErrInvalidBMaaSTransition, from, to)
	}
	return effects, nil
}

func ResolveAllocationTransition(from, to string) (string, error) {
	effects, err := resolveBMaaSTransition(from, to)
	if err != nil {
		return "", err
	}
	if effects.allocation == "" {
		return BMaaSEffectSkip, err
	}
	return effects.allocation, nil
}

func ResolveConsumptionTransition(from, to string) (string, error) {
	effects, err := resolveBMaaSTransition(from, to)
	if err != nil {
		return "", err
	}
	if effects.consumption == "" {
		return BMaaSEffectSkip, err
	}
	return effects.consumption, nil
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
