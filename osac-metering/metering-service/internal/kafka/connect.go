package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
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

// baseConfig builds the sarama config every metering client shares: the broker version,
// TLS, and SASL.
func baseConfig(cfg ConnectionConfig) (*sarama.Config, error) {
	sc := sarama.NewConfig()
	sc.Version = sarama.V3_9_0_0
	if err := ConfigureTLS(sc, cfg.TLSCACert); err != nil {
		return nil, err
	}
	if err := ConfigureSASL(sc, cfg.SASLUser, cfg.SASLPassFile); err != nil {
		return nil, err
	}
	return sc, nil
}

// ProducerConfig builds the sarama config metering produces heartbeats with. Sends wait
// for all in-sync replicas, like the fulfillment publisher: the relay deletes an outbox
// row after its send succeeds, so a write the leader acknowledged but never replicated
// loses committed usage forever.
func ProducerConfig(cfg ConnectionConfig) (*sarama.Config, error) {
	sc, err := baseConfig(cfg)
	if err != nil {
		return nil, err
	}
	sc.Producer.Return.Successes = true
	sc.Producer.RequiredAcks = sarama.WaitForAll
	return sc, nil
}

// NewSyncProducer creates a producer that sends heartbeats to the tenant heartbeat
// topics.
func NewSyncProducer(cfg ConnectionConfig) (sarama.SyncProducer, error) {
	brokers := SplitAndTrim(cfg.Brokers, ",")
	sc, err := ProducerConfig(cfg)
	if err != nil {
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

// topicAdmin is the slice of sarama's ClusterAdmin the topic setup needs. Tests replace it.
type topicAdmin interface {
	DescribeTopics(topics []string) ([]*sarama.TopicMetadata, error)
	CreateTopic(topic string, detail *sarama.TopicDetail, validateOnly bool) error
}

// EnsureTenantTopics creates the tenant's heartbeat and dead-letter topics when they are
// missing. Metering owns both, so the discovery pass that first serves a tenant creates
// them; nothing else does. An existing topic is left untouched, and a topic another pass
// created in the meantime is not an error.
func EnsureTenantTopics(cfg ConnectionConfig, tenant string, replicationFactor int16, retentionMS string) error {
	brokers := SplitAndTrim(cfg.Brokers, ",")
	sc, err := baseConfig(cfg)
	if err != nil {
		return err
	}
	admin, err := sarama.NewClusterAdmin(brokers, sc)
	if err != nil {
		return fmt.Errorf("connecting to kafka: %w", err)
	}
	defer func() { _ = admin.Close() }()

	for _, topic := range []string{topics.Heartbeat(tenant), topics.DeadLetter(tenant)} {
		if err := ensureTopic(admin, topic, replicationFactor, retentionMS); err != nil {
			return err
		}
	}
	return nil
}

// ensureTopic creates one topic when it is missing. One partition keeps the heartbeat
// order the relay relies on, and the retention is the installation's recovery window,
// because metering rebuilds its database from the topics.
func ensureTopic(admin topicAdmin, topic string, replicationFactor int16, retentionMS string) error {
	descriptions, err := admin.DescribeTopics([]string{topic})
	if err != nil {
		return fmt.Errorf("describing topic '%s': %w", topic, err)
	}
	if len(descriptions) != 1 {
		return fmt.Errorf("expected one description of topic '%s', but got %d", topic, len(descriptions))
	}
	switch descriptions[0].Err {
	case sarama.ErrNoError:
		return nil
	case sarama.ErrUnknownTopicOrPartition:
	default:
		return fmt.Errorf("describing topic '%s': %w", topic, descriptions[0].Err)
	}
	cleanupPolicy := "delete"
	detail := &sarama.TopicDetail{
		NumPartitions:     1,
		ReplicationFactor: replicationFactor,
		ConfigEntries: map[string]*string{
			"retention.ms":   &retentionMS,
			"cleanup.policy": &cleanupPolicy,
		},
	}
	if replicationFactor >= 2 {
		minInSync := "2"
		detail.ConfigEntries["min.insync.replicas"] = &minInSync
	}
	if err := admin.CreateTopic(topic, detail, false); err != nil && !errors.Is(err, sarama.ErrTopicAlreadyExists) {
		return fmt.Errorf("creating topic '%s': %w", topic, err)
	}
	return nil
}

// ListEventTenants returns the tenants that have an event topic, sorted by name. Metering
// discovers tenants this way: a tenant exists for metering when its event topic does.
func ListEventTenants(ctx context.Context, cfg ConnectionConfig) ([]string, error) {
	brokers := SplitAndTrim(cfg.Brokers, ",")
	sc, err := baseConfig(cfg)
	if err != nil {
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
	return EventTenantsOf(names), nil
}

// EventTenantsOf maps event topic names to the tenants metering serves, skipping the
// built-in tenants. Fulfillment creates the system and shared tenants for platform use;
// metering does not read them.
func EventTenantsOf(names []string) []string {
	var tenants []string
	for _, name := range names {
		tenant, ok := topics.TenantOfEvents(name)
		if !ok || !topics.Metered(tenant) {
			continue
		}
		tenants = append(tenants, tenant)
	}
	sort.Strings(tenants)
	return tenants
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
