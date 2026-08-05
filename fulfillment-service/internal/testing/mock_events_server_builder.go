/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package testing

import (
	"time"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

// MockEventsServerBuilder builds a mock events server with configurable scenarios
type MockEventsServerBuilder struct {
	scenario *EventScenario
}

// NewMockEventsServerBuilder creates a new builder for mock events server
func NewMockEventsServerBuilder() *MockEventsServerBuilder {
	return &MockEventsServerBuilder{}
}

// WithScenario sets the event scenario to use
func (b *MockEventsServerBuilder) WithScenario(scenario *EventScenario) *MockEventsServerBuilder {
	b.scenario = scenario
	return b
}

// Build creates the EventsServerFuncs with the configured scenario
// If no scenario is set, the server will send no events
func (b *MockEventsServerBuilder) Build() *EventsServerFuncs {
	return &EventsServerFuncs{
		WatchFunc: b.createWatchFunc(),
	}
}

// createWatchFunc creates a WatchFunc that sends events from the scenario
func (b *MockEventsServerBuilder) createWatchFunc() func(*publicv1.EventsWatchRequest, publicv1.Events_WatchServer) error {
	return func(request *publicv1.EventsWatchRequest, stream publicv1.Events_WatchServer) error {
		filter := request.GetFilter()

		// If no scenario is set, just wait for context cancellation
		if b.scenario != nil {
			for _, scenarioEvent := range b.scenario.Events {
				// Apply delay if specified
				if scenarioEvent.DelaySeconds > 0 {
					time.Sleep(time.Duration(scenarioEvent.DelaySeconds) * time.Second)
				}

				// Convert scenario event to proto event
				event := scenarioEvent.ToProtoEvent()

				// Send event if it matches the filter
				if err := SendEventIfMatches(event, filter, stream); err != nil {
					return err
				}
			}
		}

		// Wait for context cancellation
		<-stream.Context().Done()
		return stream.Context().Err()
	}
}
