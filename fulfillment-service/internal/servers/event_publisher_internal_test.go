package servers

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/IBM/sarama"
)

// retentionAdmin fakes the cluster admin for the retention check. Existing tests of
// prepareTopic run against a real broker in the servers suite; this pins only the
// existing-topic branch.
type retentionAdmin struct {
	exists    bool
	retention string
	configErr error
	created   []string
	details   []*sarama.TopicDetail
}

func (a *retentionAdmin) DescribeTopics(topics []string) ([]*sarama.TopicMetadata, error) {
	if a.exists {
		return []*sarama.TopicMetadata{{
			Name:       topics[0],
			Err:        sarama.ErrNoError,
			Partitions: []*sarama.PartitionMetadata{{}},
		}}, nil
	}
	return []*sarama.TopicMetadata{{Name: topics[0], Err: sarama.ErrUnknownTopicOrPartition}}, nil
}

func (a *retentionAdmin) CreateTopic(topic string, detail *sarama.TopicDetail, validateOnly bool) error {
	a.created = append(a.created, topic)
	a.details = append(a.details, detail)
	return nil
}

func (a *retentionAdmin) DescribeConfig(resource sarama.ConfigResource) ([]sarama.ConfigEntry, error) {
	if !a.exists {
		return nil, errors.New("topic does not exist")
	}
	if a.configErr != nil {
		return nil, a.configErr
	}
	return []sarama.ConfigEntry{{Name: "retention.ms", Value: a.retention}}, nil
}

func TestPrepareTopicRejectsWrongRetentionOnExistingTopic(t *testing.T) {
	admin := &retentionAdmin{exists: true, retention: "604800000"}

	publisher := &EventPublisher{logger: slog.Default(), kafkaTopicRetention: DefaultTopicRetention}
	err := publisher.prepareTopic(context.Background(), admin, "osac.events.acme")

	if err == nil || !strings.Contains(err.Error(), "fix the topic's configuration") {
		t.Fatalf("expected the wrong retention to be refused, got %v", err)
	}
	if len(admin.created) != 0 {
		t.Fatalf("an existing topic must not be recreated, created %v", admin.created)
	}
}

func TestPrepareTopicAcceptsMatchingRetentionOnExistingTopic(t *testing.T) {
	admin := &retentionAdmin{exists: true, retention: "-1"}

	publisher := &EventPublisher{logger: slog.Default(), kafkaTopicRetention: DefaultTopicRetention}
	err := publisher.prepareTopic(context.Background(), admin, "osac.events.acme")

	if err != nil {
		t.Fatalf("matching retention must be accepted, got %v", err)
	}
}

// TestPrepareTopicSetsFullRetentionContractOnCreation pins every config entry of a
// created topic: the records are the recovery source, so a broker size limit or a
// compacting cleanup default must not be able to shorten or rewrite them.
func TestPrepareTopicSetsFullRetentionContractOnCreation(t *testing.T) {
	admin := &retentionAdmin{}

	publisher := &EventPublisher{logger: slog.Default(), kafkaTopicRetention: DefaultTopicRetention}
	err := publisher.prepareTopic(context.Background(), admin, "osac.events.acme")

	if err != nil {
		t.Fatalf("prepareTopic: %v", err)
	}
	if len(admin.details) != 1 {
		t.Fatalf("expected the missing topic to be created, created %v", admin.created)
	}
	entries := admin.details[0].ConfigEntries
	retentionMS := entries["retention.ms"]
	if retentionMS == nil || *retentionMS != "-1" {
		t.Fatalf("expected retention.ms -1, got %v", retentionMS)
	}
	retentionBytes := entries["retention.bytes"]
	if retentionBytes == nil || *retentionBytes != "-1" {
		t.Fatalf("expected retention.bytes -1, got %v", retentionBytes)
	}
	cleanupPolicy := entries["cleanup.policy"]
	if cleanupPolicy == nil || *cleanupPolicy != "delete" {
		t.Fatalf("expected cleanup.policy delete, got %v", cleanupPolicy)
	}
}

// TestPrepareTopicRefusesUnverifiableRetention pins the fail-closed branch: without
// the DescribeConfigs permission the retention check can never run, so every
// existing topic would be accepted without verification. A denied read is a
// deployment misconfiguration someone must fix, not an outage to retry through.
// The error is the resource-level shape sarama's cluster admin returns.
func TestPrepareTopicRefusesUnverifiableRetention(t *testing.T) {
	admin := &retentionAdmin{
		exists: true,
		configErr: &sarama.DescribeConfigError{
			Err: sarama.ErrTopicAuthorizationFailed,
		},
	}

	publisher := &EventPublisher{logger: slog.Default(), kafkaTopicRetention: DefaultTopicRetention}
	err := publisher.prepareTopic(context.Background(), admin, "osac.events.acme")

	if err == nil || !strings.Contains(err.Error(), "DescribeConfigs") {
		t.Fatalf("expected the denied config read to be refused, got %v", err)
	}
	if len(admin.created) != 0 {
		t.Fatalf("an existing topic must not be recreated, created %v", admin.created)
	}
}

// TestPrepareTopicContinuesWhenRetentionConfigUnreadable checks the other branch:
// a read that fails for a non-authorization reason is an outage, not a signal
// about the topic, and publication continues like today.
func TestPrepareTopicContinuesWhenRetentionConfigUnreadable(t *testing.T) {
	admin := &retentionAdmin{exists: true, configErr: sarama.ErrOutOfBrokers}

	publisher := &EventPublisher{logger: slog.Default(), kafkaTopicRetention: DefaultTopicRetention}
	err := publisher.prepareTopic(context.Background(), admin, "osac.events.acme")

	if err != nil {
		t.Fatalf("an unreadable config must be an outage, not a topic problem, got %v", err)
	}
}

// TestPrepareTopicRefusesAMultiPartitionTopic checks that an existing topic with more
// than one partition is refused: the publisher and metering's catch-up assume one
// partition per topic, ordered by offset.
func TestPrepareTopicRefusesAMultiPartitionTopic(t *testing.T) {
	admin := &multiPartitionAdmin{topic: "osac.events.acme"}

	publisher := &EventPublisher{logger: slog.Default(), kafkaTopicRetention: DefaultTopicRetention}
	err := publisher.prepareTopic(context.Background(), admin, "osac.events.acme")

	if err == nil || !strings.Contains(err.Error(), "2 partitions, want 1") {
		t.Fatalf("a multi-partition topic must be refused, got %v", err)
	}
}

// multiPartitionAdmin describes an existing topic with two partitions.
type multiPartitionAdmin struct {
	topic string
}

func (a *multiPartitionAdmin) DescribeTopics(topics []string) ([]*sarama.TopicMetadata, error) {
	return []*sarama.TopicMetadata{{
		Name:       a.topic,
		Err:        sarama.ErrNoError,
		Partitions: []*sarama.PartitionMetadata{{}, {}},
	}}, nil
}

func (a *multiPartitionAdmin) CreateTopic(string, *sarama.TopicDetail, bool) error {
	return nil
}

func (a *multiPartitionAdmin) DescribeConfig(sarama.ConfigResource) ([]sarama.ConfigEntry, error) {
	return nil, nil
}
