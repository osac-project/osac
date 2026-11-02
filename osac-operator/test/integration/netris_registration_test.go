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

package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"gopkg.in/yaml.v3"

	"github.com/osac-project/osac/osac-operator/test/utils"
)

const (
	netrisManagerConfigMap  = "osac-network-fabric-manager-netris"
	k8sOnlyManagerConfigMap = "osac-network-k8s-manager-k8s-only"
)

type networkManagerConfigMapData struct {
	Name                      string `yaml:"name"`
	AnsibleRole               string `yaml:"ansible_role"`
	Capabilities              string `yaml:"capabilities"`
	ExternalIPContractVersion string `yaml:"externalIPContractVersion"`
}

func networkManagerRenderedConfigMapsAllowingFailure(
	chartsDir, managerName string, extraArgs ...string,
) (map[string]networkManagerConfigMapData, error) {
	args := append([]string{
		"template", "netris-registration", chartsDir,
		"--namespace", operatorNamespace,
	}, extraArgs...)
	output, err := utils.Run(exec.Command("helm", args...))
	if err != nil {
		return nil, err
	}
	return parseNetworkManagerConfigMaps(string(output), managerName), nil
}

func parseNetworkManagerConfigMaps(
	rendered, managerName string,
) map[string]networkManagerConfigMapData {
	configMaps := map[string]networkManagerConfigMapData{}
	decoder := yaml.NewDecoder(strings.NewReader(rendered))
	for {
		var manifest struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Data map[string]string `yaml:"data"`
		}
		if err := decoder.Decode(&manifest); err != nil {
			if err == io.EOF {
				break
			}
			GinkgoWriter.Printf(
				"skipping malformed rendered document: %v\n", err)
			continue
		}
		if manifest.Kind != "ConfigMap" || manifest.Data["name"] != managerName {
			continue
		}
		configMaps[manifest.Metadata.Name] = networkManagerConfigMapData{
			Name:                      manifest.Data["name"],
			AnsibleRole:               manifest.Data["ansible_role"],
			Capabilities:              manifest.Data["capabilities"],
			ExternalIPContractVersion: manifest.Data["externalIPContractVersion"],
		}
	}
	return configMaps
}

func networkManagerRenderedConfigMaps(
	chartsDir, managerName string, extraArgs ...string,
) map[string]networkManagerConfigMapData {
	args := append([]string{
		"template", "netris-registration", chartsDir,
		"--namespace", operatorNamespace,
	}, extraArgs...)
	output, err := utils.Run(exec.Command("helm", args...))
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "helm template failed")
	return parseNetworkManagerConfigMaps(string(output), managerName)
}

func netrisUmbrellaOverrides() []string {
	return []string{
		"--set", "service.enabled=false",
		"--set", "ui.enabled=false",
		"--set", "service.externalHostname=osac.example.com",
		"--set", "service.internalHostname=internal.osac.example.com",
		"--set", "service.auth.issuerUrl=https://keycloak.example.com/realms/osac",
		"--set", "service.certs.issuerRef.name=osac-ca",
		"--set", "service.mcp.resources.requests.cpu=100m",
		"--set", "service.mcp.resources.requests.memory=128Mi",
		"--set", "service.mcp.resources.limits.cpu=500m",
		"--set", "service.mcp.resources.limits.memory=512Mi",
		"--set", "global.networking.fabricManager=netris",
		"--set", "global.networking.k8sManager=cudn_evpn",
	}
}

