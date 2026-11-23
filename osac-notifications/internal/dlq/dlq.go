/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dlq

import (
	"context"
	"fmt"

	"github.com/IBM/sarama"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
)

const (
	headerReason = "osac-dlq-reason"
)

// Publisher writes rejected or permanently failed events to the tenant DLQ topic.
type Publisher interface {
	Publish(ctx context.Context, tenant string, payload []byte, reason string) error
}

// KafkaPublisher publishes to osac.notifications.dlq.<tenant>.
type KafkaPublisher struct {
	producer sarama.SyncProducer
}

// NewKafkaPublisher wraps a sync producer.
func NewKafkaPublisher(producer sarama.SyncProducer) *KafkaPublisher {
	return &KafkaPublisher{producer: producer}
}

// TopicForTenant returns the DLQ topic for a tenant.
func TopicForTenant(tenant string) string {
	return cevent.TopicPrefix + "dlq." + tenant
}

// Publish sends payload to the tenant DLQ topic with a reason header.
func (p *KafkaPublisher) Publish(_ context.Context, tenant string, payload []byte, reason string) error {
	if p == nil || p.producer == nil {
		return fmt.Errorf("dlq publisher is not configured")
	}
	if tenant == "" {
		return fmt.Errorf("dlq tenant is required")
	}
	msg := &sarama.ProducerMessage{
		Topic: TopicForTenant(tenant),
		Value: sarama.ByteEncoder(payload),
		Headers: []sarama.RecordHeader{{
			Key:   []byte(headerReason),
			Value: []byte(reason),
		}},
	}
	_, _, err := p.producer.SendMessage(msg)
	if err != nil {
		return fmt.Errorf("publish dlq topic %s: %w", msg.Topic, err)
	}
	return nil
}

// Nop is a no-op publisher for tests.
type Nop struct{}

// Publish implements Publisher.
func (Nop) Publish(context.Context, string, []byte, string) error { return nil }
