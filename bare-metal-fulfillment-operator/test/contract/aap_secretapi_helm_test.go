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

package contract

import (
	"testing"
)

// TestHelmDeploymentDefaultOmitsAAPTemplatePrefix verifies the default
// render keeps the operator's built-in "osac" template prefix (no env var),
// so installs that do not share an AAP render exactly as before.
func TestHelmDeploymentDefaultOmitsAAPTemplatePrefix(t *testing.T) {
	out, err := renderOperatorDeployment(t)
	if err != nil {
		t.Fatalf("helm template failed with default values: %v\noutput:\n%s", err, string(out))
	}
	deployment := parseRenderedDeployment(t, out)
	manager := findManagerContainer(t, deployment)
	for _, env := range manager.Env {
		if env.Name == "OSAC_AAP_TEMPLATE_PREFIX" {
			t.Fatalf("unexpected OSAC_AAP_TEMPLATE_PREFIX env var with default values: %+v", env)
		}
	}
	foundVol := findVolume(deployment, "secret-api-client-credentials")
	if foundVol == nil || foundVol.Secret == nil {
		t.Fatalf("secret-api-client-credentials Secret volume not found on Pod spec: %+v", foundVol)
	}
	if foundVol.Secret.SecretName != "fulfillment-controller-credentials" {
		t.Fatalf("secret-api-client-credentials secretName = %q, want the fixed default %q",
			foundVol.Secret.SecretName, "fulfillment-controller-credentials")
	}
}

// TestHelmDeploymentAAPTemplatePrefixAndCredentialsSecret verifies the
// shared-AAP wiring: the job template prefix env var and the Fulfillment
// Service credential Secret name both follow their values, the way a
// vcluster run's overlay sets them (its AAP organization prefixes every
// job template name, and its credential Secret is run-prefixed).
func TestHelmDeploymentAAPTemplatePrefixAndCredentialsSecret(t *testing.T) {
	out, err := renderOperatorDeployment(t,
		"env.aapTemplatePrefix=pr2100b1",
		"secretAPI.credentialsSecret=pr2100b1-fulfillment-controller-credentials",
	)
	if err != nil {
		t.Fatalf("helm template failed with shared-AAP values: %v\noutput:\n%s", err, string(out))
	}
	deployment := parseRenderedDeployment(t, out)
	manager := findManagerContainer(t, deployment)

	found := false
	for _, env := range manager.Env {
		if env.Name == "OSAC_AAP_TEMPLATE_PREFIX" {
			found = true
			if env.Value != "pr2100b1" {
				t.Fatalf("OSAC_AAP_TEMPLATE_PREFIX = %q, want %q", env.Value, "pr2100b1")
			}
		}
	}
	if !found {
		t.Fatalf("OSAC_AAP_TEMPLATE_PREFIX env var not found on manager container")
	}

	foundVol := findVolume(deployment, "secret-api-client-credentials")
	if foundVol == nil || foundVol.Secret == nil {
		t.Fatalf("secret-api-client-credentials Secret volume not found on Pod spec: %+v", foundVol)
	}
	if foundVol.Secret.SecretName != "pr2100b1-fulfillment-controller-credentials" {
		t.Fatalf("secret-api-client-credentials secretName = %q, want %q",
			foundVol.Secret.SecretName, "pr2100b1-fulfillment-controller-credentials")
	}
}
