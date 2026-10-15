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

package provisioning_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("ParseExternalIPResult", func() {
	validResult := func() map[string]any {
		return map[string]any{
			"osac_result": map[string]any{
				"schemaVersion": "v1",
				"operation":     "external_ip.allocate",
				"resourceUID":   "external-ip-uid-1",
				"generation":    float64(3),
				"data":          map[string]any{},
			},
		}
	}

	It("converts the v1 AAP artifact into the stable ExternalIP outcome", func() {
		outcome, err := provisioning.ParseExternalIPResult(validResult())

		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Operation).To(Equal(provisioning.ExternalIPOperationAllocate))
		Expect(outcome.ResourceUID).To(Equal("external-ip-uid-1"))
		Expect(outcome.Generation).To(Equal(int64(3)))
	})

	DescribeTable("rejects incomplete or unsupported artifacts",
		func(artifacts map[string]any) {
			_, err := provisioning.ParseExternalIPResult(artifacts)
			Expect(err).To(HaveOccurred())
		},
		Entry("missing result", map[string]any{}),
		Entry("wrong envelope type", map[string]any{"osac_result": "success"}),
		Entry("unsupported version", func() map[string]any {
			result := validResult()
			result["osac_result"].(map[string]any)["schemaVersion"] = "v2"
			return result
		}()),
		Entry("unsupported operation", func() map[string]any {
			result := validResult()
			result["osac_result"].(map[string]any)["operation"] = "external_ip.attach"
			return result
		}()),
		Entry("missing UID", func() map[string]any {
			result := validResult()
			delete(result["osac_result"].(map[string]any), "resourceUID")
			return result
		}()),
		Entry("fractional generation", func() map[string]any {
			result := validResult()
			result["osac_result"].(map[string]any)["generation"] = float64(3.5)
			return result
		}()),
		Entry("negative generation", func() map[string]any {
			result := validResult()
			result["osac_result"].(map[string]any)["generation"] = int64(-1)
			return result
		}()),
		Entry("generation above int64 range", func() map[string]any {
			result := validResult()
			result["osac_result"].(map[string]any)["generation"] = float64(1 << 63)
			return result
		}()),
		Entry("missing data", func() map[string]any {
			result := validResult()
			delete(result["osac_result"].(map[string]any), "data")
			return result
		}()),
		Entry("unexpected payload", func() map[string]any {
			result := validResult()
			result["osac_result"].(map[string]any)["data"] = map[string]any{"address": "192.0.2.1"}
			return result
		}()),
	)
})
