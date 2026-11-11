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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	rbacv1 "k8s.io/api/rbac/v1"
)

const clusterOrderAdminWebhookName = "osac-operator-clusterorder-rbac-webhook"

func expectCanI(verb, resource, namespace, manager, expected string) {
	args := []string{"auth", "can-i", verb, resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "--as="+manager)
	output, err := kubectl("", args...)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	decision := strings.TrimSpace(lines[len(lines)-1])
	// kubectl auth can-i prints "no" and exits 1 for a denied request.
	if expected == "no" && decision == "no" {
		err = nil
	}
	Expect(err).NotTo(HaveOccurred(), output)
	Expect(decision).To(Equal(expected))
}

var _ = Describe(
	"ClusterOrder admin RoleBinding admission",
	Ordered,
	Serial,
	Label("clusterorder", "admission"),
	func() {
		var orderName, generatedNamespace, otherNamespace, manager string

		BeforeAll(func() {
			suffix := uniqueSuffix()
			orderName = "admin-admission-" + suffix
			generatedNamespace = operatorNamespace + "-" + orderName
			otherNamespace = "admin-admission-outside-" + suffix
			manager = operatorIdentity()
			for _, verb := range []string{"create", "delete"} {
				By(fmt.Sprintf("confirming the manager cannot %s ClusterOrders", verb))
				expectCanI(verb, "clusterorders.osac.openshift.io", operatorNamespace, manager, "no")
			}
			By("confirming the manager can bind only the built-in admin ClusterRole")
			expectCanI("bind", "clusterroles.rbac.authorization.k8s.io/admin", "", manager, "yes")
			expectCanI("bind", "clusterroles.rbac.authorization.k8s.io/edit", "", manager, "no")

			By("waiting for the fail-closed webhook and its injected CA bundle")
			Eventually(func() error {
				output, err := kubectl("", "get", "validatingwebhookconfiguration", clusterOrderAdminWebhookName, "-o", "json")
				if err != nil {
					return err
				}
				var configuration struct {
					Webhooks []struct {
						ClientConfig struct {
							CABundle []byte `json:"caBundle"`
						} `json:"clientConfig"`
					} `json:"webhooks"`
				}
				if err := json.Unmarshal([]byte(output), &configuration); err != nil {
					return err
				}
				if len(configuration.Webhooks) != 1 || len(configuration.Webhooks[0].ClientConfig.CABundle) == 0 {
					return fmt.Errorf("webhook CA bundle is not injected")
				}
				return nil
			}, 3*time.Minute, time.Second).Should(Succeed())

			By("creating a manually managed ClusterOrder to exercise its generated namespace and binding")
			manifest := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: ClusterOrder
metadata:
  name: %s
  namespace: %s
  annotations:
    osac.openshift.io/management-state: manual
    osac.openshift.io/tenant: admission-test
    osac.openshift.io/owner-reference: Cluster/%s
spec:
  templateID: noop
`, orderName, operatorNamespace, orderName)
			output, err := kubectl(manifest, "create", "-f", "-")
			Expect(err).NotTo(HaveOccurred(), output)
			DeferCleanup(func() {
				removeFinalizers("clusterorder", orderName, operatorNamespace)
				_, _ = kubectl("", "delete", "clusterorder", orderName, "-n", operatorNamespace,
					"--ignore-not-found", "--wait=false")
				_, _ = kubectl("", "delete", "namespace", generatedNamespace, "--ignore-not-found", "--wait=false")
				_, _ = kubectl("", "delete", "namespace", otherNamespace, "--ignore-not-found", "--wait=false")
			})

			By("waiting for the operator to create the expected admin binding")
			Eventually(func(g Gomega) {
				output, err := kubectl("", "get", "rolebinding", "osac", "-n", generatedNamespace, "-o", "json")
				g.Expect(err).NotTo(HaveOccurred(), output)
				var binding rbacv1.RoleBinding
				g.Expect(json.Unmarshal([]byte(output), &binding)).To(Succeed())
				g.Expect(binding.RoleRef).To(Equal(rbacv1.RoleRef{
					APIGroup: rbacv1.GroupName,
					Kind:     "ClusterRole",
					Name:     "admin",
				}))
				g.Expect(binding.Subjects).To(Equal([]rbacv1.Subject{{
					Kind:      "ServiceAccount",
					Name:      "osac",
					Namespace: generatedNamespace,
				}}))
			}, readyTimeout, time.Second).Should(Succeed())
		})

		It("denies manager admin bindings outside the generated namespace and subject", func() {
			By("creating an unrelated namespace")
			output, err := kubectl("", "create", "namespace", otherNamespace)
			Expect(err).NotTo(HaveOccurred(), output)

			By("confirming RBAC permits the manager to create RoleBindings so the webhook is the denial boundary")
			expectCanI("create", "rolebindings.rbac.authorization.k8s.io", otherNamespace, manager, "yes")

			By("denying an admin binding in a namespace without a ClusterOrder")
			outsideBinding := fmt.Sprintf(`apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: osac
  namespace: %s
  labels:
    app.kubernetes.io/name: osac-operator
    osac.openshift.io/clusterorder: %s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: admin
subjects:
- kind: ServiceAccount
  name: osac
  namespace: %s
`, otherNamespace, orderName, otherNamespace)
			output, err = kubectl(outsideBinding, "create", "-f", "-", "--as="+manager)
			Expect(err).To(HaveOccurred(), "operator manager must not bind admin in an unrelated namespace")
			Expect(output).To(ContainSubstring("OSAC may bind admin only to the service account for an existing ClusterOrder"))

			By("denying a different ServiceAccount on the generated admin binding")
			patch := fmt.Sprintf(
				`{"subjects":[{"kind":"ServiceAccount","name":"attacker","namespace":"%s"}]}`,
				generatedNamespace,
			)
			output, err = kubectl("", "patch", "rolebinding", "osac", "-n", generatedNamespace,
				"--type=merge", "-p", patch, "--as="+manager)
			Expect(err).To(HaveOccurred(), "operator manager must not change the admin binding subject")
			Expect(output).To(ContainSubstring("OSAC may bind admin only to the service account for an existing ClusterOrder"))

			output, err = kubectl("", "get", "rolebinding", "osac", "-n", generatedNamespace, "-o", "json")
			Expect(err).NotTo(HaveOccurred(), output)
			var binding rbacv1.RoleBinding
			Expect(json.Unmarshal([]byte(output), &binding)).To(Succeed())
			Expect(binding.Subjects).To(Equal([]rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      "osac",
				Namespace: generatedNamespace,
			}}), "a denied update must leave the controller binding intact")
		})
	})
