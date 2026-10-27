/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package kafka

import (
	"strings"
	"testing"

	"github.com/IBM/sarama"
)

// fakeAdmin records what the topic setup did.
type fakeAdmin struct {
	description       *sarama.TopicMetadata
	describeErr       error
	created           []string
	details           []*sarama.TopicDetail
	createErr         error
	configEntries     []sarama.ConfigEntry
	describeConfigErr error
}

func (f *fakeAdmin) DescribeTopics(topics []string) ([]*sarama.TopicMetadata, error) {
	return []*sarama.TopicMetadata{f.description}, f.describeErr
}

func (f *fakeAdmin) CreateTopic(topic string, detail *sarama.TopicDetail, validateOnly bool) error {
	f.created = append(f.created, topic)
	f.details = append(f.details, detail)
	return f.createErr
}

func configValue(t *testing.T, detail *sarama.TopicDetail, name string) *string {
	t.Helper()
	value, ok := detail.ConfigEntries[name]
	if !ok {
		return nil
	}
	return value
}

func TestEnsureTopicCreatesMissingTopic(t *testing.T) {
	admin := &fakeAdmin{description: &sarama.TopicMetadata{Err: sarama.ErrUnknownTopicOrPartition}}

	err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

	if err != nil {
		t.Fatalf("ensureTopic: %v", err)
	}
	if len(admin.created) != 1 || admin.created[0] != "osac.metering.heartbeat.acme" {
		t.Fatalf("expected the missing topic to be created, created %v", admin.created)
	}
	detail := admin.details[0]
	if detail.NumPartitions != 1 {
		t.Fatalf("expected one partition to keep heartbeat order, got %d", detail.NumPartitions)
	}
	if detail.ReplicationFactor != -1 {
		t.Fatalf("expected the broker default replication factor, got %d", detail.ReplicationFactor)
	}
	if got := configValue(t, detail, "retention.ms"); got == nil || *got != "-1" {
		t.Fatalf("expected retention.ms -1, got %v", got)
	}
	if got := configValue(t, detail, "retention.bytes"); got == nil || *got != "-1" {
		t.Fatalf("expected retention.bytes -1, got %v", got)
	}
	if got := configValue(t, detail, "cleanup.policy"); got == nil || *got != "delete" {
		t.Fatalf("expected cleanup.policy delete, got %v", got)
	}
}

func TestEnsureTopicLeavesExistingTopicUntouched(t *testing.T) {
	admin := &fakeAdmin{description: &sarama.TopicMetadata{Err: sarama.ErrNoError, Partitions: []*sarama.PartitionMetadata{{}}}}

	err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

	if err != nil {
		t.Fatalf("ensureTopic: %v", err)
	}
	if len(admin.created) != 0 {
		t.Fatalf("expected an existing topic to be left untouched, created %v", admin.created)
	}
}

func TestEnsureTopicToleratesConcurrentCreation(t *testing.T) {
	admin := &fakeAdmin{
		description: &sarama.TopicMetadata{Err: sarama.ErrUnknownTopicOrPartition},
		createErr:   sarama.ErrTopicAlreadyExists,
	}

	err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

	if err != nil {
		t.Fatalf("a topic another pass created in the meantime is not an error: %v", err)
	}
}

func TestEnsureTopicReportsFailures(t *testing.T) {
	cases := []struct {
		name  string
		admin *fakeAdmin
		want  string
	}{
		{
			name:  "describe error",
			admin: &fakeAdmin{describeErr: sarama.ErrOutOfBrokers},
			want:  "describing topic",
		},
		{
			name:  "describe problem",
			admin: &fakeAdmin{description: &sarama.TopicMetadata{Err: sarama.ErrTopicAuthorizationFailed}},
			want:  "describing topic",
		},
		{
			name: "create error",
			admin: &fakeAdmin{
				description: &sarama.TopicMetadata{Err: sarama.ErrUnknownTopicOrPartition},
				createErr:   sarama.ErrInvalidReplicationFactor,
			},
			want: "creating topic",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ensureTopic(c.admin, "osac.metering.heartbeat.acme", "-1")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected the error to mention %q, got %q", c.want, err.Error())
			}
		})
	}
}

func (f *fakeAdmin) DescribeConfig(resource sarama.ConfigResource) ([]sarama.ConfigEntry, error) {
	if f.describeConfigErr != nil {
		return nil, f.describeConfigErr
	}
	return f.configEntries, nil
}

// TestEnsureTopicRejectsWrongRetentionOnExistingTopic checks that a topic the service
// did not create, carrying the broker default retention, is refused loudly instead of
// accepted with a silently shrunken recovery window.
func TestEnsureTopicRejectsWrongRetentionOnExistingTopic(t *testing.T) {
	admin := &fakeAdmin{
		description: &sarama.TopicMetadata{Err: sarama.ErrNoError, Partitions: []*sarama.PartitionMetadata{{}}},
		configEntries: []sarama.ConfigEntry{
			{Name: "retention.ms", Value: "604800000"},
		},
	}

	err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

	if err == nil || !strings.Contains(err.Error(), "fix the topic's configuration") {
		t.Fatalf("expected the wrong retention to be refused, got %v", err)
	}
}

