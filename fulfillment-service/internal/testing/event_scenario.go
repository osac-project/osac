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
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

// EventScenario represents a test scenario with a sequence of events
type EventScenario struct {
	Name        string
	Description string
	Events      []*ScenarioEvent
}

// ScenarioEvent represents a single event in a test scenario
type ScenarioEvent struct {
	ID           string
	Type         publicv1.EventType
	DelaySeconds int
	Cluster      *ClusterEventData
}

// ClusterEventData contains cluster-specific event data
type ClusterEventData struct {
	ID         string
	Name       string
	State      publicv1.ClusterState
	Conditions []*ConditionData
}

// ConditionData represents a condition in the event
type ConditionData struct {
	Type    publicv1.ClusterConditionType
	Status  publicv1.ConditionStatus
	Message string
}

// YAML parsing structures - used only for loading from YAML files
type scenarioFile struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Events      []*eventFile `yaml:"events"`
}

type eventFile struct {
	ID           string            `yaml:"id"`
	Type         string            `yaml:"type"`
	DelaySeconds int               `yaml:"delaySeconds"`
	Cluster      *clusterEventFile `yaml:"cluster,omitempty"`
}

type clusterEventFile struct {
	ID         string           `yaml:"id"`
	Name       string           `yaml:"name"`
	State      string           `yaml:"state"`
	Conditions []*conditionFile `yaml:"conditions,omitempty"`
}

type conditionFile struct {
	Type    string `yaml:"type"`
	Status  string `yaml:"status"`
	Message string `yaml:"message"`
}

// LoadScenarioFromFile loads an event scenario from a YAML file
func LoadScenarioFromFile(filename string) (*EventScenario, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read scenario file: %w", err)
	}

	var file scenarioFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("failed to parse scenario YAML: %w", err)
	}

	return file.toEventScenario(), nil
}

// toEventScenario converts a scenarioFile to an EventScenario with proper proto enums
func (sf *scenarioFile) toEventScenario() *EventScenario {
	scenario := &EventScenario{
		Name:        sf.Name,
		Description: sf.Description,
		Events:      make([]*ScenarioEvent, len(sf.Events)),
	}

	for i, fileEvent := range sf.Events {
		scenario.Events[i] = &ScenarioEvent{
			ID:           fileEvent.ID,
			Type:         publicv1.EventType(publicv1.EventType_value[fileEvent.Type]),
			DelaySeconds: fileEvent.DelaySeconds,
		}

		if fileEvent.Cluster != nil {
			scenario.Events[i].Cluster = &ClusterEventData{
				ID:    fileEvent.Cluster.ID,
				Name:  fileEvent.Cluster.Name,
				State: publicv1.ClusterState(publicv1.ClusterState_value[fileEvent.Cluster.State]),
			}

			if len(fileEvent.Cluster.Conditions) > 0 {
				scenario.Events[i].Cluster.Conditions = make([]*ConditionData, len(fileEvent.Cluster.Conditions))
				for j, fileCond := range fileEvent.Cluster.Conditions {
					scenario.Events[i].Cluster.Conditions[j] = &ConditionData{
						Type:    publicv1.ClusterConditionType(publicv1.ClusterConditionType_value[fileCond.Type]),
						Status:  publicv1.ConditionStatus(publicv1.ConditionStatus_value[fileCond.Status]),
						Message: fileCond.Message,
					}
				}
			}
		}
	}

	return scenario
}

// ToProtoEvent converts a ScenarioEvent to a proto Event
func (se *ScenarioEvent) ToProtoEvent() *publicv1.Event {
	event := &publicv1.Event{
		Id:   se.ID,
		Type: se.Type,
	}

	if se.Cluster != nil {
		cluster := &publicv1.Cluster{
			Id: se.Cluster.ID,
			Metadata: &publicv1.Metadata{
				Name: se.Cluster.Name,
			},
			Status: &publicv1.ClusterStatus{
				State: se.Cluster.State,
			},
		}

		if len(se.Cluster.Conditions) > 0 {
			cluster.Status.Conditions = make([]*publicv1.ClusterCondition, len(se.Cluster.Conditions))
			for i, cond := range se.Cluster.Conditions {
				msg := cond.Message
				cluster.Status.Conditions[i] = &publicv1.ClusterCondition{
					Type:    cond.Type,
					Status:  cond.Status,
					Message: &msg,
				}
			}
		}

		event.Payload = &publicv1.Event_Cluster{
			Cluster: cluster,
		}
	}

	return event
}
