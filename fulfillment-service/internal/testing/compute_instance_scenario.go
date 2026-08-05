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

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"gopkg.in/yaml.v3"
)

// ComputeInstanceScenario represents test data for compute instances and templates
type ComputeInstanceScenario struct {
	Name        string
	Description string
	Templates   []*TemplateData
	Instances   []*InstanceData
}

// TemplateData contains compute instance template data
type TemplateData struct {
	ID          string
	Name        string
	Title       string
	Description string
}

// InstanceData contains compute instance data
type InstanceData struct {
	ID                string
	Name              string
	Template          string
	State             publicv1.ComputeInstanceState
	InternalIPAddress string
	ExternalIPAddress string
}

// YAML parsing structures
type computeInstanceScenarioFile struct {
	Name        string          `yaml:"name"`
	Description string          `yaml:"description"`
	Templates   []*templateFile `yaml:"templates"`
	Instances   []*instanceFile `yaml:"instances"`
}

type templateFile struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

type instanceFile struct {
	ID                string `yaml:"id"`
	Name              string `yaml:"name"`
	Template          string `yaml:"template"`
	State             string `yaml:"state"`
	InternalIPAddress string `yaml:"internalIpAddress"`
	ExternalIPAddress string `yaml:"externalIpAddress"`
}

// LoadComputeInstanceScenarioFromFile loads a compute instance scenario from a YAML file
func LoadComputeInstanceScenarioFromFile(filename string) (*ComputeInstanceScenario, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read scenario file: %w", err)
	}

	var file computeInstanceScenarioFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("failed to parse scenario YAML: %w", err)
	}

	return file.toScenario()
}

// toScenario converts a computeInstanceScenarioFile to a ComputeInstanceScenario with proper proto enums
func (f *computeInstanceScenarioFile) toScenario() (*ComputeInstanceScenario, error) {
	scenario := &ComputeInstanceScenario{
		Name:        f.Name,
		Description: f.Description,
		Templates:   make([]*TemplateData, len(f.Templates)),
		Instances:   make([]*InstanceData, len(f.Instances)),
	}

	for i, t := range f.Templates {
		scenario.Templates[i] = &TemplateData{
			ID:          t.ID,
			Name:        t.Name,
			Title:       t.Title,
			Description: t.Description,
		}
	}

	for i, inst := range f.Instances {
		rawState, ok := publicv1.ComputeInstanceState_value[inst.State]
		if !ok {
			return nil, fmt.Errorf("invalid compute instance state %q for instance %q", inst.State, inst.ID)
		}
		scenario.Instances[i] = &InstanceData{
			ID:                inst.ID,
			Name:              inst.Name,
			Template:          inst.Template,
			State:             publicv1.ComputeInstanceState(rawState),
			InternalIPAddress: inst.InternalIPAddress,
			ExternalIPAddress: inst.ExternalIPAddress,
		}
	}

	return scenario, nil
}

// ToProtoTemplate converts TemplateData to a proto ComputeInstanceTemplate
func (t *TemplateData) ToProtoTemplate() *publicv1.ComputeInstanceTemplate {
	return &publicv1.ComputeInstanceTemplate{
		Id: t.ID,
		Metadata: &publicv1.Metadata{
			Name: t.Name,
		},
		Title:       t.Title,
		Description: t.Description,
	}
}

// ToProtoInstance converts InstanceData to a proto ComputeInstance
func (i *InstanceData) ToProtoInstance() *publicv1.ComputeInstance {
	return &publicv1.ComputeInstance{
		Id: i.ID,
		Metadata: &publicv1.Metadata{
			Name: i.Name,
		},
		Spec: &publicv1.ComputeInstanceSpec{
			Template: i.Template,
		},
		Status: &publicv1.ComputeInstanceStatus{
			State:             i.State,
			InternalIpAddress: i.InternalIPAddress,
			ExternalIpAddress: i.ExternalIPAddress,
		},
	}
}
