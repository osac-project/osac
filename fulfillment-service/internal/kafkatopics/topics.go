/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package kafkatopics defines the names of the Kafka topics that each tenant owns. The event publisher and tenant
// onboarding both derive topic names here, so a tenant's events always land on the topic created at its onboarding.
package kafkatopics

const (
	// EventsPrefix is prepended to the tenant name to form the topic that carries the tenant's change events.
	EventsPrefix = "osac.events."

	// HeartbeatPrefix is prepended to the tenant name to form the topic that carries the tenant's metering
	// heartbeats.
	HeartbeatPrefix = "osac.metering.heartbeat."

	// DeadLetterPrefix is prepended to the tenant name to form the topic that carries the tenant's metering
	// heartbeats rejected by the billing provider.
	DeadLetterPrefix = "osac.metering.dlq."
)

// Names returns the names of the Kafka topics of a tenant, in the order they are created at onboarding.
func Names(tenant string) []string {
	return []string{
		EventsPrefix + tenant,
		HeartbeatPrefix + tenant,
		DeadLetterPrefix + tenant,
	}
}
