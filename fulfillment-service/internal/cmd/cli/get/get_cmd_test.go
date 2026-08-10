/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package get

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/get/externalippool"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/get/hub"
)

var _ = Describe("Get command", func() {
	DescribeTable("Subcommand aliases",
		func(cmdFunc func() *cobra.Command, expectedAlias string) {
			cmd := cmdFunc()
			Expect(cmd.Aliases).To(ContainElement(expectedAlias))
		},
		Entry("hub", hub.Cmd, "hubs"),
		Entry("externalippool", externalippool.Cmd, "externalippools"),
	)

	Describe("Subcommands", func() {
		It("should have all expected subcommands", func() {
			cmd := Cmd()
			subcommands := cmd.Commands()

			var subcommandNames []string
			for _, subcmd := range subcommands {
				subcommandNames = append(subcommandNames, subcmd.Name())
			}

			Expect(subcommandNames).To(ContainElements("hub", "externalippool", "kubeconfig", "password", "token"))
		})
	})
})
