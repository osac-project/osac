package watch

import (
	"context"
	"errors"
	"fmt"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/projection"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func (c *Consumer) handlePreMappingScaling(
	ctx context.Context,
	event *privatev1.Event,
	mapper events.ResourceMapper,
	existing *projection.ResourceState,
	transitionTime time.Time,
	version int32,
	currentState string,
	isBillable bool,
	dims map[string]any,
) (bool, error) {
	if mapper.ResourceType() != events.ResourceTypeVolume || existing == nil || !existing.IsBillable || !isBillable ||
		existing.CurrentState != currentState || events.DimensionsEqual(existing.BillingDimensions, dims) {
		return false, nil
	}
	return true, c.handleScalingEvent(ctx, event, mapper, existing, transitionTime, version, currentState, isBillable, dims)
}

func (c *Consumer) handleSkippedTransition(
	ctx context.Context,
	event *privatev1.Event,
	mapper events.ResourceMapper,
	existing *projection.ResourceState,
	currentState string,
	isBillable bool,
	dims map[string]any,
	version int32,
	transitionTime time.Time,
	resourceID string,
) error {
	if existing != nil && !events.DimensionsEqual(existing.BillingDimensions, dims) {
		return c.handleScalingEvent(ctx, event, mapper, existing, transitionTime, version, currentState, isBillable, dims)
	}
	c.logger.V(1).Info("non-billing state transition, updating projection only",
		"resource_id", resourceID, "state", currentState)
	projState := c.buildProjectionState(mapper, existing, transitionTime, version, currentState, isBillable, dims)
	if err := c.store.Upsert(ctx, projState); err != nil && !errors.Is(err, projection.ErrStaleVersion) {
		return fmt.Errorf("upserting projection for %s: %w", resourceID, err)
	}
	return nil
}

func normalizeEventDimensions(event *privatev1.Event, mapper events.ResourceMapper, dims map[string]any) map[string]any {
	if mapper.ResourceType() == events.ResourceTypeClusterOrder &&
		(event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_CREATED ||
			event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED) {
		return topLevelDims(dims)
	}
	return dims
}

func sameVersionVolumeDeletionBoundary(event *privatev1.Event, existing *projection.ResourceState, version int32, currentState string) bool {
	return event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED &&
		event.GetVolume() != nil &&
		event.GetVolume().GetMetadata().GetDeletionTimestamp() != nil &&
		currentState == events.VolumeStateDeleting &&
		existing != nil &&
		existing.FulfillmentVersion == version &&
		existing.CurrentState != events.VolumeStateDeleting
}

// DimComponents is the billing dimensions key for the nested components array.
const DimComponents = "components"

func topLevelDims(dims map[string]any) map[string]any {
	flat := make(map[string]any, len(dims))
	for k, v := range dims {
		if k != DimComponents {
			flat[k] = v
		}
	}
	return flat
}

func (c *Consumer) handleScalingEvent(ctx context.Context, event *privatev1.Event, mapper events.ResourceMapper, existing *projection.ResourceState, transitionTime time.Time, version int32, currentState string, isBillable bool, dims map[string]any) error {
	resourceID := mapper.ResourceID()
	projState := c.buildProjectionState(mapper, existing, transitionTime, version, currentState, isBillable, dims)
	stateCtx := c.buildStateContext(existing, isBillable, transitionTime, dims)

	return c.publishAndUpsert(ctx, func() error {
		if mapper.ResourceType() == events.ResourceTypeClusterOrder {
			changed, err := events.ChangedComponents(existing.BillingDimensions, dims)
			if err != nil {
				return err
			}
			if len(changed) == 0 {
				c.logger.V(1).Info("non-component dimension change, projection updated",
					"resource_id", resourceID)
				return nil
			}
			for _, comp := range changed {
				scalingCtx := &events.StateContext{
					PreviousState: stateCtx.PreviousState,
				}
				if !comp.IsNew {
					scalingCtx.DurationSeconds = c.componentDurationSeconds(existing, comp.NodeSet, transitionTime)
				}
				ce, ceErr := c.buildScalingEvent(
					events.ComponentEventID(event.GetId(), comp),
					mapper, comp.FlatBillingDimensions(), scalingCtx, transitionTime)
				if ceErr != nil {
					return ceErr
				}
				if err := c.publishWithRetry(ctx, &ce); err != nil {
					return err
				}
			}
			c.logger.Info("published scaling events",
				"resource_id", resourceID, "changed_components", len(changed))
			return nil
		}
		// VMaaS and networking use a single updated.v1. An ExternalIP
		// dimension change closes the prior slice with its prior dimensions.
		scalingDims := dims
		if mapper.ResourceType() == events.ResourceTypeExternalIP || mapper.ResourceType() == events.ResourceTypeVolume {
			scalingDims = existing.BillingDimensions
		}
		ce, ceErr := c.buildScalingEvent(event.GetId(), mapper, scalingDims, stateCtx, transitionTime)
		if ceErr != nil {
			return ceErr
		}
		return c.publishWithRetry(ctx, &ce)
	}, projState, resourceID, false)
}

func (c *Consumer) buildScalingEvent(eventID string, mapper events.ResourceMapper, dims map[string]any, stateCtx *events.StateContext, transitionTime time.Time) (cloudevents.Event, error) {
	if err := events.ValidateResourceIdentity(mapper, eventID); err != nil {
		return cloudevents.Event{}, err
	}
	ce := cloudevents.NewEvent()
	ce.SetID(eventID)
	ce.SetSource("osac-metering")
	ce.SetType(events.EventUpdated)
	ce.SetTime(transitionTime)

	projectID := ""
	if p := mapper.ProjectID(); p != nil {
		projectID = *p
	}
	events.SetOSACExtensions(&ce, mapper.ResourceID(), mapper.ResourceType(), mapper.TenantID(), projectID)

	data := events.BuildLifecycleData(mapper, dims, stateCtx.PreviousState, stateCtx.DurationSeconds, transitionTime)
	usage, err := mapper.Usage(events.EventUpdated, stateCtx.BillableSince, transitionTime, dims)
	if err != nil {
		return ce, err
	}
	data.Usage = usage
	if err := ce.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return ce, fmt.Errorf("setting scaling event data: %w", err)
	}
	return ce, nil
}
