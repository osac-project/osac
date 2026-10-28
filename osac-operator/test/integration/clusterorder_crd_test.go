// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/test/utils"
)

// Validate the deployed ClusterOrder CRD's sshKey schema through the
// Kubernetes API using server-side dry-run, so nothing is persisted and the
// operator is not triggered. This pins the generated CRD contract installed
// by the deployment: the registered SSH-key Secret reference (sshKey.id)
// replaced the removed inline sshPublicKey field.
var _ = Describe("ClusterOrder sshKey CRD validation", func() {
	applyManifest := func(manifest string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), kubectlCommandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "kubectl", "apply", "--dry-run=server",
			"-f", "-", "-o", "json", "-n", operatorNamespace)
		cmd.Stdin = strings.NewReader(manifest)
		return utils.Run(cmd)
	}

	orderManifest := func(spec string) string {
		return fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: ClusterOrder
metadata:
  name: sshkey-crd-validation
  namespace: %s
spec:
  templateID: osac.templates.ocp_small
%s
`, operatorNamespace, spec)
	}

	It("accepts a registered SSH-key Secret reference", func() {
		output, err := applyManifest(orderManifest("  sshKey:\n    id: 54290e11-6f19-4bdc-9d7e-0123456789ab"))
		Expect(err).NotTo(HaveOccurred(), "valid sshKey.id must pass server-side validation")
		Expect(strings.TrimSpace(string(output))).NotTo(BeEmpty())
	})

	DescribeTable("rejects malformed sshKey references",
		func(spec string, expected string) {
			_, err := applyManifest(orderManifest(spec))
			Expect(err).To(HaveOccurred(), expected)
			Expect(err.Error()).To(ContainSubstring("sshKey"))
		},
		Entry("missing id", "  sshKey: {}", "sshKey without id must be rejected"),
		Entry("empty id", "  sshKey:\n    id: \"\"", "sshKey with an empty id must be rejected"),
		Entry("over-length id", "  sshKey:\n    id: "+strings.Repeat("a", 65),
			"sshKey.id beyond the 64-character maximum must be rejected"),
	)

	It("prunes the removed inline sshPublicKey field instead of storing it", func() {
		// CRDs drop unknown fields rather than rejecting them; the assertion is
		// that a legacy manifest no longer carries key material into storage.
		output, err := applyManifest(orderManifest("  sshPublicKey: ssh-ed25519 AAAA inline-legacy-key"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(output)).NotTo(ContainSubstring("sshPublicKey"),
			"the removed inline field must be pruned by the API server")
		Expect(string(output)).NotTo(ContainSubstring("ssh-ed25519"),
			"inline key material must not be stored")
	})
})
