/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package kafka_test

import (
	"os"
	"path/filepath"

	"github.com/IBM/sarama"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/internal/kafka"
)

var _ = Describe("event tenant discovery", func() {
	It("maps event topics to tenants, sorted", func() {
		Expect(kafka.EventTenantsOf([]string{
			"osac.metering.heartbeat.acme",
			"osac.events.oscar",
			"osac.events.acme",
			"unrelated",
		})).To(Equal([]string{"acme", "oscar"}))
	})

	It("skips the built-in system and shared tenants", func() {
		Expect(kafka.EventTenantsOf([]string{
			"osac.events.system",
			"osac.events.shared",
			"osac.events.acme",
		})).To(Equal([]string{"acme"}))
	})
})

var _ = Describe("producer config", func() {
	// The relay deletes an outbox row after its send succeeds, so the write must be
	// replicated before the row is gone: sarama's default waits for the leader only.
	It("waits for all in-sync replicas", func() {
		passFile := filepath.Join(GinkgoT().TempDir(), "password")
		Expect(os.WriteFile(passFile, []byte("secret"), 0o600)).To(Succeed())
		sc, err := kafka.ProducerConfig(kafka.ConnectionConfig{
			SASLUser: "metering", SASLPassFile: passFile,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Producer.RequiredAcks).To(Equal(sarama.WaitForAll))
		Expect(sc.Producer.Return.Successes).To(BeTrue())
		Expect(sc.Version).To(Equal(sarama.V3_9_0_0))
	})
})

var _ = Describe("SASL config", func() {
	It("leaves SASL off when no user and password file are set", func() {
		sc, err := kafka.ProducerConfig(kafka.ConnectionConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Net.SASL.Enable).To(BeFalse())
	})

	It("refuses a user without a password file", func() {
		_, err := kafka.ProducerConfig(kafka.ConnectionConfig{SASLUser: "metering"})
		Expect(err).To(MatchError(ContainSubstring("reading SASL password file")))
	})
})

var _ = Describe("TLS config", func() {
	It("leaves TLS off when disabled", func() {
		sc, err := kafka.ProducerConfig(kafka.ConnectionConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Net.TLS.Enable).To(BeFalse())
	})

	It("trusts the system roots when enabled without a CA cert", func() {
		sc, err := kafka.ProducerConfig(kafka.ConnectionConfig{TLSEnabled: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Net.TLS.Enable).To(BeTrue())
		Expect(sc.Net.TLS.Config.RootCAs).To(BeNil())
	})

	It("enables TLS with the CA cert", func() {
		certFile := filepath.Join(GinkgoT().TempDir(), "ca.crt")
		Expect(os.WriteFile(certFile, []byte("not a cert"), 0o600)).To(Succeed())
		_, err := kafka.ProducerConfig(kafka.ConnectionConfig{TLSEnabled: true, TLSCACert: certFile})
		Expect(err).To(MatchError(ContainSubstring("failed to parse Kafka CA cert")))
	})
})
