/*
Copyright 2025.

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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/test/utils"
)

const operatorNamespace = "osac"

var _ = BeforeSuite(func() {
	By("verifying controller-manager pod is running with zero restarts")
	Eventually(func() error {
		cmd := exec.Command("kubectl", "get", "pods",
			"-l", "control-plane=controller-manager",
			"-n", operatorNamespace, "-o", "json")
		output, err := utils.Run(cmd)
		if err != nil {
			return err
		}

		var podList struct {
			Items []struct {
				Metadata struct {
					Name              string     `json:"name"`
					DeletionTimestamp *time.Time `json:"deletionTimestamp"`
				} `json:"metadata"`
				Status struct {
					Phase             string `json:"phase"`
					ContainerStatuses []struct {
						Ready        bool  `json:"ready"`
						RestartCount int64 `json:"restartCount"`
					} `json:"containerStatuses"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(output, &podList); err != nil {
			return fmt.Errorf("failed to parse pod list: %w", err)
		}

		var running int
		for _, pod := range podList.Items {
			if pod.Metadata.DeletionTimestamp != nil {
				continue
			}
			if pod.Status.Phase != "Running" {
				return fmt.Errorf("pod %s in %s phase", pod.Metadata.Name, pod.Status.Phase)
			}
			for _, cs := range pod.Status.ContainerStatuses {
				if !cs.Ready {
					return fmt.Errorf("pod %s has unready container", pod.Metadata.Name)
				}
				if cs.RestartCount > 0 {
					return fmt.Errorf("pod %s has %d restarts (crash-looping)", pod.Metadata.Name, cs.RestartCount)
				}
			}
			running++
		}
		if running == 0 {
			return fmt.Errorf("no running controller-manager pods found")
		}
		return nil
	}, 5*time.Minute, 5*time.Second).Should(Succeed())
})

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting osac-operator suite\n")
	RunSpecs(t, "integration suite")
}