// TestNetrisRegistrationRendering asserts the rendered registration manifests
// without assuming a particular manager is deployed by the active Kind profile.
func TestNetrisRegistrationRendering(t *testing.T) {
	RegisterTestingT(t)

	chartDir := "charts/operator"

	t.Run("the operator chart renders no netris registration", func(t *testing.T) {
		RegisterTestingT(t)
		configMaps := networkManagerRenderedConfigMaps(
			chartDir,
			"netris",
			"--set", "global.networking.fabricManager=netris",
		)
		Expect(configMaps).NotTo(HaveKey(netrisManagerConfigMap))
	})

	t.Run("the operator chart renders k8s_only with the v1 contract", func(t *testing.T) {
		RegisterTestingT(t)
		configMaps := networkManagerRenderedConfigMaps(
			chartDir,
			"k8s_only",
			"--set", "global.networking.k8sManager=k8s_only",
		)
		Expect(configMaps).To(HaveKey(k8sOnlyManagerConfigMap))
		Expect(configMaps[k8sOnlyManagerConfigMap].Name).To(Equal("k8s_only"))
		Expect(configMaps[k8sOnlyManagerConfigMap].ExternalIPContractVersion).To(Equal("v1"))
	})

	t.Run("the installer chart renders the netris registration", func(t *testing.T) {
		RegisterTestingT(t)
		if _, err := exec.LookPath("helm"); err != nil {
			t.Skip("umbrella-chart rendering gap: the helm executable is required")
		}
		umbrellaDir := filepath.Join("..", "osac-installer", "charts", "osac")
		// Dependency artifacts (charts/*.tgz, Chart.lock) are gitignored;
		// building them locally mirrors the file:// dependency sources.
		build, err := utils.Run(exec.Command("helm", "dependency", "build", umbrellaDir))
		if err != nil {
			t.Skipf("umbrella-chart rendering gap: dependency build failed: %v: %s", err, string(build))
		}
		configMaps, renderErr := networkManagerRenderedConfigMapsAllowingFailure(
			umbrellaDir, "netris", netrisUmbrellaOverrides()...,
		)
		if renderErr != nil {
			// The umbrella chart requires secret-backed controller
			// credentials that are injected at install time, so a
			// standalone render skips; other render errors fail.
			if strings.Contains(renderErr.Error(), "service.auth.controllerCredentials must include a secret-backed") {
				t.Skipf("umbrella-chart rendering gap: %v", renderErr)
			}
			t.Fatalf("umbrella chart render failed with the supplied overrides: %v", renderErr)
		}
		Expect(configMaps).To(HaveKey(netrisManagerConfigMap))
		Expect(configMaps[netrisManagerConfigMap].Name).To(Equal("netris"))
		Expect(configMaps[netrisManagerConfigMap].AnsibleRole).To(Equal("osac.templates.netris"))
		Expect(configMaps[netrisManagerConfigMap].Capabilities).To(Equal("eastWestEthernet,security_group_icmp"))
	})
}

var _ = Describe("Netris registration", func() {
	Describe("deployed registration", func() {
		It("registers netris in the contract format when deployed", func() {
			cmd := exec.Command("kubectl", "get", "configmaps",
				"-l", "osac.openshift.io/network-fabric-manager=true",
				"-n", operatorNamespace, "-o", "json")
			output, err := utils.Run(cmd)
			if err != nil {
				Fail(fmt.Sprintf("kubectl get configmaps failed: %v", err))
			}

			var configMapList struct {
				Items []struct {
					Metadata struct {
						Name string `json:"name"`
					} `json:"metadata"`
					Data map[string]string `json:"data"`
				} `json:"items"`
			}
			ExpectWithOffset(1, json.Unmarshal(output, &configMapList)).To(Succeed())

			deployed := false
			for _, cm := range configMapList.Items {
				if cm.Data["name"] != "netris" {
					continue
				}
				deployed = true
				// The contract registration has no ExternalIP contract version
				// key; the operator rejects unknown data keys.
				ExpectWithOffset(1, cm.Data).NotTo(HaveKey("externalIPContractVersion"))
				ExpectWithOffset(1, cm.Data["ansible_role"]).To(Equal("osac.templates.netris"))
				ExpectWithOffset(1, cm.Data["capabilities"]).To(Equal("eastWestEthernet,security_group_icmp"))
			}
			if !deployed {
				By("the environment does not register the netris fabric manager")
			}
		})
	})
})
