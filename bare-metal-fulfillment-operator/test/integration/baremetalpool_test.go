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

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/utils"
)

// Nothing in Kind runs a real Metal3 BareMetalOperator/Ironic, so these tests play
// that role directly: a fake BareMetalHost is created "available" up front, and its
// status.poweredOn is patched by hand at the point a real BMO would have finished an
// IPMI/Redfish power action. Everything else -- FindFreeHost/AssignHost/UnassignHost,
// finalizer handling, phase transitions -- is the real reconciler running against a
// real deployed manager, exercised the same way a user would (kubectl against CRs),
// not via in-process Reconcile() calls.

func kubectlJSONPath(kind, name, ns, path string) (string, error) {
	cmd := exec.Command("kubectl", "get", kind, name, "-n", ns, "-o", fmt.Sprintf("jsonpath=%s", path))
	return utils.Run(cmd)
}

func patchBareMetalHostStatus(name, ns, statusJSON string) {
	cmd := exec.Command("kubectl", "patch", "baremetalhost", name,
		"-n", ns, "--type=merge", "--subresource=status", "-p", statusJSON)
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())
}

func createBareMetalHostYAML(name, ns, hostType string) *strings.Reader {
	yaml := fmt.Sprintf(`apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/host-type: %s
    osac.openshift.io/managed-by: baremetal
spec:
  online: false
`, name, ns, hostType)
	return strings.NewReader(yaml)
}

// createAvailableBareMetalHost creates a fake BareMetalHost already in the "available,
// operational" state -- the state a real Metal3 BareMetalOperator would reach only
// after successfully inspecting real hardware.
func createAvailableBareMetalHost(name, ns, hostType string) {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = createBareMetalHostYAML(name, ns, hostType)
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	patchBareMetalHostStatus(name, ns,
		`{"status":{"operationalStatus":"OK","poweredOn":false,"errorCount":0,`+
			`"errorMessage":"","provisioning":{"ID":"","state":"available"},`+
			`"hardware":{"nics":[{"name":"eth0","model":"virtio",`+
			`"mac":"52:54:00:12:34:56","ip":"192.168.111.20","speedGbps":10,"pxe":true}]}}}`)
}

func deleteBareMetalHost(name, ns string) {
	cmd := exec.Command("kubectl", "delete", "baremetalhost", name, "-n", ns, "--ignore-not-found", "--wait=false")
	_, _ = utils.Run(cmd)
}

func createBareMetalInstanceYAML(name, ns, hostType, runStrategy string) *strings.Reader {
	yaml := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: BareMetalInstance
metadata:
  name: %s
  namespace: %s
spec:
  hostType: %s
  externalHostID: ""
  templateID: noop
  runStrategy: %s
`, name, ns, hostType, runStrategy)
	return strings.NewReader(yaml)
}

func createBareMetalPoolYAML(name, ns, hostType string, replicas int) *strings.Reader {
	yaml := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: BareMetalPool
metadata:
  name: %s
  namespace: %s
spec:
  hostSets:
    - hostType: %s
      replicas: %d
`, name, ns, hostType, replicas)
	return strings.NewReader(yaml)
}

