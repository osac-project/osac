/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalip_test

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/externalip"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestExternalIPHelpers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "External IP CLI helpers")
}

var _ = Describe("AttachmentName", func() {
	eip := func(id, name string) *publicv1.ExternalIP {
		return publicv1.ExternalIP_builder{
			Id:       id,
			Metadata: publicv1.Metadata_builder{Name: name}.Build(),
		}.Build()
	}

	It("uses the metadata name plus suffix", func() {
		Expect(externalip.AttachmentName(eip("eip-1", "my-ip"))).To(Equal("my-ip-attachment"))
	})

	It("falls back to the ID when the IP has no name", func() {
		Expect(externalip.AttachmentName(eip("1234", ""))).To(Equal("eip-1234-attachment"))
	})

	It("falls back to the ID when the name plus suffix exceeds 63 characters", func() {
		longName := strings.Repeat("a", 60)
		Expect(externalip.AttachmentName(eip("1234", longName))).To(Equal("eip-1234-attachment"))
	})

	It("keeps short names intact at the boundary", func() {
		name := strings.Repeat("a", 52)
		Expect(externalip.AttachmentName(eip("1234", name))).To(Equal(name + "-attachment"))
	})

	It("truncates when even the ID-based form exceeds 63 characters", func() {
		longID := strings.Repeat("x", 100)
		Expect(externalip.AttachmentName(eip(longID, ""))).To(HaveLen(63))
		Expect(externalip.AttachmentName(eip(longID, ""))).To(HaveSuffix("-attachment"))
	})
})
