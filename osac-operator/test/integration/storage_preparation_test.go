// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

var _ = Describe("Storage preparation deployment", func() {
	It("provides a hub namespace readable by the installed operator", func() {
		preparationNamespace := operatorDeploymentEnv("OSAC_STORAGE_PREPARATION_NAMESPACE")
		Expect(preparationNamespace).To(Equal("osac-storage"))
		output, err := runKubectl("get", "deployments", "-n", operatorNamespace,
			"-l", "app.kubernetes.io/name=operator", "-o",
			`jsonpath={.items[0].spec.template.spec.containers[0].env`+
				`[?(@.name=="OSAC_STORAGE_CONFIG_NAMESPACE")].valueFrom.fieldRef.fieldPath}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(output))).To(Equal("metadata.namespace"))
		output, err = runKubectl("get", "namespace", preparationNamespace, "-o", "jsonpath={.status.phase}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(output))).To(Equal("Active"))

		output, err = runKubectl("get", "pods", "-n", operatorNamespace,
			"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator",
			"-o", "jsonpath={.items[0].spec.serviceAccountName}")
		Expect(err).NotTo(HaveOccurred())
		serviceAccount := strings.TrimSpace(string(output))
		Expect(serviceAccount).NotTo(BeEmpty())
		identity := fmt.Sprintf("system:serviceaccount:%s:%s", operatorNamespace, serviceAccount)
		for _, verb := range []string{"get", "list"} {
			output, err := runKubectl("auth", "can-i", verb, "secrets", "-n", preparationNamespace, "--as="+identity)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(output))).To(Equal("yes"), "%s Secrets in %s", verb, preparationNamespace)
		}
	})
})
