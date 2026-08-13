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
	"os"
	"regexp"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

// ansiPattern matches ANSI escape sequences (CSI sequences for colors, styles, etc.).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// saveAndSetEnv sets an environment variable and registers cleanup to restore its original state.
func saveAndSetEnv(name, value string) {
	origVal, wasSet := os.LookupEnv(name)
	Expect(os.Setenv(name, value)).To(Succeed())
	DeferCleanup(func() {
		if wasSet {
			os.Setenv(name, origVal)
		} else {
			os.Unsetenv(name)
		}
	})
}

// saveAndUnsetEnv unsets an environment variable and registers cleanup to restore its original state.
func saveAndUnsetEnv(name string) {
	origVal, wasSet := os.LookupEnv(name)
	os.Unsetenv(name)
	DeferCleanup(func() {
		if wasSet {
			os.Setenv(name, origVal)
		}
	})
}

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

	It("Emits ANSI escape codes when FORCE_COLOR is set", func() {
		saveAndSetEnv("FORCE_COLOR", "1")
		saveAndUnsetEnv("NO_COLOR")

		cmd.SetArgs([]string{"--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).ToNot(BeEmpty())
		Expect(ansiPattern.FindString(output.String())).ToNot(BeEmpty(),
			"help output should contain ANSI escape codes when FORCE_COLOR is set")
	})

	It("Does not include heading prefixes in plain output", func() {
		cmd.SetArgs([]string{"--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).ToNot(HavePrefix("# "),
			"help output should not contain Markdown heading prefix in plain mode")
	})

	It("Does not emit ANSI escape codes when both FORCE_COLOR and NO_COLOR are set", func() {
		saveAndSetEnv("FORCE_COLOR", "1")
		saveAndSetEnv("NO_COLOR", "1")

		cmd.SetArgs([]string{"--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).ToNot(BeEmpty())
		Expect(ansiPattern.FindString(output.String())).To(BeEmpty(),
			"help output should not contain ANSI escape codes when NO_COLOR overrides FORCE_COLOR")
	})
})
