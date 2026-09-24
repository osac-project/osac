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
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/test/utils"
)

const topoLVMLogicalVolumesCRD = "logicalvolumes.topolvm.io"

var _ = Describe("Volume controller startup", func() {
	It("runs without the TopoLVM CRD when LVMS is not configured", func() {
		crdOutput, err := utils.Run(exec.Command(
			"kubectl", "get", "crd", topoLVMLogicalVolumesCRD, "--ignore-not-found", "-o", "name",
		))
		Expect(err).NotTo(HaveOccurred())
		if strings.TrimSpace(string(crdOutput)) != "" {
			Skip("TopoLVM CRD is installed; this case covers the LVMS-disabled configuration")
		}

		vendorControllers := operatorDeploymentEnv("OSAC_VENDOR_CONTROLLERS")
		if strings.Contains(vendorControllers, "lvms=") {
			Skip("LVMS is configured; this case covers the LVMS-disabled configuration")
		}
		Expect(operatorDeploymentEnv("OSAC_ENABLE_VOLUME_CONTROLLER")).To(Equal("true"))

		Eventually(func() error {
			output, err := utils.Run(exec.Command(
				"kubectl", "get", "pods",
				"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator",
				"-n", operatorNamespace,
				"-o", "jsonpath={.items[0].status.containerStatuses[0].ready}",
			))
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(output)) != "true" {
				return fmt.Errorf("operator container is not ready: %q", strings.TrimSpace(string(output)))
			}
			return nil
		}, 30*time.Second, time.Second).Should(Succeed())
	})
})

func operatorDeploymentEnv(name string) string {
	path := fmt.Sprintf(`{.items[0].spec.template.spec.containers[0].env[?(@.name=="%s")].value}`, name)
	output, err := utils.Run(exec.Command(
		"kubectl", "get", "deployments",
		"-l", "app.kubernetes.io/name=operator",
		"-n", operatorNamespace,
		"-o", "jsonpath="+path,
	))
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(string(output))
}
