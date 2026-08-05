/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package describe

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/cluster"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/clusterversion"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/computeinstance"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/networkclass"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/securitygroup"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/subnet"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/describe/virtualnetwork"
)

var _ = Describe("Describe command", func() {
	DescribeTable("Subcommand aliases",
		func(cmdFunc func() *cobra.Command, expectedAlias string) {
			cmd := cmdFunc()
			Expect(cmd.Aliases).To(ContainElement(expectedAlias))
		},
		Entry("cluster", cluster.Cmd, "clusters"),
		Entry("clusterversion", clusterversion.Cmd, "clusterversions"),
		Entry("computeinstance", computeinstance.Cmd, "computeinstances"),
		Entry("networkclass", networkclass.Cmd, "networkclasses"),
		Entry("virtualnetwork", virtualnetwork.Cmd, "virtualnetworks"),
		Entry("subnet", subnet.Cmd, "subnets"),
		Entry("securitygroup", securitygroup.Cmd, "securitygroups"),
	)

	Describe("Subcommands", func() {
		It("should have all expected subcommands", func() {
			cmd := Cmd()
			subcommands := cmd.Commands()

			var subcommandNames []string
			for _, subcmd := range subcommands {
				subcommandNames = append(subcommandNames, subcmd.Name())
			}

			Expect(subcommandNames).To(ContainElements("cluster", "clusterversion", "computeinstance", "networkclass", "virtualnetwork", "subnet", "securitygroup"))
		})
	})

	Describe("CEL filter construction", func() {
		It("should produce valid CEL for a plain name", func() {
			ref := "example-resource"
			filter := fmt.Sprintf(`this.id == %[1]q || this.metadata.name == %[1]q`, ref)
			Expect(filter).To(Equal(`this.id == "example-resource" || this.metadata.name == "example-resource"`))
		})

		It("should escape single quotes in names", func() {
			ref := "example'resource"
			filter := fmt.Sprintf(`this.id == %[1]q || this.metadata.name == %[1]q`, ref)
			Expect(filter).To(ContainSubstring(`"example'resource"`))
			Expect(filter).NotTo(ContainSubstring(`'example'`))
		})

		It("should escape backticks in names", func() {
			ref := "example`resource"
			filter := fmt.Sprintf(`this.id == %[1]q || this.metadata.name == %[1]q`, ref)
			Expect(filter).To(ContainSubstring(`"example` + "`" + `resource"`))
		})

		It("should escape backslashes in names", func() {
			ref := `example\resource`
			filter := fmt.Sprintf(`this.id == %[1]q || this.metadata.name == %[1]q`, ref)
			Expect(filter).To(ContainSubstring(`"example\\resource"`))
		})

		It("should escape double quotes properly", func() {
			ref := `example"resource`
			filter := fmt.Sprintf(`this.id == %[1]q || this.metadata.name == %[1]q`, ref)
			Expect(filter).To(ContainSubstring(`"example\"resource"`))
		})

		It("should produce valid CEL for a UUID-style ID", func() {
			ref := "550e8400-e29b-41d4-a716-446655440000"
			filter := fmt.Sprintf(`this.id == %[1]q || this.metadata.name == %[1]q`, ref)
			Expect(filter).To(Equal(`this.id == "550e8400-e29b-41d4-a716-446655440000" || this.metadata.name == "550e8400-e29b-41d4-a716-446655440000"`))
		})
	})
})
