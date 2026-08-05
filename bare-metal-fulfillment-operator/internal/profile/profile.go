/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package profile provides profile loading and matching functionality
package profile

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

var (
	registeredProfiles = map[string]*Profile{}
)

// LoadProfiles registers profiles into the internal registry
func LoadProfiles(profiles []*Profile) error {
	for _, profile := range profiles {
		if profile.Name == "" {
			return fmt.Errorf("profile missing required 'name' field")
		}
		if _, exists := registeredProfiles[profile.Name]; exists {
			return fmt.Errorf("duplicate profile name: %s", profile.Name)
		}
		for key, value := range profile.Labels {
			if errs := validation.IsQualifiedName(key); len(errs) > 0 {
				return fmt.Errorf("invalid label key '%s' in profile '%s': %v", key, profile.Name, errs)
			}
			if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
				return fmt.Errorf("invalid label value '%s' for key '%s' in profile '%s': %v", value, key, profile.Name, errs)
			}
		}
		for key, value := range profile.PersistentLabels {
			if errs := validation.IsQualifiedName(key); len(errs) > 0 {
				return fmt.Errorf("invalid persistent label key '%s' in profile '%s': %v", key, profile.Name, errs)
			}
			if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
				return fmt.Errorf("invalid persistent label value '%s' for key '%s' in profile '%s': %v", value, key, profile.Name, errs)
			}
		}
		registeredProfiles[profile.Name] = profile
	}

	return nil
}

// Get returns a registered profile or nil if it doesn't exist
func Get(name string) *Profile {
	return registeredProfiles[name]
}

// Profile defines a configuration profile with workflows and host selection
type Profile struct {
	Name                       string            `yaml:"name"`
	HostSelector               map[string]string `yaml:"hostSelector"`
	ExpectedTemplateParameters []string          `yaml:"expectedTemplateParameters"`
	OptionalTemplateParameters []string          `yaml:"optionalTemplateParameters,omitempty"`
	BareMetalPoolTemplate      string            `yaml:"bareMetalPoolTemplate,omitempty"`
	HostTemplate               string            `yaml:"hostTemplate,omitempty"`
	Labels                     map[string]string `yaml:"labels"`
	PersistentLabels           map[string]string `yaml:"persistentLabels"`
}

func (p *Profile) ValidateParameters(templateParameters string) bool {
	if templateParameters == "" {
		return len(p.ExpectedTemplateParameters) == 0
	}

	// Parse the JSON string using yaml.Unmarshal (which can handle JSON)
	var params map[string]any
	if err := yaml.Unmarshal([]byte(templateParameters), &params); err != nil {
		return false // Invalid JSON/YAML
	}

	// Build a set of all allowed parameter keys (expected + optional)
	allowedKeys := make(map[string]struct{})
	for _, key := range p.ExpectedTemplateParameters {
		allowedKeys[key] = struct{}{}
	}
	for _, key := range p.OptionalTemplateParameters {
		allowedKeys[key] = struct{}{}
	}

	// Check that all expected parameters are present in the template parameters
	for _, expectedKey := range p.ExpectedTemplateParameters {
		if _, ok := params[expectedKey]; !ok {
			return false
		}
	}

	// Check that all provided parameters are in the allowed set (expected or optional)
	for key := range params {
		if _, ok := allowedKeys[key]; !ok {
			return false
		}
	}

	return true
}
