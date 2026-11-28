/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package kafka

import (
	"fmt"
	"sort"
	"strings"

	"github.com/IBM/sarama"
	"github.com/osac-project/osac-metering/schema"
)

// Each tenant owns a heartbeat topic and a dead-letter topic. The shared names come from
// the schema module; they match the ones fulfillment's tenant onboarding creates. A
// deployment that runs next to others on one Kafka cluster configures its own prefixes,
// which these helpers resolve the same way the metering service does.
const (
	// HeartbeatPrefix is prepended to the tenant name to form the topic that carries the
	// tenant's metering heartbeats.
	HeartbeatPrefix = schema.HeartbeatPrefix

	// DeadLetterPrefix is prepended to the tenant name to form the topic that carries the
	// tenant's heartbeats rejected by the billing provider.
	DeadLetterPrefix = schema.DeadLetterPrefix

	// TopicInference is the shared topic that carries MaaS inference usage. The design
	// keeps MaaS metered separately from the per-tenant heartbeat pipeline, so this one
	// topic is not owned by a tenant.
	TopicInference = "osac.metering.inference"

	// InferenceDeadLetter is the dead-letter topic for records rejected from the shared
	// inference topic. It is separate from the per-tenant DLQ prefix: the inference topic
	// has no tenant, and a rejected record must never go back to the source topic, which
	// the adapters can only read.
	InferenceDeadLetter = "osac.metering.inference.dlq"
)

// ResolveHeartbeatPrefix returns prefix, or the default heartbeat prefix when prefix is empty.
func ResolveHeartbeatPrefix(prefix string) string {
	if prefix == "" {
		return HeartbeatPrefix
	}
	return prefix
}

// ResolveDeadLetterPrefix returns prefix, or the default dead-letter prefix when prefix is empty.
func ResolveDeadLetterPrefix(prefix string) string {
	if prefix == "" {
		return DeadLetterPrefix
	}
	return prefix
}

// ValidateTopicPrefixes rejects a dead-letter prefix that overlaps the heartbeat prefix,
// in either direction. Both topic families are formed from the same tenant name, so an
// overlap makes a heartbeat topic its own dead-letter topic: a record the adapter rejects
// goes back to the topic it came from, and the consumer rejects it again on every pass.
// Discovery breaks the same way, because each prefix then matches the other family's
// topics. An empty prefix uses the default.
func ValidateTopicPrefixes(heartbeatPrefix, deadLetterPrefix string) error {
	heartbeat := ResolveHeartbeatPrefix(heartbeatPrefix)
	deadLetter := ResolveDeadLetterPrefix(deadLetterPrefix)
	switch {
	case heartbeat == deadLetter:
		return fmt.Errorf("dead-letter prefix %q is the heartbeat prefix", deadLetter)
	case strings.HasPrefix(deadLetter, heartbeat):
		return fmt.Errorf("dead-letter prefix %q extends the heartbeat prefix %q",
			deadLetter, heartbeat)
	case strings.HasPrefix(heartbeat, deadLetter):
		return fmt.Errorf("heartbeat prefix %q extends the dead-letter prefix %q",
			heartbeat, deadLetter)
	}
	return nil
}

// HeartbeatTopic returns the tenant's heartbeat topic for prefix. An empty prefix uses the default.
func HeartbeatTopic(prefix, tenant string) string { return ResolveHeartbeatPrefix(prefix) + tenant }

// DeadLetterTopic returns the tenant's dead-letter topic for prefix. An empty prefix uses the default.
func DeadLetterTopic(prefix, tenant string) string { return ResolveDeadLetterPrefix(prefix) + tenant }

// HeartbeatTopics lists every tenant's heartbeat topic. Adapters discover tenants this way.
// The client's metadata cache is refreshed first: sarama only refreshes it every ten
// minutes by default, and a tenant onboarded a moment ago must be discovered now.
// An empty heartbeatPrefix uses the default.
func HeartbeatTopics(client sarama.Client, heartbeatPrefix string) ([]string, error) {
	return topicsWithPrefix(client, ResolveHeartbeatPrefix(heartbeatPrefix))
}

// DeadLetterTopics lists every tenant's dead-letter topic, like HeartbeatTopics. An empty
// deadLetterPrefix uses the default.
func DeadLetterTopics(client sarama.Client, deadLetterPrefix string) ([]string, error) {
	return topicsWithPrefix(client, ResolveDeadLetterPrefix(deadLetterPrefix))
}

// AllDeadLetterTopics lists every dead-letter topic that exists: each tenant's
// and the shared inference topic's. DLQ occupancy is reported over all of them,
// because rejected records land in either. An empty deadLetterPrefix uses the default.
func AllDeadLetterTopics(client sarama.Client, deadLetterPrefix string) ([]string, error) {
	topics, err := DeadLetterTopics(client, deadLetterPrefix)
	if err != nil {
		return nil, err
	}
	return append(topics, InferenceDeadLetter), nil
}

// ConsumeTopics returns every topic a provider adapter consumes: each metered tenant's
// heartbeat topic and the shared MaaS inference topic, so inference usage keeps flowing to
// the provider alongside the per-tenant heartbeats. An empty heartbeatPrefix uses the
// default.
func ConsumeTopics(client sarama.Client, heartbeatPrefix string) ([]string, error) {
	topics, err := HeartbeatTopics(client, heartbeatPrefix)
	if err != nil {
		return nil, err
	}
	return append(topics, TopicInference), nil
}

// topicsWithPrefix lists every topic with the prefix after refreshing the client's metadata
// cache, so a topic created a moment ago is not missed. The built-in tenants' topics are
// skipped: they are never billed.
func topicsWithPrefix(client sarama.Client, prefix string) ([]string, error) {
	if err := client.RefreshMetadata(); err != nil {
		return nil, fmt.Errorf("refreshing kafka metadata: %w", err)
	}
	names, err := client.Topics()
	if err != nil {
		return nil, fmt.Errorf("listing kafka topics: %w", err)
	}
	var topics []string
	for _, name := range names {
		tenant, ok := schema.TenantOfTopic(name, prefix)
		if !ok || schema.BuiltinTenant(tenant) {
			continue
		}
		topics = append(topics, name)
	}
	sort.Strings(topics)
	return topics, nil
}

// DeadLetterTopicOf maps a source topic to the topic its rejected records go to: a
// heartbeat topic maps to the tenant's dead-letter topic, and the shared inference topic
// maps to its own dead-letter topic. It returns the input unchanged only for topics that
// are neither, which the runner never consumes. An empty heartbeatPrefix or
// deadLetterPrefix uses the default. Overlapping prefixes are an error: the mapping would
// send a rejected heartbeat back to its own topic and the consumer would never advance.
func DeadLetterTopicOf(source, heartbeatPrefix, deadLetterPrefix string) (string, error) {
	if err := ValidateTopicPrefixes(heartbeatPrefix, deadLetterPrefix); err != nil {
		return "", err
	}
	if tenant, ok := schema.TenantOfTopic(source, ResolveHeartbeatPrefix(heartbeatPrefix)); ok {
		return DeadLetterTopic(deadLetterPrefix, tenant), nil
	}
	if source == TopicInference {
		return InferenceDeadLetter, nil
	}
	return source, nil
}
