/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cli

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("CLI root color flag", func() {
	It("enables styled help on a redirected writer", func() {
		GinkgoT().Setenv("NO_COLOR", "")
		cmd, err := Root()
		Expect(err).NotTo(HaveOccurred())
		Expect(cmd.PersistentFlags().Lookup("color")).NotTo(BeNil())
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs([]string{"--color", "describe", "computeinstancecatalogitem", "--help"})
		Expect(cmd.Execute()).To(Succeed())
		Expect(output.String()).To(ContainSubstring("\x1b["))
	})
})

var _ = Describe("CLI root --project flag", func() {
	It("registers the --project persistent flag", func() {
		cmd, err := Root()
		Expect(err).NotTo(HaveOccurred())
		flag := cmd.PersistentFlags().Lookup("project")
		Expect(flag).NotTo(BeNil())
		Expect(flag.DefValue).To(Equal(""))
	})

	It("includes the project subcommand", func() {
		cmd, err := Root()
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		Expect(names).To(ContainElement("project"))
	})
})
