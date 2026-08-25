/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package storagetier

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Create storagetier command", func() {
	It("should create command without error", func() {
		cmd := Cmd()
		Expect(cmd).NotTo(BeNil())
		Expect(cmd.Use).To(Equal("storagetier"))
	})

	It("should register --name flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("name")).NotTo(BeNil())
	})

	It("should register --backend-id flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("backend-id")).NotTo(BeNil())
	})

	It("should register --protocol flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("protocol")).NotTo(BeNil())
	})

	It("should register --max-read-bandwidth-mbs flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("max-read-bandwidth-mbs")).NotTo(BeNil())
	})

	It("should register --max-write-bandwidth-mbs flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("max-write-bandwidth-mbs")).NotTo(BeNil())
	})

	It("should register --encryption-enabled flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("encryption-enabled")).NotTo(BeNil())
	})

	It("should not register a --quota-gib flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("quota-gib")).To(BeNil())
	})
})

var _ = Describe("parseProtocol", func() {
	It("parses NFS", func() {
		result, err := parseProtocol("NFS")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS))
	})

	It("parses BLOCK", func() {
		result, err := parseProtocol("BLOCK")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK))
	})

	It("parses case-insensitively", func() {
		result, err := parseProtocol("nfs")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS))
	})

	It("rejects an empty value", func() {
		_, err := parseProtocol("")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("protocol is required"))
	})

	It("rejects an invalid protocol", func() {
		_, err := parseProtocol("iscsi")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid protocol"))
	})
})
