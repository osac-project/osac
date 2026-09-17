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

type preparedEvent struct {
	event                    *privatev1.Event
	mapper                   events.ResourceMapper
	existing                 *projection.ResourceState
	resourceID               string
	currentState             string
	isBillable               bool
	version                  int32
	dimensions               map[string]any
	transitionTime           time.Time
	allowSameVersionDeletion bool
}

func (c *Consumer) prepareEvent(ctx context.Context, event *privatev1.Event) (preparedEvent, bool, error) {
	mapper, err := c.mapperFactory.MapperForEvent(ctx, event)
	if err != nil {
		return preparedEvent{}, false, fmt.Errorf("unexpected event payload for %s: %w", event.GetId(), err)
	}

	resourceID := mapper.ResourceID()
	dimensions, err := mapper.BillingDimensionsMap()
	if err != nil {
		return preparedEvent{}, false, fmt.Errorf("building billing dimensions for %s: %w", resourceID, err)
	}
	existing, err := c.store.Get(ctx, resourceID)
	if err != nil {
		return preparedEvent{}, false, fmt.Errorf("reading projection for %s: %w", resourceID, err)
	}

	previousState := ""
	if existing != nil {
		previousState = existing.CurrentState
	}
	transitionTime, err := mapper.TransitionTime(event, previousState)
	if err != nil {
		if errors.Is(err, events.ErrUnsupportedEvent) {
			eventsSkipped.WithLabelValues("unsupported_event_type").Inc()
			c.logger.V(1).Info("skipping unsupported event type",
				"event_id", event.GetId(), "resource_id", resourceID)
			return preparedEvent{}, true, nil
		}
		if errors.Is(err, events.ErrDataQuality) && mapper.ResourceType() != events.ResourceTypeVolume && existing != nil && existing.CurrentState == mapper.CurrentState() {
			c.logger.V(1).Info("skipping metadata-only update with no state change",
				"event_id", event.GetId(), "resource_id", resourceID, "state", mapper.CurrentState())
			return preparedEvent{}, true, nil
		}
		return preparedEvent{}, false, err
	}

	return preparedEvent{
		event:                    event,
		mapper:                   mapper,
		existing:                 existing,
		resourceID:               resourceID,
		currentState:             mapper.CurrentState(),
		isBillable:               mapper.IsBillable(),
		version:                  mapper.FulfillmentVersion(),
		dimensions:               dimensions,
		transitionTime:           transitionTime,
		allowSameVersionDeletion: sameVersionVolumeDeletionBoundary(event, existing, mapper.FulfillmentVersion(), mapper.CurrentState()),
	}, false, nil
}

func (c *Consumer) skipStaleEvent(prepared preparedEvent) bool {
	if !projectionIsAhead(
		prepared.existing,
		prepared.version,
		prepared.currentState,
		prepared.dimensions,
		prepared.allowSameVersionDeletion,
	) {
		return false
	}
	c.logger.Info("skipping stale Watch event before publication",
		"resource_id", prepared.resourceID,
		"event_version", prepared.version,
		"projection_version", prepared.existing.FulfillmentVersion)
	return true
}

func (c *Consumer) mapPreparedEvent(ctx context.Context, prepared preparedEvent) (*cloudevents.Event, bool, error) {
	stateContext := c.buildStateContext(prepared.existing, prepared.isBillable, prepared.transitionTime, prepared.dimensions)
	eventDimensions := normalizeEventDimensions(prepared.event, prepared.mapper, prepared.dimensions)
	cloudEvent, err := events.MapWatchEvent(prepared.event, prepared.mapper, stateContext, eventDimensions)
	if err == nil {
		return cloudEvent, false, nil
	}
	if errors.Is(err, events.ErrTransientState) {
		return nil, true, c.handleTransientState(ctx, prepared.mapper, prepared.existing, prepared.version, prepared.transitionTime)
	}
	if errors.Is(err, events.ErrSkipTransition) {
		return nil, true, c.handleSkippedTransition(
			ctx,
			prepared.event,
			prepared.mapper,
			prepared.existing,
			prepared.currentState,
			prepared.isBillable,
			prepared.dimensions,
			prepared.version,
			prepared.transitionTime,
			prepared.resourceID,
		)
	}
	return nil, false, err
}

func (c *Consumer) commitMappedEvent(ctx context.Context, prepared preparedEvent, cloudEvent *cloudevents.Event) error {
	projectionState := c.buildProjectionState(
		prepared.mapper,
		prepared.existing,
		prepared.transitionTime,
		prepared.version,
		prepared.currentState,
		prepared.isBillable,
		prepared.dimensions,
	)

	if prepared.event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED {
		latest, err := c.store.Get(ctx, prepared.resourceID)
		if err != nil {
			return fmt.Errorf("rechecking projection for %s: %w", prepared.resourceID, err)
		}
		if projectionIsAhead(latest, prepared.version, prepared.currentState, prepared.dimensions, false) {
			c.logger.Info("skipping stale delete event before publication",
				"resource_id", prepared.resourceID,
				"event_version", prepared.version,
				"projection_version", latest.FulfillmentVersion)
			return nil
		}
		if err := c.publishLifecycleEvents(ctx, cloudEvent, prepared.mapper, prepared.event.GetId(), prepared.dimensions); err != nil {
			return err
		}
		if prepared.existing != nil {
			if err := c.store.Delete(ctx, prepared.resourceID); err != nil {
				return fmt.Errorf("deleting projection for %s: %w", prepared.resourceID, err)
			}
		}
		return nil
	}

	return c.publishAndUpsert(ctx, func() error {
		return c.publishLifecycleEvents(ctx, cloudEvent, prepared.mapper, prepared.event.GetId(), prepared.dimensions)
	}, projectionState, prepared.resourceID, prepared.allowSameVersionDeletion)
}
