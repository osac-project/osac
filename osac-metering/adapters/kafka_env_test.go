/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KafkaConfigFromEnv", func() {
	It("enables TLS by default when KAFKA_TLS_ENABLED is unset", func() {
		GinkgoT().Setenv("KAFKA_TLS_ENABLED", "")
		cfg := KafkaConfigFromEnv()
		Expect(cfg.TLSEnabled).To(BeTrue())
	})

	It("disables TLS when KAFKA_TLS_ENABLED is 'false'", func() {
		GinkgoT().Setenv("KAFKA_TLS_ENABLED", "false")
		cfg := KafkaConfigFromEnv()
		Expect(cfg.TLSEnabled).To(BeFalse())
	})

	It("enables TLS when KAFKA_TLS_ENABLED is 'true'", func() {
		GinkgoT().Setenv("KAFKA_TLS_ENABLED", "true")
		cfg := KafkaConfigFromEnv()
		Expect(cfg.TLSEnabled).To(BeTrue())
	})

	It("enables TLS for any non-'false' value", func() {
		GinkgoT().Setenv("KAFKA_TLS_ENABLED", "yes")
		cfg := KafkaConfigFromEnv()
		Expect(cfg.TLSEnabled).To(BeTrue())
	})

	It("reads KAFKA_TLS_CA_CERT", func() {
		GinkgoT().Setenv("KAFKA_TLS_CA_CERT", "/etc/kafka/ca.crt")
		cfg := KafkaConfigFromEnv()
		Expect(cfg.TLSCACert).To(Equal("/etc/kafka/ca.crt"))
	})

	It("reads SASL configuration", func() {
		GinkgoT().Setenv("KAFKA_SASL_USERNAME", "metering-user")
		GinkgoT().Setenv("KAFKA_SASL_PASSWORD_FILE", "/run/secrets/kafka-password")
		cfg := KafkaConfigFromEnv()
		Expect(cfg.SASLUser).To(Equal("metering-user"))
		Expect(cfg.SASLPassFile).To(Equal("/run/secrets/kafka-password"))
	})

	It("returns empty strings for unset optional fields", func() {
		GinkgoT().Setenv("KAFKA_TLS_CA_CERT", "")
		GinkgoT().Setenv("KAFKA_SASL_USERNAME", "")
		GinkgoT().Setenv("KAFKA_SASL_PASSWORD_FILE", "")
		cfg := KafkaConfigFromEnv()
		Expect(cfg.TLSCACert).To(BeEmpty())
		Expect(cfg.SASLUser).To(BeEmpty())
		Expect(cfg.SASLPassFile).To(BeEmpty())
	})
})
