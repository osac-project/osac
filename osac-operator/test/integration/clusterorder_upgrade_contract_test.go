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
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/test/utils"
)

var _ = Describe("ClusterOrder upgrade deployment contract", func() {
	It("admits only supported upgrade observation states through the installed CRD", func(ctx SpecContext) {
		name := fmt.Sprintf("upgrade-admission-%d", time.Now().UnixNano())
		DeferCleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cleanupCtx, "kubectl", "delete", "clusterorder", name, "-n", operatorNamespace,
				"--wait=false", "--ignore-not-found")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), string(output))
		})
		createCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(createCtx, "kubectl", "create", "-f", "-")
		cmd.Stdin = strings.NewReader(fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: ClusterOrder
metadata:
  name: %s
  namespace: %s
  annotations:
    osac.openshift.io/management-state: unmanaged
    osac.openshift.io/tenant: upgrade-admission-test
    osac.openshift.io/owner-reference: upgrade-admission-test
spec:
  templateID: noop
`, name, operatorNamespace))
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), string(output))

		patchStatus := func(ctx context.Context, status string) ([]byte, error) {
			cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cmdCtx, "kubectl", "patch", "clusterorder", name, "-n", operatorNamespace,
				"--subresource=status", "--type=merge", "-p", status)
			return utils.Run(cmd)
		}
		readState := func(ctx context.Context, path string) string {
			cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cmdCtx, "kubectl", "get", "clusterorder", name, "-n", operatorNamespace,
				"-o", "jsonpath="+path)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), string(output))
			return string(output)
		}

		_, err = patchStatus(ctx, `{"status":{"upgradeObservation":{"attemptID":"cp-id","state":"Progressing"}}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(readState(ctx, "{.status.upgradeObservation.state}")).To(Equal("Progressing"))

		_, err = patchStatus(ctx, `{"status":{"upgradeObservation":{"attemptID":"cp-pruned-id",`+
			`"state":"Progressing","baselineTransitionTime":"2026-11-18T00:00:00Z"}}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(readState(ctx, "{.status.upgradeObservation.attemptID}")).To(Equal("cp-pruned-id"))
		Expect(readState(ctx, "{.status.upgradeObservation.baselineTransitionTime}")).To(BeEmpty())

		output, err = patchStatus(ctx, `{"status":{"upgradeObservation":{"state":"Unknown"}}}`)
		Expect(err).To(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("status.upgradeObservation.state"))
		Expect(string(output)).To(ContainSubstring("Unsupported value: \"Unknown\""))
		Expect(readState(ctx, "{.status.upgradeObservation.state}")).To(Equal("Progressing"))

		_, err = patchStatus(ctx, `{"status":{"nodeRequests":[{"nodeSet":"workers","numberOfNodes":1,`+
			`"bareMetal":{"instanceType":"test"},"upgradeObservation":{"attemptID":"np-id","state":"Succeeded"}}]}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(readState(ctx, "{.status.nodeRequests[0].upgradeObservation.state}")).To(Equal("Succeeded"))

		_, err = patchStatus(ctx, `{"status":{"nodeRequests":[{"nodeSet":"workers","numberOfNodes":1,`+
			`"bareMetal":{"instanceType":"test"},"upgradeObservation":{"attemptID":"np-pruned-id",`+
			`"state":"Succeeded","baselineTransitionTime":"2026-11-18T00:00:00Z"}}]}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(readState(ctx, "{.status.nodeRequests[0].upgradeObservation.attemptID}")).To(Equal("np-pruned-id"))
		Expect(readState(ctx, "{.status.nodeRequests[0].upgradeObservation.baselineTransitionTime}")).To(BeEmpty())

		output, err = patchStatus(ctx, `{"status":{"nodeRequests":[{"nodeSet":"workers","numberOfNodes":1,`+
			`"bareMetal":{"instanceType":"test"},"upgradeObservation":{"attemptID":"np-id","state":"Unknown"}}]}}`)
		Expect(err).To(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("status.nodeRequests[0].upgradeObservation.state"))
		Expect(string(output)).To(ContainSubstring("Unsupported value: \"Unknown\""))
		Expect(readState(ctx, "{.status.nodeRequests[0].upgradeObservation.state}")).To(Equal("Succeeded"))

		_, err = patchStatus(ctx, `{"status":{"observedVersion":"4.22.1","nodeRequests":[{`+
			`"nodeSet":"workers","numberOfNodes":1,"bareMetal":{"instanceType":"test"},`+
			`"observedVersion":"4.22.0"}]}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(readState(ctx, "{.status.observedVersion}")).To(Equal("4.22.1"))
		Expect(readState(ctx, "{.status.nodeRequests[0].observedVersion}")).To(Equal("4.22.0"))
	})

	It("passes the configured upgrade deadlines to the installed manager", func() {
		cmd := exec.Command("kubectl", "get", "deployments", "-n", operatorNamespace,
			"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator", "-o", "json")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), string(output))

		var deployments struct {
			Items []struct {
				Spec struct {
					Template struct {
						Spec struct {
							Containers []struct {
								Name string `json:"name"`
								Env  []struct {
									Name  string `json:"name"`
									Value string `json:"value"`
								} `json:"env"`
							} `json:"containers"`
						} `json:"spec"`
					} `json:"template"`
				} `json:"spec"`
			} `json:"items"`
		}
		Expect(json.Unmarshal(output, &deployments)).To(Succeed())
		Expect(deployments.Items).To(HaveLen(1))
		var managerEnv map[string]string
		for _, container := range deployments.Items[0].Spec.Template.Spec.Containers {
			if container.Name != "manager" {
				continue
			}
			managerEnv = make(map[string]string, len(container.Env))
			for _, variable := range container.Env {
				managerEnv[variable.Name] = variable.Value
			}
		}
		Expect(managerEnv).To(HaveKeyWithValue("OSAC_CLUSTER_UPGRADE_PENDING_TIMEOUT", "10m"))
		Expect(managerEnv).To(HaveKeyWithValue("OSAC_CLUSTER_UPGRADE_CONTROL_PLANE_TIMEOUT", "40m"))
		Expect(managerEnv).To(HaveKeyWithValue("OSAC_CLUSTER_UPGRADE_NODE_POOL_PER_NODE_TIMEOUT", "20m"))
	})
})
