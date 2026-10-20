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
	created   []string
}

func (a *retentionAdmin) DescribeTopics(topics []string) ([]*sarama.TopicMetadata, error) {
	if a.exists {
		return []*sarama.TopicMetadata{{Name: topics[0], Err: sarama.ErrNoError}}, nil
	}
	return []*sarama.TopicMetadata{{Name: topics[0], Err: sarama.ErrUnknownTopicOrPartition}}, nil
}

func (a *retentionAdmin) CreateTopic(topic string, detail *sarama.TopicDetail, validateOnly bool) error {
	a.created = append(a.created, topic)
	return nil
}

func (a *retentionAdmin) DescribeConfig(resource sarama.ConfigResource) ([]sarama.ConfigEntry, error) {
	if !a.exists {
		return nil, errors.New("topic does not exist")
	}
	return []sarama.ConfigEntry{{Name: "retention.ms", Value: a.retention}}, nil
}

func TestPrepareTopicRejectsWrongRetentionOnExistingTopic(t *testing.T) {
	admin := &retentionAdmin{exists: true, retention: "604800000"}

	err := prepareTopic(context.Background(), admin, "osac.events.acme", "-1", slog.Default())

	if err == nil || !strings.Contains(err.Error(), "fix the topic's configuration") {
		t.Fatalf("expected the wrong retention to be refused, got %v", err)
	}
	if len(admin.created) != 0 {
		t.Fatalf("an existing topic must not be recreated, created %v", admin.created)
	}
}

func TestPrepareTopicAcceptsMatchingRetentionOnExistingTopic(t *testing.T) {
	admin := &retentionAdmin{exists: true, retention: "-1"}

	err := prepareTopic(context.Background(), admin, "osac.events.acme", "-1", slog.Default())

	if err != nil {
		t.Fatalf("matching retention must be accepted, got %v", err)
	}
}
