/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/IBM/sarama"
	"github.com/xdg-go/scram"
)

// newConsumerConfig creates a Sarama config for the adapter consumer group.
func newConsumerConfig(cfg KafkaConfig) (*sarama.Config, error) {
	sc := sarama.NewConfig()
	sc.Version = sarama.V3_9_0_0
	sc.Consumer.Return.Errors = true
	sc.Consumer.Offsets.Initial = sarama.OffsetOldest
	sc.Consumer.Offsets.AutoCommit.Enable = false
	sc.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{
		sarama.NewBalanceStrategyRange(),
	}

	if cfg.TLSEnabled {
		if err := configureTLS(sc, cfg.TLSCACert); err != nil {
			return nil, err
		}
	}
	if cfg.SASLUser != "" {
		if err := configureSASL(sc, cfg.SASLUser, cfg.SASLPassFile); err != nil {
			return nil, err
		}
	}
	return sc, nil
}

func configureTLS(sc *sarama.Config, caCertPath string) error {
	sc.Net.TLS.Enable = true
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caCertPath != "" {
		caCert, err := os.ReadFile(caCertPath)
		if err != nil {
			return fmt.Errorf("reading Kafka CA cert %s: %w", caCertPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("failed to parse Kafka CA cert %s", caCertPath)
		}
		tlsCfg.RootCAs = pool
	}
	sc.Net.TLS.Config = tlsCfg
	return nil
}

func configureSASL(sc *sarama.Config, user, passFile string) error {
	password, err := os.ReadFile(passFile)
	if err != nil {
		return fmt.Errorf("reading SASL password file %s: %w", passFile, err)
	}
	trimmed := strings.TrimSpace(string(password))
	if trimmed == "" {
		return fmt.Errorf("SASL password file %s is empty", passFile)
	}
	sc.Net.SASL.Enable = true
	sc.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
	sc.Net.SASL.User = user
	sc.Net.SASL.Password = trimmed
	sc.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
		return &adapterScramClient{}
	}
	return nil
}

type adapterScramClient struct {
	conversation *scram.ClientConversation
}

func (c *adapterScramClient) Begin(userName, password, authzID string) error {
	client, err := scram.SHA512.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	c.conversation = client.NewConversation()
	return nil
}

func (c *adapterScramClient) Step(challenge string) (string, error) {
	return c.conversation.Step(challenge)
}

func (c *adapterScramClient) Done() bool {
	return c.conversation.Done()
}

func newProducerConfig(cfg KafkaConfig) (*sarama.Config, error) {
	sc := sarama.NewConfig()
	sc.Version = sarama.V3_9_0_0
	sc.Producer.RequiredAcks = sarama.WaitForAll
	sc.Producer.Idempotent = true
	sc.Producer.Return.Successes = true
	sc.Net.MaxOpenRequests = 1

	if cfg.TLSEnabled {
		if err := configureTLS(sc, cfg.TLSCACert); err != nil {
			return nil, err
		}
	}
	if cfg.SASLUser != "" {
		if err := configureSASL(sc, cfg.SASLUser, cfg.SASLPassFile); err != nil {
			return nil, err
		}
	}
	return sc, nil
}

func splitAndTrimBrokers(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := parts[:0]
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
