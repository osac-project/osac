/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package kafka

import (
	"errors"
	"sync/atomic"

	"github.com/IBM/sarama"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("adapter topics", func() {
	It("names the per-tenant topics", func() {
		Expect(HeartbeatPrefix + "acme").To(Equal("osac.metering.heartbeat.acme"))
		Expect(DeadLetterPrefix + "acme").To(Equal("osac.metering.dlq.acme"))
	})

	It("maps a heartbeat topic to the tenant's dead-letter topic", func() {
		Expect(DeadLetterTopicOf("osac.metering.heartbeat.acme")).
			To(Equal("osac.metering.dlq.acme"))
		Expect(DeadLetterTopicOf("osac.events.acme")).
			To(Equal("osac.events.acme"))
	})

	It("maps the inference topic to its own dead-letter topic, not back to itself", func() {
		Expect(DeadLetterTopicOf(TopicInference)).To(Equal(InferenceDeadLetter))
		Expect(InferenceDeadLetter).To(Equal("osac.metering.inference.dlq"))
		Expect(DeadLetterTopicOf(TopicInference)).NotTo(Equal(TopicInference))
	})
})

var _ = Describe("topic discovery", func() {
	It("refreshes the metadata cache before listing topics", func() {
		client := &refreshingClient{
			topics: []string{
				"osac.metering.heartbeat.acme",
				"osac.metering.dlq.acme",
				"osac.events.other",
			},
		}
		topics, err := HeartbeatTopics(client)
		Expect(err).NotTo(HaveOccurred())
		Expect(topics).To(Equal([]string{"osac.metering.heartbeat.acme"}))
		Expect(client.refreshes.Load()).To(Equal(int32(1)),
			"the metadata cache must be refreshed before the topics are read")

		dlq, err := DeadLetterTopics(client)
		Expect(err).NotTo(HaveOccurred())
		Expect(dlq).To(Equal([]string{"osac.metering.dlq.acme"}))
		Expect(client.refreshes.Load()).To(Equal(int32(2)))
	})

	It("skips the built-in system and shared tenants", func() {
		client := &refreshingClient{
			topics: []string{
				"osac.metering.heartbeat.system",
				"osac.metering.heartbeat.shared",
				"osac.metering.heartbeat.acme",
				"osac.metering.dlq.shared",
			},
		}
		topics, err := HeartbeatTopics(client)
		Expect(err).NotTo(HaveOccurred())
		Expect(topics).To(Equal([]string{"osac.metering.heartbeat.acme"}))

		dlq, err := DeadLetterTopics(client)
		Expect(err).NotTo(HaveOccurred())
		Expect(dlq).To(BeEmpty())
	})

	It("lists every dead-letter topic, the tenants' and the inference topic's", func() {
		client := &refreshingClient{
			topics: []string{
				"osac.metering.heartbeat.acme",
				"osac.metering.dlq.acme",
				"osac.metering.dlq.shared",
				TopicInference,
				InferenceDeadLetter,
			},
		}
		topics, err := AllDeadLetterTopics(client)
		Expect(err).NotTo(HaveOccurred())
		Expect(topics).To(Equal([]string{
			"osac.metering.dlq.acme",
			"osac.metering.inference.dlq",
		}))
	})

	It("consumes the shared inference topic alongside the heartbeat topics", func() {
		client := &refreshingClient{
			topics: []string{
				"osac.metering.heartbeat.acme",
				"osac.metering.heartbeat.oscar",
				"osac.metering.dlq.acme",
			},
		}
		topics, err := ConsumeTopics(client)
		Expect(err).NotTo(HaveOccurred())
		Expect(topics).To(Equal([]string{
			"osac.metering.heartbeat.acme",
			"osac.metering.heartbeat.oscar",
			"osac.metering.inference",
		}))
	})
})

// refreshingClient stands in for a sarama client and counts metadata refreshes.
type refreshingClient struct {
	sarama.Client
	topics    []string
	refreshes atomic.Int32
}

func (c *refreshingClient) RefreshMetadata(...string) error {
	c.refreshes.Add(1)
	return nil
}

func (c *refreshingClient) Topics() ([]string, error) {
	if c.refreshes.Load() == 0 {
		return nil, errors.New("stale metadata: topics read before any refresh")
	}
	return c.topics, nil
}
