/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"time"

	"github.com/IBM/sarama"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("newConsumerConfig", func() {
	It("disables TLS when TLSEnabled is false", func() {
		sc, err := newConsumerConfig(KafkaConfig{TLSEnabled: false})
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Net.TLS.Enable).To(BeFalse())
	})

	It("enables TLS when TLSEnabled is true", func() {
		sc, err := newConsumerConfig(KafkaConfig{TLSEnabled: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Net.TLS.Enable).To(BeTrue())
		Expect(sc.Net.TLS.Config).NotTo(BeNil())
	})

	It("sets correct consumer defaults", func() {
		sc, err := newConsumerConfig(KafkaConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Consumer.Return.Errors).To(BeTrue())
		Expect(sc.Consumer.Offsets.AutoCommit.Enable).To(BeFalse())
	})
})

var _ = Describe("NewProducerConfig", func() {
	It("returns idempotent producer config with no TLS or SASL", func() {
		sc, err := NewProducerConfig(KafkaConfig{TLSEnabled: false})
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Producer.RequiredAcks).To(Equal(sarama.WaitForAll))
		Expect(sc.Producer.Idempotent).To(BeTrue())
		Expect(sc.Producer.Return.Successes).To(BeTrue())
		Expect(sc.Net.MaxOpenRequests).To(Equal(1))
		Expect(sc.Net.DialTimeout).To(Equal(5 * time.Second))
		Expect(sc.Net.ReadTimeout).To(Equal(5 * time.Second))
		Expect(sc.Net.WriteTimeout).To(Equal(5 * time.Second))
		Expect(sc.Net.TLS.Enable).To(BeFalse())
	})

	It("enables TLS when TLSEnabled is true", func() {
		sc, err := NewProducerConfig(KafkaConfig{TLSEnabled: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Net.TLS.Enable).To(BeTrue())
		Expect(sc.Net.TLS.Config).NotTo(BeNil())
	})
})

var _ = Describe("splitAndTrimBrokers", func() {
	It("splits and trims broker addresses", func() {
		result := splitAndTrimBrokers("  broker1:9092 , broker2:9092 , ", ",")
		Expect(result).To(Equal([]string{"broker1:9092", "broker2:9092"}))
	})

	It("handles a single broker", func() {
		result := splitAndTrimBrokers("broker1:9092", ",")
		Expect(result).To(Equal([]string{"broker1:9092"}))
	})

	It("skips empty segments", func() {
		result := splitAndTrimBrokers("broker1:9092,,broker2:9092", ",")
		Expect(result).To(Equal([]string{"broker1:9092", "broker2:9092"}))
	})
})
