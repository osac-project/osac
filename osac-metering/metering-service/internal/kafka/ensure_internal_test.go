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
	if got := configValue(t, detail, "cleanup.policy"); got == nil || *got != "delete" {
		t.Fatalf("expected cleanup.policy delete, got %v", got)
	}
}

func TestEnsureTopicLeavesExistingTopicUntouched(t *testing.T) {
	admin := &fakeAdmin{description: &sarama.TopicMetadata{Err: sarama.ErrNoError}}

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
		description: &sarama.TopicMetadata{Err: sarama.ErrNoError},
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
		description: &sarama.TopicMetadata{Err: sarama.ErrNoError},
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
