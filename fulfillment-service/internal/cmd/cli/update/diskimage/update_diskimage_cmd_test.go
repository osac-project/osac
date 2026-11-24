/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package diskimage

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Update diskimage flag registration", func() {
	It("should create command without error", func() {
		cmd := Cmd()
		Expect(cmd).NotTo(BeNil())
		Expect(cmd.Use).To(Equal("diskimage ID|NAME"))
	})

	It("should register --pull-secret and --clear-pull-secret", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("pull-secret")).NotTo(BeNil())
		Expect(cmd.Flags().Lookup("clear-pull-secret")).NotTo(BeNil())
	})

	It("should require one of the pull-secret flags", func() {
		cmd := Cmd()
		cmd.SetArgs([]string{"fedora-41"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("one of the flags"))
	})
})
