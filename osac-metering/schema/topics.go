/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema

// Kafka topic names every metering consumer shares. Fulfillment's tenant onboarding creates
// the topics with these names; the metering service and the provider adapters derive them
// from here, so the naming rules exist in one copy.
const (
	// EventsPrefix is prepended to the tenant name to form the topic that carries the
	// tenant's change events.
	EventsPrefix = "osac.events."

	// HeartbeatPrefix is prepended to the tenant name to form the topic that carries the
	// tenant's metering heartbeats.
	HeartbeatPrefix = "osac.metering.heartbeat."

	// DeadLetterPrefix is prepended to the tenant name to form the topic that carries the
	// tenant's heartbeats rejected by the billing provider.
	DeadLetterPrefix = "osac.metering.dlq."
)

// BuiltinTenant reports whether a tenant is one fulfillment creates for platform use: the
// system and shared tenants. Their topics exist, but their usage is never billed. The names
// match fulfillment's auth.SystemTenant and auth.SharedTenant constants.
func BuiltinTenant(tenant string) bool {
	return tenant == "system" || tenant == "shared"
}

// TenantOfTopic returns the tenant that owns the topic, and false when the topic does not
// carry the prefix. A topic equal to the prefix itself owns no tenant.
func TenantOfTopic(topic, prefix string) (string, bool) {
	if len(topic) <= len(prefix) || topic[:len(prefix)] != prefix {
		return "", false
	}
	return topic[len(prefix):], true
}
