/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	_ "embed"
	"fmt"
	"sync"

	"go.yaml.in/yaml/v3"
)

//go:embed data/prerequisites.yaml
var prerequisitesYAML []byte

// matrixEntry is one row of the prerequisites matrix
// (data/prerequisites.yaml): everything DefaultChecks needs to know about a
// single prerequisite, as data rather than Go code. Which fields are
// meaningful depends on Kind; see the matching engine_*.go file for which
// are required and how they're interpreted. matrix_test.go validates every
// entry in the embedded file against this shape.
type matrixEntry struct {
	// ID is the stable, machine-friendly check name (Check.Name).
	ID string `yaml:"id"`
	// Kind selects the check engine that builds this entry into a Check;
	// see the engines map in registry.go for the closed set of valid
	// values.
	Kind string `yaml:"kind"`
	// Severity is "required" or "warning"; see parseSeverity.
	Severity string `yaml:"severity"`
	// Category is "resource" or "operator"; see parseCategory. Display-only.
	Category string `yaml:"category"`
	// RequiredFor lists the OSAC services that need this prerequisite:
	// "vmaas", "caas", "bmaas", "maas", "metering", or "all".
	RequiredFor []string `yaml:"requiredFor"`
	// Toggle additionally gates this entry on cluster-state-undiscoverable
	// install intent (e.g. "metal3": only in play when the Metal3 backend
	// is going to be used). Empty means no additional gate.
	Toggle string `yaml:"toggle,omitempty"`

	// crd-exists
	CRD         string `yaml:"crd,omitempty"`
	Remediation string `yaml:"remediation,omitempty"`

	// csv-succeeded
	DisplayName   string `yaml:"displayName,omitempty"`
	Namespace     string `yaml:"namespace,omitempty"`
	PackagePrefix string `yaml:"packagePrefix,omitempty"`
	DocsURL       string `yaml:"docsURL,omitempty"`

	// csv-succeeded and ocp-version-range
	MinVersion string `yaml:"minVersion,omitempty"`

	// resource-field-equals
	Group           string   `yaml:"group,omitempty"`
	Version         string   `yaml:"version,omitempty"`
	Resource        string   `yaml:"resource,omitempty"`
	Field           []string `yaml:"field,omitempty"`
	Expected        any      `yaml:"expected,omitempty"`
	NotFoundMessage string   `yaml:"notFoundMessage,omitempty"`
	MismatchMessage string   `yaml:"mismatchMessage,omitempty"`
}

var (
	matrixOnce    sync.Once
	matrixEntries []matrixEntry
	matrixErr     error
)

// loadMatrix parses the embedded prerequisites matrix once and caches the
// result; every call after the first returns the same slice and error.
func loadMatrix() ([]matrixEntry, error) {
	matrixOnce.Do(func() {
		matrixErr = yaml.Unmarshal(prerequisitesYAML, &matrixEntries)
	})
	return matrixEntries, matrixErr
}

// parseSeverity converts the matrix's plain-text severity into a Severity.
func parseSeverity(value string) (Severity, error) {
	switch value {
	case "required":
		return Required, nil
	case "warning":
		return Warning, nil
	default:
		return 0, fmt.Errorf("unknown severity %q (want \"required\" or \"warning\")", value)
	}
}

// parseCategory converts the matrix's plain-text category into a Category.
func parseCategory(value string) (Category, error) {
	switch value {
	case "resource":
		return ResourceCategory, nil
	case "operator":
		return OperatorCategory, nil
	default:
		return 0, fmt.Errorf("unknown category %q (want \"resource\" or \"operator\")", value)
	}
}

// knownRequiredFor and knownToggles are the closed vocabularies matrix_test.go
// validates every entry's RequiredFor/Toggle values against, and appliesTo
// (registry.go) uses to filter entries. Keeping the vocabulary here, next to
// the type it constrains, means adding a new service or toggle is a
// one-line change in one place.
var (
	knownRequiredFor = map[string]bool{
		"all": true, "vmaas": true, "caas": true, "bmaas": true, "maas": true, "metering": true,
	}
	knownToggles = map[string]bool{
		"": true, "metal3": true,
	}
)
