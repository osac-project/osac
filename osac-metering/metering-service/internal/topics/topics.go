/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package topics names the Kafka topics each tenant owns. Fulfillment's event publisher and
// tenant onboarding derive the same names in fulfillment-service; the names here must stay in
// sync with them.
package topics

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

// builtinTenants are the tenants fulfillment creates for platform use. Metering never reads
// them: the design keeps the system tenant out of metering, and the shared tenant holds
// platform-scoped objects, not billable tenant usage. The names match fulfillment's
// auth.SystemTenant and auth.SharedTenant constants.
var builtinTenants = map[string]bool{
	"system": true,
	"shared": true,
}

// Metered reports whether a tenant is one metering serves. It is false only for the built-in
// system and shared tenants, whose topics exist but whose usage is never billed.
func Metered(tenant string) bool { return !builtinTenants[tenant] }

// Events returns the name of the tenant's event topic.
func Events(tenant string) string { return EventsPrefix + tenant }

// Heartbeat returns the name of the tenant's heartbeat topic.
func Heartbeat(tenant string) string { return HeartbeatPrefix + tenant }

// DeadLetter returns the name of the tenant's dead-letter topic.
func DeadLetter(tenant string) string { return DeadLetterPrefix + tenant }

// TenantOfEvents returns the tenant that owns the event topic, and false when the topic is not
// an event topic.
func TenantOfEvents(topic string) (string, bool) {
	return tenantOf(topic, EventsPrefix)
}

// TenantOfHeartbeat returns the tenant that owns the heartbeat topic, and false when the topic is
// not a heartbeat topic.
func TenantOfHeartbeat(topic string) (string, bool) {
	return tenantOf(topic, HeartbeatPrefix)
}

// TenantOfDeadLetter returns the tenant that owns the dead-letter topic, and false when the topic
// is not a dead-letter topic.
func TenantOfDeadLetter(topic string) (string, bool) {
	return tenantOf(topic, DeadLetterPrefix)
}

func tenantOf(topic, prefix string) (string, bool) {
	if len(topic) <= len(prefix) || topic[:len(prefix)] != prefix {
		return "", false
	}
	return topic[len(prefix):], true
}
