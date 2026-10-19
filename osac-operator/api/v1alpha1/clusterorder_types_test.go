package v1alpha1_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
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

	It("preserves the config hash of an order with an empty node image", func() {
		spec := v1alpha1.ClusterOrderSpec{NodeRequests: []v1alpha1.NodeRequest{{
			NodeSet: "workers", NumberOfNodes: 3,
		}}}
		encoded, err := json.Marshal(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(Equal(`{"nodeRequests":[{"nodeSet":"workers","numberOfNodes":3}]}`))

		hash, err := provisioning.ComputeDesiredConfigVersion(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("398d688e45d7741c"))

		spec.NodeRequests[0].ReleaseImage = "registry.example/ocp:4.21.0"
		withImage, err := provisioning.ComputeDesiredConfigVersion(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(withImage).NotTo(Equal(hash))
	})
})
