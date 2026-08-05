package watch

import (
	"context"
	"fmt"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/events"
	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
)

const (
	defaultInitialDelay   = 1 * time.Second
	defaultMaxDelay       = 30 * time.Second
	defaultHandlerRetries = 3
	computeInstanceFilter = "has(event.compute_instance)"
)

// Consumer connects to the fulfillment-service gRPC Watch stream, maps
// incoming events to CloudEvents, and publishes them to Kafka. It
// automatically reconnects with exponential backoff when the stream breaks.
type Consumer struct {
	client    privatev1.EventsClient
	publisher kafkapub.EventPublisher
	logger    logr.Logger

	InitialDelay   time.Duration
	MaxDelay       time.Duration
	HandlerRetries int
}

func NewConsumer(client privatev1.EventsClient, publisher kafkapub.EventPublisher, logger logr.Logger) *Consumer {
	return &Consumer{
		client:         client,
		publisher:      publisher,
		logger:         logger,
		InitialDelay:   defaultInitialDelay,
		MaxDelay:       defaultMaxDelay,
		HandlerRetries: defaultHandlerRetries,
	}
}

// Run starts consuming the Watch stream. It blocks until ctx is cancelled,
// at which point it returns nil. Stream errors trigger automatic reconnection
// with exponential backoff.
func (c *Consumer) Run(ctx context.Context) error {
	delay := c.InitialDelay
	for {
		received, err := c.consumeStream(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if received > 0 {
			delay = c.InitialDelay
		}
		c.logger.Error(err, "Watch stream error, reconnecting", "delay", delay, "receivedEvents", received)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, c.MaxDelay)
	}
}

func (c *Consumer) consumeStream(ctx context.Context) (int, error) {
	filter := computeInstanceFilter
	stream, err := c.client.Watch(ctx, &privatev1.EventsWatchRequest{
		Filter: &filter,
	})
	if err != nil {
		return 0, fmt.Errorf("establishing watch stream: %w", err)
	}

	received := 0
	for {
		resp, err := stream.Recv()
		if err != nil {
			return received, fmt.Errorf("receiving event: %w", err)
		}
		if resp.GetEvent() == nil {
			c.logger.V(1).Info("Received response with nil event, skipping")
			continue
		}
		received++

		ce, err := events.MapWatchEvent(resp.GetEvent())
		if err != nil {
			return received, fmt.Errorf("mapping event %s: %w", resp.GetEvent().GetId(), err)
		}

		if err := c.publishWithRetry(ctx, ce); err != nil {
			return received, err
		}
	}
}

func (c *Consumer) logPublished(ce *cloudevents.Event) {
	resourceID, _ := ce.Context.GetExtension("osacresourceid")
	tenantID, _ := ce.Context.GetExtension("osactenant")
	c.logger.Info("published metering event",
		"event_id", ce.ID(),
		"type", ce.Type(),
		"resource_id", resourceID,
		"tenant_id", tenantID,
	)
}

func (c *Consumer) publishWithRetry(ctx context.Context, ce *cloudevents.Event) error {
	delay := c.InitialDelay
	for attempt := range c.HandlerRetries {
		err := c.publisher.Publish(ctx, *ce)
		if err == nil {
			c.logPublished(ce)
			return nil
		}
		c.logger.Error(err, "publish error, retrying",
			"event_id", ce.ID(),
			"attempt", attempt+1,
			"maxAttempts", c.HandlerRetries,
		)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, c.MaxDelay)
	}
	// Phase 1: if retries are exhausted the event is unrecoverable — the stream
	// reconnects without a resume token. Phase 2 adds a dead-letter queue to
	// prevent billing data loss.
	return fmt.Errorf("publish failed after %d retries for event %s", c.HandlerRetries, ce.ID())
}
