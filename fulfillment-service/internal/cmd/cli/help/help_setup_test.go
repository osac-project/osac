/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package help

import (
	"bytes"
	"regexp"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

// ansiPattern matches ANSI escape sequences (CSI sequences for colors, styles, etc.).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func newTestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "A test command",
		Long:  "A longer description of the test command.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "sub",
		Short: "A subcommand",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	})
	return cmd
}

var _ = Describe("Help output", func() {
	var (
		cmd    *cobra.Command
		output *bytes.Buffer
	)

	BeforeEach(func() {
		cmd = newTestCommand()
		Setup(cmd)
		output = &bytes.Buffer{}
		cmd.SetOut(output)
	})

	It("Does not emit ANSI escape codes when output is not a terminal", func() {
		cmd.SetArgs([]string{"--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).ToNot(BeEmpty())
		Expect(ansiPattern.FindString(output.String())).To(BeEmpty(),
			"help output should not contain ANSI escape codes when writing to a non-TTY")
	})

	It("Contains the command name in non-TTY output", func() {
		cmd.SetArgs([]string{"--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("test"))
	})

	It("Contains the command description in non-TTY output", func() {
		cmd.SetArgs([]string{"--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("A longer description of the test command."))
	})

	It("Contains subcommand information in non-TTY output", func() {
		cmd.SetArgs([]string{"--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("sub"))
	})

	It("Does not emit ANSI escape codes for subcommand help", func() {
		cmd.SetArgs([]string{"sub", "--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(ansiPattern.FindString(output.String())).To(BeEmpty(),
			"subcommand help output should not contain ANSI escape codes when writing to a non-TTY")
	})
})
