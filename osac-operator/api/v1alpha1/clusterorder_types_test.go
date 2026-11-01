package v1alpha1_test

import (
	"os"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

var _ = Describe("ClusterOrder upgrade contract", func() {
	It("validates control-plane and node-set release images identically", func() {
		data, err := os.ReadFile("../../config/crd/bases/osac.openshift.io_clusterorders.yaml")
		Expect(err).NotTo(HaveOccurred())
		var crd apiextensionsv1.CustomResourceDefinition
		Expect(yaml.Unmarshal(data, &crd)).To(Succeed())
		Expect(crd.Spec.Versions).NotTo(BeEmpty())

		spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
		controlPlane := spec.Properties["releaseImage"]
		nodeSet := spec.Properties["nodeRequests"].Items.Schema.Properties["releaseImage"]
		Expect(nodeSet.Pattern).To(Equal(controlPlane.Pattern))
		Expect(nodeSet.MaxLength).To(Equal(controlPlane.MaxLength))
		Expect(controlPlane.MaxLength).NotTo(BeNil())
		Expect(*controlPlane.MaxLength).To(Equal(int64(512)))
		pattern := regexp.MustCompile(controlPlane.Pattern)

		for _, image := range []string{
			// Tagged pull specs.
			"quay.io/openshift-release-dev/ocp-release:4.22.0-multi",
			"registry--mirror.example/ocp/release:stable",
			"localhost:5000/ocp/release:_candidate-4.22",
			"[2001:db8::1]:5000/ocp/release:stable",
			// SHA digest pull specs, with or without a tag.
			"registry.example:5000/ocp/release@sha256:" + strings.Repeat("a", 64),
			"quay.io/openshift-release-dev/ocp-release@sha256:" + strings.Repeat("A", 64),
			"quay.io/ocp/release:stable@sha256:" + strings.Repeat("a", 64),
		} {
			Expect(pattern.MatchString(image)).To(BeTrue(), image)
		}
		for _, image := range []string{
			"quay.io/ocp/release:bad tag",
			"quay.io/ocp/release:bad\ttag",
			"quay.io/ocp/release:tag;rm",
			"quay.io/ocp/release:",
			"quay.io/ocp/release",
			"quay.io/ocp/release@sha256:abc",
		} {
			Expect(pattern.MatchString(image)).To(BeFalse(), image)
		}
	})

	// Config-hash regression for ClusterOrderSpec lives in
	// osac-operator/pkg/provisioning/provision_lifecycle_test.go
	// (the provisioning package that owns ComputeDesiredConfigVersion).
})
