package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/IBM/sarama"

	"github.com/osac-project/osac-metering/internal/topics"
)

type ConnectionConfig struct {
	Brokers      string
	TLSCACert    string
	SASLUser     string
	SASLPassFile string
}

func NewSyncProducer(cfg ConnectionConfig) (sarama.SyncProducer, error) {
	brokers := SplitAndTrim(cfg.Brokers, ",")
	sc := sarama.NewConfig()
	sc.Version = sarama.V3_9_0_0
	sc.Producer.Return.Successes = true
	if err := ConfigureTLS(sc, cfg.TLSCACert); err != nil {
		return nil, err
	}
	if err := ConfigureSASL(sc, cfg.SASLUser, cfg.SASLPassFile); err != nil {
		return nil, err
	}
	return sarama.NewSyncProducer(brokers, sc)
}

func VerifyTopicExists(cfg ConnectionConfig, topic string) error {
	tenants, err := ListEventTenants(context.Background(), cfg)
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if topics.Events(t) == topic {
			return nil
		}
	}
	return fmt.Errorf("topic %q does not exist on cluster (%d tenants available)", topic, len(tenants))
}

// ListEventTenants returns the tenants that have an event topic, sorted by name. Metering
// discovers tenants this way: a tenant exists for metering when its event topic does.
func ListEventTenants(ctx context.Context, cfg ConnectionConfig) ([]string, error) {
	brokers := SplitAndTrim(cfg.Brokers, ",")
	sc := sarama.NewConfig()
	sc.Version = sarama.V3_9_0_0
	if err := ConfigureTLS(sc, cfg.TLSCACert); err != nil {
		return nil, err
	}
	if err := ConfigureSASL(sc, cfg.SASLUser, cfg.SASLPassFile); err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(brokers, sc)
	if err != nil {
		return nil, fmt.Errorf("connecting to kafka: %w", err)
	}
	defer func() { _ = client.Close() }()

	names, err := client.Topics()
	if err != nil {
		return nil, fmt.Errorf("listing topics: %w", err)
	}
	var tenants []string
	for _, name := range names {
		if tenant, ok := topics.TenantOfEvents(name); ok {
			tenants = append(tenants, tenant)
		}
	}
	sort.Strings(tenants)
	return tenants, nil
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
