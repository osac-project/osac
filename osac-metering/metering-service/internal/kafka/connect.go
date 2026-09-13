package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/IBM/sarama"
)

type ConnectionConfig struct {
	Brokers      string
	TLSCACert    string
	SASLUser     string
	SASLPassFile string
}

func NewConsumerGroup(cfg ConnectionConfig, groupID string) (sarama.ConsumerGroup, error) {
	brokers := SplitAndTrim(cfg.Brokers, ",")
	sc := sarama.NewConfig()
	sc.Version = sarama.V3_9_0_0
	sc.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	sc.Consumer.Offsets.Initial = sarama.OffsetOldest
	sc.Consumer.Offsets.AutoCommit.Enable = false
	if err := ConfigureTLS(sc, cfg.TLSCACert); err != nil {
		return nil, err
	}
	if err := ConfigureSASL(sc, cfg.SASLUser, cfg.SASLPassFile); err != nil {
		return nil, err
	}
	return sarama.NewConsumerGroup(brokers, groupID, sc)
}

func NewSyncProducer(cfg ConnectionConfig) (sarama.SyncProducer, error) {
	brokers := SplitAndTrim(cfg.Brokers, ",")
	sc := NewProducerConfig()
	if err := ConfigureTLS(sc, cfg.TLSCACert); err != nil {
		return nil, err
	}
	if err := ConfigureSASL(sc, cfg.SASLUser, cfg.SASLPassFile); err != nil {
		return nil, err
	}
	return sarama.NewSyncProducer(brokers, sc)
}

func VerifyTopicExists(cfg ConnectionConfig, topic string) error {
	brokers := SplitAndTrim(cfg.Brokers, ",")
	sc := sarama.NewConfig()
	sc.Version = sarama.V3_9_0_0
	if err := ConfigureTLS(sc, cfg.TLSCACert); err != nil {
		return err
	}
	if err := ConfigureSASL(sc, cfg.SASLUser, cfg.SASLPassFile); err != nil {
		return err
	}
	client, err := sarama.NewClient(brokers, sc)
	if err != nil {
		return fmt.Errorf("connecting to kafka: %w", err)
	}
	defer func() { _ = client.Close() }()

	topics, err := client.Topics()
	if err != nil {
		return fmt.Errorf("listing topics: %w", err)
	}
	for _, t := range topics {
		if t == topic {
			return nil
		}
	}
	return fmt.Errorf("topic %q does not exist on cluster (%d topics available)", topic, len(topics))
}

func ConfigureTLS(sc *sarama.Config, caCertPath string) error {
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

func ConfigureSASL(sc *sarama.Config, user, passFile string) error {
	password, err := os.ReadFile(passFile)
	if err != nil {
		return fmt.Errorf("reading SASL password file %s: %w", passFile, err)
	}
	sc.Net.SASL.Enable = true
	sc.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
	trimmed := strings.TrimSpace(string(password))
	if trimmed == "" {
		return fmt.Errorf("SASL password file %s is empty", passFile)
	}
	sc.Net.SASL.User = user
	sc.Net.SASL.Password = trimmed
	sc.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
		return &scramClient{}
	}
	return nil
}

func SplitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := parts[:0]
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
