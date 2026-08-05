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
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac-operator/test/utils"
)

const (
	operatorImage     = "localhost/osac-operator:latest"
	operatorNamespace = "osac-operator-system"
)

var _ = BeforeSuite(func() {
	By("installing cert-manager")
	Expect(utils.InstallCertManager()).To(Succeed())

	By("installing CRDs")
	cmd := exec.Command("make", "install")
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	By("installing prometheus operator")
	Expect(utils.InstallPrometheusOperator()).To(Succeed())

	By("building the operator image")
	cmd = exec.Command("make", "image-build", fmt.Sprintf("IMG=%s", operatorImage))
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	By("loading the operator image into the kind cluster")
	err = utils.LoadImageToKindClusterWithName(operatorImage)
	Expect(err).NotTo(HaveOccurred())

	By("creating manager namespace")
	cmd = exec.Command("kubectl", "create", "ns", operatorNamespace)
	_, err = utils.Run(cmd)
	if err != nil && !strings.Contains(err.Error(), "AlreadyExists") {
		Fail(fmt.Sprintf("failed to create namespace %s: %v", operatorNamespace, err))
	}

	By("deploying the controller-manager")
	cmd = exec.Command("kubectl", "apply", "-k", "config/testing/default")
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	By("waiting for controller-manager to be ready")
	verifyControllerUp := func() error {
		cmd := exec.Command("kubectl", "get",
			"pods", "-l", "control-plane=controller-manager",
			"-o", "go-template={{ range .items }}"+
				"{{ if not .metadata.deletionTimestamp }}"+
				"{{ .metadata.name }}"+
				"{{ \"\\n\" }}{{ end }}{{ end }}",
			"-n", operatorNamespace,
		)
		podOutput, err := utils.Run(cmd)
		if err != nil {
			return err
		}
		podNames := utils.GetNonEmptyLines(string(podOutput))
		if len(podNames) != 1 {
			return fmt.Errorf("expect 1 controller pod running, but got %d", len(podNames))
		}

		cmd = exec.Command("kubectl", "get",
			"pods", podNames[0], "-o", "jsonpath={.status.phase}",
			"-n", operatorNamespace,
		)
		status, err := utils.Run(cmd)
		if err != nil {
			return err
		}
		if string(status) != "Running" {
			return fmt.Errorf("controller pod in %s status", status)
		}
		return nil
	}
	Eventually(verifyControllerUp, 2*time.Minute, time.Second).Should(Succeed())
})

var _ = AfterSuite(func() {
	By("undeploying the controller-manager")
	cmd := exec.Command("kubectl", "delete", "-k", "config/testing/default", "--ignore-not-found")
	_, _ = utils.Run(cmd)

	By("removing manager namespace")
	cmd = exec.Command("kubectl", "delete", "ns", operatorNamespace)
	_, _ = utils.Run(cmd)

	By("uninstalling the Prometheus manager bundle")
	utils.UninstallPrometheusOperator()

	By("uninstalling cert-manager")
	utils.UninstallCertManager()
})

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting osac-operator suite\n")
	RunSpecs(t, "integration suite")
}
