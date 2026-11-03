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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/test/utils"
)

var _ = Describe("ComputeInstance disk Volume IDs", func() {
	const computeInstanceName = "test-ci-disk-volume-ids"

	AfterEach(func() {
		removeFinalizers("computeinstance", computeInstanceName, operatorNamespace)
		cmd := exec.Command("kubectl", "delete", "computeinstance", computeInstanceName,
			"-n", operatorNamespace, "--wait=false", "--ignore-not-found")
		_, _ = utils.Run(cmd)
		removeFinalizers("computeinstance", computeInstanceName, operatorNamespace)
	})

	It("round-trips the optional disk key to Volume ID map through the installed CRD", func() {
		By("creating an unmanaged ComputeInstance with keyed disk IDs")
		manifest := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: ComputeInstance
metadata:
  name: %s
  namespace: %s
  annotations:
    osac.openshift.io/management-state: unmanaged
spec:
  templateID: test_template
  image:
    sourceType: registry
    sourceRef: quay.io/fedora/fedora-coreos:stable
  vcpus: 2
  memoryGiB: 4
  bootDisk:
    sizeGiB: 20
    storageTier: standard
  additionalDisks:
    - sizeGiB: 10
      storageTier: standard
    - sizeGiB: 20
      storageTier: standard
  diskVolumeIDs:
    disk-2: volume-data-2
    boot: volume-boot
    disk-1: volume-data-1
  runStrategy: Always
`, computeInstanceName, operatorNamespace)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("checking the Kubernetes API retained each key and ID")
		Eventually(func() error {
			cmd := exec.Command("kubectl", "get", "computeinstance", computeInstanceName,
				"-n", operatorNamespace, "-o", "json")
			output, err := utils.Run(cmd)
			if err != nil {
				return err
			}
			var resource struct {
				Spec struct {
					DiskVolumeIDs map[string]string `json:"diskVolumeIDs"`
				} `json:"spec"`
			}
			if err := json.Unmarshal(output, &resource); err != nil {
				return fmt.Errorf("decode ComputeInstance: %w", err)
			}
			expected := map[string]string{
				"boot":   "volume-boot",
				"disk-1": "volume-data-1",
				"disk-2": "volume-data-2",
			}
			if len(resource.Spec.DiskVolumeIDs) != len(expected) {
				return fmt.Errorf("unexpected diskVolumeIDs: %+v", resource.Spec.DiskVolumeIDs)
			}
			for key, id := range expected {
				if resource.Spec.DiskVolumeIDs[key] != id {
					return fmt.Errorf("diskVolumeIDs[%q] = %q, want %q", key, resource.Spec.DiskVolumeIDs[key], id)
				}
			}
			return nil
		}, 30*time.Second, time.Second).Should(Succeed())
	})
})
