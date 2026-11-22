/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package realmauth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	itesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
)

type keycloakFixture struct {
	issuer           string
	secrets          secretStoreFixture
	certFile         string
	fullScopeAllowed bool
}

// The only stand-in on the import path is the Kubernetes Secret store. Execute
// the shipped resolver unchanged, supplying disposable secrets through `oc`.
// Unknown commands fail closed, so setup cannot repair roles or scope mappings.
const secretStore = `#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  "get secret keycloak-client-secrets"*)
    if [[ "$*" == *"-o jsonpath="* ]]; then
      printf '%s' "${REALM_TEST_SECRET}" | base64 | tr -d '\n'
    fi
    ;;
  *) exit 1 ;;
esac
`

func startKeycloak(ctx context.Context, source string) keycloakFixture {
	GinkgoHelper()
	root, err := filepath.Abs("../../..")
	Expect(err).ToNot(HaveOccurred())
	installer := filepath.Join(root, "osac-installer")
	chart := filepath.Join(installer, "charts/osac-infra")
	prerequisites := filepath.Join(installer, "prerequisites/keycloak/service")
	var values struct {
		Keycloak struct {
			Images struct {
				Keycloak string `yaml:"keycloak"`
			} `yaml:"images"`
		} `yaml:"keycloak"`
	}
	readYAML(filepath.Join(chart, "values.yaml"), &values)
	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					InitContainers []struct {
						Name    string   `yaml:"name"`
						Command []string `yaml:"command"`
					} `yaml:"initContainers"`
					Containers []struct {
						Name  string `yaml:"name"`
						Image string `yaml:"image"`
					} `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	readYAML(filepath.Join(prerequisites, "deployment.yaml"), &deployment)

	// Ginkgo owns the private parent directory and removes it after cleanup.
	dir := GinkgoT().TempDir()
	importDir := filepath.Join(dir, "import")
	configDir := filepath.Join(dir, "config")
	binDir := filepath.Join(dir, "bin")
	for _, path := range []string{importDir, configDir, binDir} {
		Expect(os.Mkdir(path, 0755)).To(Succeed())
	}
	secrets := newSecretStoreFixture(binDir)
	resolved := filepath.Join(importDir, "osac-realm.json")
	raw := filepath.Join(chart, "files/realm.json")
	script := filepath.Join(chart, "files/hooks/resolve-realm-secrets.sh")
	image := values.Keycloak.Images.Keycloak
	if source == "prerequisites" {
		raw = filepath.Join(prerequisites, "files/realm.json")
		script = filepath.Join(dir, "resolve.sh")
		found := false
		for _, container := range deployment.Spec.Template.Spec.InitContainers {
			if container.Name == "resolve-realm-secrets" {
				Expect(container.Command).To(HaveLen(3))
				// The static manifest uses absolute mount paths. Redirect only
				// those paths, as in the installer's UI redirect URI test.
				body := strings.NewReplacer(
					"/realm-raw/realm.json", shellQuote(raw),
					"/realm/realm.json", shellQuote(resolved),
				).Replace(container.Command[2])
				Expect(os.WriteFile(script, []byte(body), 0600)).To(Succeed())
				found = true
			}
		}
		Expect(found).To(BeTrue(), "the prerequisite resolver must exist")
		image = ""
		for _, container := range deployment.Spec.Template.Spec.Containers {
			if container.Name == "keycloak" {
				image = container.Image
			}
		}
	}
	Expect(image).ToNot(BeEmpty())
	command := exec.CommandContext(ctx, "bash", script)
	command.Env = append(secrets.environment(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KEYCLOAK_NAMESPACE=keycloak",
		"REALM_ADMIN_USERNAME=realm-test-admin", "REALM_ADMIN_PASSWORD="+secrets.realmAdmin,
		"OSAC_UI_URL=https://localhost", "REALM_RAW_PATH="+raw, "REALM_OUTPUT_PATH="+resolved,
	)
	// Don't print resolver output: future resolver errors could contain credentials.
	Expect(command.Run()).To(Succeed(), "the shipped secret resolver must succeed")

	var realm struct {
		Clients []struct {
			ClientID         string `json:"clientId"`
			FullScopeAllowed *bool  `json:"fullScopeAllowed"`
		} `json:"clients"`
	}
	data, err := os.ReadFile(resolved)
	Expect(err).ToNot(HaveOccurred())
	Expect(json.Unmarshal(data, &realm)).To(Succeed())
	fullScopeAllowed := true
	for _, client := range realm.Clients {
		if client.ClientID == "osac-csi-driver" && client.FullScopeAllowed != nil {
			fullScopeAllowed = *client.FullScopeAllowed
		}
	}

	cert, key, ca := itesting.LocalhostCertificateFiles()
	DeferCleanup(os.Remove, ca)
	certFile := filepath.Join(configDir, "tls.crt")
	keyFile := filepath.Join(configDir, "tls.key")
	Expect(os.Rename(cert, certFile)).To(Succeed())
	Expect(os.Rename(key, keyFile)).To(Succeed())
	// Container-readable files remain under the private test directory on the host.
	for _, path := range []string{certFile, keyFile, resolved} {
		Expect(os.Chmod(path, 0644)).To(Succeed())
	}
	tool := os.Getenv("CONTAINER_TOOL")
	if tool == "" {
		tool, err = exec.LookPath("podman")
		if err != nil {
			tool, err = exec.LookPath("docker")
			Expect(err).ToNot(HaveOccurred(), "Podman or Docker is required; this contract must not skip")
		}
	}
	id := runContainerCommand(ctx, tool, "run", "--detach",
		"--publish", "127.0.0.1::8443",
		"--volume", importDir+":/opt/keycloak/data/import:ro,Z",
		"--volume", configDir+":/test-config:ro,Z",
		image, "start", "--db=dev-file", "--hostname-strict=false", "--http-enabled=false",
		"--https-certificate-file=/test-config/tls.crt", "--https-certificate-key-file=/test-config/tls.key",
		"--import-realm",
	)
	DeferCleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runContainerCommand(cleanupCtx, tool, "rm", "--force", id)
	})
	address := runContainerCommand(ctx, tool, "port", id, "8443/tcp")
	Expect(address).To(HavePrefix("127.0.0.1:"))
	fmt.Fprintf(GinkgoWriter, "realm=%s image=%s fresh_startup_import=true\n", source, image)
	return keycloakFixture{
		issuer: "https://" + address + "/realms/osac", secrets: secrets,
		certFile: certFile, fullScopeAllowed: fullScopeAllowed,
	}
}

func readYAML(path string, target any) {
	GinkgoHelper()
	data, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	Expect(yaml.Unmarshal(data, target)).To(Succeed())
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func runContainerCommand(ctx context.Context, tool string, args ...string) string {
	GinkgoHelper()
	output, err := exec.CommandContext(ctx, tool, args...).Output()
	Expect(err).ToNot(HaveOccurred(), "container command %s must succeed", args[0])
	return strings.TrimSpace(string(output))
}
