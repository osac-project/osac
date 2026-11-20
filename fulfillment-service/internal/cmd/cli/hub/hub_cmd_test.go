/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package hub

import (
	"bytes"
	"testing"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestHub(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hub CLI suite")
}

var _ = Describe("Hub CLI", func() {
	It("registers list and describe commands", func() {
		cmd := Cmd()
		Expect(cmd.Use).To(Equal("hub"))
		Expect(cmd.Commands()).To(HaveLen(2))
	})

	It("renders Hub identity without credentials", func() {
		testKubeconfig := []byte("secret")
		hub := privatev1.Hub_builder{
			Id:       "hub-1",
			Metadata: privatev1.Metadata_builder{Name: "ai-grid-hub"}.Build(),
			Spec:     privatev1.HubSpec_builder{Namespace: "osac-orders", Kubeconfig: testKubeconfig}.Build(),
		}.Build()
		output := &bytes.Buffer{}
		Expect(renderHub(output, hub)).To(Succeed())
		Expect(output.String()).To(ContainSubstring("hub-1"))
		Expect(output.String()).To(ContainSubstring("ai-grid-hub"))
		Expect(output.String()).To(ContainSubstring("osac-orders"))
		Expect(output.String()).NotTo(ContainSubstring("secret"))
	})
})