var _ = Describe("BareMetalPool provisioning", Ordered, func() {
	Context("direct BareMetalInstance allocation", func() {
		const (
			hostType = "test-direct-host"
			bmhName  = "test-direct-bmh"
			bmiName  = "test-direct-bmi"
		)

		BeforeAll(func() {
			createAvailableBareMetalHost(bmhName, namespace, hostType)
		})

		AfterAll(func() {
			cmd := exec.Command("kubectl", "delete", "baremetalinstance", bmiName,
				"-n", namespace, "--ignore-not-found", "--wait=false")
			_, _ = utils.Run(cmd)
			deleteBareMetalHost(bmhName, namespace)
		})

		It("should allocate the fake host and reach Progressing", func() {
			By("creating a BareMetalInstance requesting this host type")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = createBareMetalInstanceYAML(bmiName, namespace, hostType, "Always")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the instance to be assigned the fake host")
			Eventually(func() (string, error) {
				return kubectlJSONPath("baremetalinstance", bmiName, namespace, "{.spec.externalHostID}")
			}, 2*time.Minute, 2*time.Second).Should(Equal(namespace + "/" + bmhName))

			By("waiting for the instance to reach Progressing with hostClass set")
			Eventually(func() (string, error) {
				return kubectlJSONPath("baremetalinstance", bmiName, namespace, "{.status.phase}")
			}, time.Minute, 2*time.Second).Should(Equal("Progressing"))

			hostClass, err := kubectlJSONPath("baremetalinstance", bmiName, namespace, "{.spec.hostClass}")
			Expect(err).NotTo(HaveOccurred())
			Expect(hostClass).To(Equal("metal3"))

			By("verifying the fake BareMetalHost shows the instance as its consumer")
			consumerName, err := kubectlJSONPath("baremetalhost", bmhName, namespace, "{.spec.consumerRef.name}")
			Expect(err).NotTo(HaveOccurred())
			Expect(consumerName).NotTo(BeEmpty())
		})

		It("should power on the host once requested, and reach Ready", func() {
			By("waiting for the operator to request power-on")
			Eventually(func() (string, error) {
				return kubectlJSONPath("baremetalhost", bmhName, namespace, "{.spec.online}")
			}, time.Minute, 2*time.Second).Should(Equal("true"))

			By("simulating BareMetalOperator/Ironic completing the power-on")
			patchBareMetalHostStatus(bmhName, namespace, `{"status":{"poweredOn":true}}`)

			By("waiting for the instance to reach Ready")
			Eventually(func() (string, error) {
				return kubectlJSONPath("baremetalinstance", bmiName, namespace, "{.status.phase}")
			}, time.Minute, 2*time.Second).Should(Equal("Ready"))
		})

		It("should release the host on deletion", func() {
			cmd := exec.Command("kubectl", "delete", "baremetalinstance", bmiName, "-n", namespace, "--wait=false")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the instance to be fully deleted")
			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "baremetalinstance", bmiName, "-n", namespace)
				_, err := utils.Run(cmd)
				if err == nil {
					return fmt.Errorf("BareMetalInstance still exists")
				}
				return nil
			}, time.Minute, 2*time.Second).Should(Succeed())

			By("verifying the fake host was released")
			Eventually(func() (string, error) {
				return kubectlJSONPath("baremetalhost", bmhName, namespace, "{.spec.consumerRef}")
			}, time.Minute, 2*time.Second).Should(BeEmpty())
		})
	})

	Context("BareMetalPool spawning and readiness aggregation", func() {
		const (
			hostType = "test-pool-host"
			bmhName  = "test-pool-bmh"
			poolName = "test-pool"
		)

		var instanceName string

		BeforeAll(func() {
			createAvailableBareMetalHost(bmhName, namespace, hostType)
		})

		AfterAll(func() {
			cmd := exec.Command("kubectl", "delete", "baremetalpool", poolName,
				"-n", namespace, "--ignore-not-found", "--wait=false")
			_, _ = utils.Run(cmd)

			// Delete by label rather than the It block's captured instanceName -- this
			// still cleans up if the It block failed before that variable was set.
			cmd = exec.Command("kubectl", "delete", "baremetalinstance", "-n", namespace,
				"-l", fmt.Sprintf("osac.openshift.io/host-type=%s", hostType),
				"--ignore-not-found", "--wait=false")
			_, _ = utils.Run(cmd)

			By("waiting for the pool and any spawned instance to be fully deleted")
			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "baremetalinstance", "-n", namespace,
					"-l", fmt.Sprintf("osac.openshift.io/host-type=%s", hostType), "-o", "name")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if len(strings.TrimSpace(output)) != 0 {
					return fmt.Errorf("baremetalinstance(s) still present: %s", output)
				}
				cmd = exec.Command("kubectl", "get", "baremetalpool", poolName, "-n", namespace)
				if _, err := utils.Run(cmd); err == nil {
					return fmt.Errorf("baremetalpool %s still exists", poolName)
				}
				return nil
			}, time.Minute, 2*time.Second).Should(Succeed())

			deleteBareMetalHost(bmhName, namespace)
		})

		It("should spawn a BareMetalInstance and reach Ready", func() {
			By("creating a BareMetalPool for this host type")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = createBareMetalPoolYAML(poolName, namespace, hostType, 1)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the pool to spawn exactly one BareMetalInstance")
			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "baremetalinstance", "-n", namespace,
					"-l", fmt.Sprintf("osac.openshift.io/host-type=%s", hostType),
					"-o", "jsonpath={.items[*].metadata.name}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				names := strings.Fields(output)
				if len(names) != 1 {
					return fmt.Errorf("expected exactly 1 spawned BareMetalInstance, found %d: %v", len(names), names)
				}
				instanceName = names[0]
				return nil
			}, time.Minute, 2*time.Second).Should(Succeed())

			// Pool-created instances get no RunStrategy (unless a Profile sets one),
			// so this one skips power management entirely and goes straight to Ready
			// once the host is assigned -- unlike the direct-instance test above.
			By("waiting for the spawned instance to reach Ready")
			Eventually(func() (string, error) {
				return kubectlJSONPath("baremetalinstance", instanceName, namespace, "{.status.phase}")
			}, 2*time.Minute, 2*time.Second).Should(Equal("Ready"))

			By("waiting for the pool itself to aggregate to Ready")
			Eventually(func() (string, error) {
				return kubectlJSONPath("baremetalpool", poolName, namespace, "{.status.phase}")
			}, time.Minute, 2*time.Second).Should(Equal("Ready"))
		})
	})
})