// TestEnsureTopicAcceptsMatchingRetentionOnExistingTopic checks the matching case.
func TestEnsureTopicAcceptsMatchingRetentionOnExistingTopic(t *testing.T) {
	admin := &fakeAdmin{
		description: &sarama.TopicMetadata{Err: sarama.ErrNoError, Partitions: []*sarama.PartitionMetadata{{}}},
		configEntries: []sarama.ConfigEntry{
			{Name: "retention.ms", Value: "-1"},
		},
	}

	err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

	if err != nil {
		t.Fatalf("matching retention must be accepted, got %v", err)
	}
	if len(admin.created) != 0 {
		t.Fatalf("an existing topic must not be recreated, created %v", admin.created)
	}
}

// TestEnsureTopicRejectsBrokenRetentionContract checks that verification covers the
// whole contract the create path pins, not only retention.ms: a topic with the right
// retention but a broker size limit or a compacting policy passes the old check and
// silently shrinks the recovery window the database rebuild depends on.
func TestEnsureTopicRejectsBrokenRetentionContract(t *testing.T) {
	cases := []struct {
		name    string
		entries []sarama.ConfigEntry
	}{
		{
			name: "size-limited retention",
			entries: []sarama.ConfigEntry{
				{Name: "retention.ms", Value: "-1"},
				{Name: "retention.bytes", Value: "1073741824"},
			},
		},
		{
			name: "compacting policy",
			entries: []sarama.ConfigEntry{
				{Name: "retention.ms", Value: "-1"},
				{Name: "cleanup.policy", Value: "compact"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			admin := &fakeAdmin{
				description: &sarama.TopicMetadata{
					Err: sarama.ErrNoError, Partitions: []*sarama.PartitionMetadata{{}},
				},
				configEntries: c.entries,
			}

			err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

			if err == nil || !strings.Contains(err.Error(), "fix the topic's configuration") {
				t.Fatalf("expected the broken retention contract to be refused, got %v", err)
			}
		})
	}
}

// TestEnsureTopicAcceptsTheWholeRetentionContract checks the matching case: a topic
// carrying all three pinned values, exactly as the create path sets them, passes.
func TestEnsureTopicAcceptsTheWholeRetentionContract(t *testing.T) {
	admin := &fakeAdmin{
		description: &sarama.TopicMetadata{Err: sarama.ErrNoError, Partitions: []*sarama.PartitionMetadata{{}}},
		configEntries: []sarama.ConfigEntry{
			{Name: "retention.ms", Value: "-1"},
			{Name: "retention.bytes", Value: "-1"},
			{Name: "cleanup.policy", Value: "delete"},
		},
	}

	err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

	if err != nil {
		t.Fatalf("a topic matching the whole retention contract must be accepted, got %v", err)
	}
	if len(admin.created) != 0 {
		t.Fatalf("an existing topic must not be recreated, created %v", admin.created)
	}
}

// TestEnsureTopicRefusesUnverifiableRetention pins the fail-closed branch: without
// the DescribeConfigs permission the retention check can never run, so every
// existing topic would be accepted without verification. A denied read is a
// deployment misconfiguration someone must fix, not an outage to retry through.
// The error is the resource-level shape sarama's cluster admin returns.
func TestEnsureTopicRefusesUnverifiableRetention(t *testing.T) {
	admin := &fakeAdmin{
		description: &sarama.TopicMetadata{Err: sarama.ErrNoError, Partitions: []*sarama.PartitionMetadata{{}}},
		describeConfigErr: &sarama.DescribeConfigError{
			Err: sarama.ErrTopicAuthorizationFailed,
		},
	}

	err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

	if err == nil || !strings.Contains(err.Error(), "DescribeConfigs") {
		t.Fatalf("expected the denied config read to be refused, got %v", err)
	}
}

// TestEnsureTopicContinuesWhenRetentionConfigUnreadable checks the other branch:
// a read that fails for a non-authorization reason is an outage, not a signal
// about the topic, and the topic is accepted like today.
func TestEnsureTopicContinuesWhenRetentionConfigUnreadable(t *testing.T) {
	admin := &fakeAdmin{
		description:       &sarama.TopicMetadata{Err: sarama.ErrNoError, Partitions: []*sarama.PartitionMetadata{{}}},
		describeConfigErr: sarama.ErrOutOfBrokers,
	}

	err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

	if err != nil {
		t.Fatalf("an unreadable config must be an outage, not a topic problem, got %v", err)
	}
}

// TestEnsureTopicRefusesAMultiPartitionTopic checks that an existing topic with more
// than one partition is refused: catch-up and the relay track a single offset on
// partition 0, so a topic with more partitions silently breaks the billing model.
func TestEnsureTopicRefusesAMultiPartitionTopic(t *testing.T) {
	admin := &fakeAdmin{description: &sarama.TopicMetadata{
		Err:        sarama.ErrNoError,
		Partitions: []*sarama.PartitionMetadata{{}, {}},
	}}

	err := ensureTopic(admin, "osac.metering.heartbeat.acme", "-1")

	if err == nil || !strings.Contains(err.Error(), "2 partitions, want 1") {
		t.Fatalf("a multi-partition topic must be refused, got %v", err)
	}
}
