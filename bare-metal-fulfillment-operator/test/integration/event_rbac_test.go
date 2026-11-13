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
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

// The warning is emitted by the deployed controller after it assigns a host
// whose selected port has no MAC mapping. Seeing the Event in events.k8s.io
// proves the controller's service account can use the API its recorder targets.
var _ = Describe("BMF event RBAC", func() {
	It("delivers controller events through events.k8s.io", func() {
		name := fmt.Sprintf("event-rbac-bmf-%d", time.Now().UnixNano())
		hostName := name + "-host"
		DeferCleanup(func() { cleanupBMFDelete("BareMetalHost", hostName) })
		createAvailableBareMetalHost(hostName, namespace, name)

		cmd := exec.Command("kubectl", "create", "-f", "-")
		cmd.Stdin = createNetworkedBareMetalInstanceYAML(name, namespace, name)
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		DeferCleanup(func() { cleanupBMFDelete("BareMetalInstance", name) })

		deployedServiceAccount, err := kubectlJSONPath("deployment", "bmf-operator-controller-manager",
			namespace, "{.spec.template.spec.serviceAccountName}")
		Expect(err).NotTo(HaveOccurred())
		Expect(deployedServiceAccount).To(Equal(serviceAccountName))

		Eventually(func() (bool, error) {
			return hasSelectedPortMACUnavailableEvent(name)
		}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
			"the deployed BMF service account should create an events.k8s.io Event")
	})
})

func hasSelectedPortMACUnavailableEvent(instanceName string) (bool, error) {
	output, err := exec.Command("kubectl", "get", "events.events.k8s.io", "-n", namespace, "-o", "json").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("list events.events.k8s.io: %w: %s", err, output)
	}
	var list struct {
		Items []struct {
			Reason    string `json:"reason"`
			Regarding struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
			} `json:"regarding"`
		} `json:"items"`
	}
	if err := json.Unmarshal(output, &list); err != nil {
		return false, fmt.Errorf("decode events.events.k8s.io response: %w", err)
	}
	for _, event := range list.Items {
		if event.Regarding.Kind == "BareMetalInstance" && event.Regarding.Name == instanceName &&
			event.Reason == "SelectedPortMACUnavailable" {
			return true, nil
		}
	}
	return false, nil
}
