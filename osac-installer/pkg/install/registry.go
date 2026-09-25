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

import "fmt"

// CheckOptions selects which prerequisites matrix entries DefaultChecks
// includes.
type CheckOptions struct {
	// Services are the OSAC services the target install will enable:
	// "vmaas", "caas", "bmaas", "maas", "metering". A matrix entry whose
	// requiredFor contains one of these (or "all") is included. Empty means
	// only entries required for every install ("all") are included.
	Services []string
	// Metal3 gates the Metal3-specific entries (BareMetalHost CRD,
	// Provisioning CR watchAllNamespaces). It mirrors osac-installer's
	// `bmf.metal3.enabled` Helm value: those prerequisites only apply when
	// the Metal3 bare-metal backend is going to be used, and that can't be
	// discovered from cluster state alone before OSAC installs.
	Metal3 bool
}

func (o CheckOptions) hasService(name string) bool {
	for _, service := range o.Services {
		if service == name {
			return true
		}
	}
	return false
}

// appliesTo reports whether entry should be included for opts: its
// requiredFor must match a requested service (or "all"), and its toggle (if
// any) must be satisfied.
func (e matrixEntry) appliesTo(opts CheckOptions) bool {
	matched := false
	for _, service := range e.RequiredFor {
		if service == "all" || opts.hasService(service) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	switch e.Toggle {
	case "":
		return true
	case "metal3":
		return opts.Metal3
	default:
		// Not reachable for a matrix that passes matrix_test.go's
		// self-check, which rejects any toggle outside knownToggles.
		return false
	}
}

// engines maps each matrix entry "kind" to the function that builds it into
// a runnable Check. Adding a new kind is adding one entry here and one
// engine_*.go file -- never a change to DefaultChecks or to every existing
// engine.
var engines = map[string]func(matrixEntry) (Check, error){
	"crd-exists":            buildCRDExistsCheck,
	"csv-succeeded":         buildCSVSucceededCheck,
	"resource-field-equals": buildResourceFieldEqualsCheck,
	"storageclass-default":  buildStorageClassDefaultCheck,
	"ocp-version-range":     buildOCPVersionRangeCheck,
}

// DefaultChecks returns the prerequisite checks that apply to opts, built
// from the embedded prerequisites matrix (data/prerequisites.yaml). It does
// not include Secret checks (AAP license, database credentials): those
// aren't fixed facts about OSAC's architecture, since their names are
// whatever the deployer configured in their own Helm values -- see
// SecretChecks.
func DefaultChecks(opts CheckOptions) ([]Check, error) {
	entries, err := loadMatrix()
	if err != nil {
		return nil, fmt.Errorf("failed to load prerequisites matrix: %w", err)
	}
	var checks []Check
	for _, entry := range entries {
		if !entry.appliesTo(opts) {
			continue
		}
		build, ok := engines[entry.Kind]
		if !ok {
			return nil, fmt.Errorf("prerequisite %q: unknown check kind %q", entry.ID, entry.Kind)
		}
		check, err := build(entry)
		if err != nil {
			return nil, fmt.Errorf("prerequisite %q: %w", entry.ID, err)
		}
		checks = append(checks, check)
	}
	return checks, nil
}
