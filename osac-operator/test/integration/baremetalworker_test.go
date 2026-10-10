// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

// Validate the installed operator's worker permissions in the existing Kind
// suite. This uses Kubernetes authorization, not rendered chart text or a
// host-built worker with privileged test credentials. It does not provision
// hosts or prove the fulfillment/provider contract.
var _ = Describe("Bare-metal worker deployment", func() {
	It("grants the deployed service account the worker lifecycle permissions", func() {
		output, err := runKubectl("get", "pods", "-n", operatorNamespace,
			"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator",
			"-o", "jsonpath={.items[0].spec.serviceAccountName}")
		Expect(err).NotTo(HaveOccurred())
		serviceAccount := strings.TrimSpace(string(output))
		Expect(serviceAccount).NotTo(BeEmpty())
		identity := fmt.Sprintf("system:serviceaccount:%s:%s", operatorNamespace, serviceAccount)
		namespace := operatorDeploymentEnv("OSAC_CLUSTER_ORDER_NAMESPACE")
		if namespace == "" {
			namespace = operatorNamespace
		}

		permissions := []struct {
			resource    string
			subresource string
			verbs       []string
		}{
			{resource: "clusterorders.osac.openshift.io", verbs: []string{"get", "list", "watch", "update"}},
			{resource: "clusterorders.osac.openshift.io", subresource: "status", verbs: []string{"get", "update", "patch"}},
			{
				resource: "infraenvs.agent-install.openshift.io",
				verbs:    []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{resource: "agents.agent-install.openshift.io", verbs: []string{"get", "list", "watch", "patch", "delete"}},
			{resource: "nodepools.hypershift.openshift.io", verbs: []string{"get", "list", "watch", "patch", "update"}},
			{resource: "secrets", verbs: []string{"get", "create"}},
		}
		for _, permission := range permissions {
			for _, verb := range permission.verbs {
				args := []string{"auth", "can-i", verb, permission.resource, "-n", namespace, "--as=" + identity}
				if permission.subresource != "" {
					args = append(args, "--subresource="+permission.subresource)
				}
				output, err := runKubectl(args...)
				Expect(err).NotTo(HaveOccurred(), "%s %s/%s", verb, permission.resource, permission.subresource)
				Expect(strings.TrimSpace(string(output))).To(Equal("yes"),
					"%s %s/%s", verb, permission.resource, permission.subresource)
			}
		}
	})
})
