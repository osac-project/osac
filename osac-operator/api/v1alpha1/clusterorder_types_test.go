package v1alpha1_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"sigs.k8s.io/yaml"
)

var _ = Describe("ClusterOrder upgrade contract", func() {
	It("validates control-plane and node-set release images identically", func() {
		data, err := os.ReadFile("../../config/crd/bases/osac.openshift.io_clusterorders.yaml")
		Expect(err).NotTo(HaveOccurred())
		var crd struct {
			Spec struct {
				Versions []struct {
					Schema struct {
						OpenAPIV3Schema struct {
							Properties map[string]json.RawMessage `json:"properties"`
						} `json:"openAPIV3Schema"`
					} `json:"schema"`
				} `json:"versions"`
			} `json:"spec"`
		}
		Expect(yaml.Unmarshal(data, &crd)).To(Succeed())
		Expect(crd.Spec.Versions).NotTo(BeEmpty())

		var spec struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		Expect(yaml.Unmarshal(crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"], &spec)).To(Succeed())

		var controlPlane struct {
			Pattern   string `json:"pattern"`
			MaxLength *int64 `json:"maxLength"`
		}
		Expect(yaml.Unmarshal(spec.Properties["releaseImage"], &controlPlane)).To(Succeed())
		var nodeRequests struct {
			Items struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"items"`
		}
		Expect(yaml.Unmarshal(spec.Properties["nodeRequests"], &nodeRequests)).To(Succeed())
		var nodeSet struct {
			Pattern   string `json:"pattern"`
			MaxLength *int64 `json:"maxLength"`
		}
		Expect(yaml.Unmarshal(nodeRequests.Items.Properties["releaseImage"], &nodeSet)).To(Succeed())
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

	It("marshals a node request with an empty release image", func() {
		spec := v1alpha1.ClusterOrderSpec{NodeRequests: []v1alpha1.NodeRequest{{
			NodeSet: "workers", NumberOfNodes: 3,
		}}}
		encoded, err := json.Marshal(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(Equal(`{"nodeRequests":[{"nodeSet":"workers","numberOfNodes":3}]}`))
	})

	It("serializes the SSH key reference as an ID-only secret", func() {
		spec := v1alpha1.ClusterOrderSpec{
			SSHKey: &v1alpha1.FulfillmentSecretReference{ID: "key-id"},
		}
		encoded, err := json.Marshal(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(Equal(`{"sshKey":{"id":"key-id"}}`))

		empty, err := json.Marshal(v1alpha1.ClusterOrderSpec{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(empty)).To(Equal(`{}`))
	})
})
