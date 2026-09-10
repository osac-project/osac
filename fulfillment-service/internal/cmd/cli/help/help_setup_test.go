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
	"path/filepath"
	"regexp"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

// ansiPattern matches the ESC character that starts all ANSI escape sequences (CSI, OSC, etc.).
var ansiPattern = regexp.MustCompile(`\x1b`)

func newTestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "A test command",
		Long:  "A longer description of the test command.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	cmd.PersistentFlags().Bool(NoColorFlag, false, "Disable colored output")
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

	// These --no-color tests verify the flag is accepted without error. They don't exercise the
	// TTY color-suppression path because bytes.Buffer is not a TTY; TTY behavior is verified manually.
	It("Does not emit ANSI escape codes when --no-color flag is set", func() {
		cmd.SetArgs([]string{"--no-color", "--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).ToNot(BeEmpty())
		Expect(ansiPattern.FindString(output.String())).To(BeEmpty(),
			"help output should not contain ANSI escape codes when --no-color is set")
	})

	It("Does not emit ANSI escape codes for subcommand help when --no-color flag is set", func() {
		cmd.SetArgs([]string{"--no-color", "sub", "--help"})
		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).ToNot(BeEmpty())
		Expect(ansiPattern.FindString(output.String())).To(BeEmpty(),
			"subcommand help should not contain ANSI escape codes when --no-color is set")
	})
})

var _ = Describe("hidePrivateSubcommands", func() {
	var (
		root       *cobra.Command
		parent     *cobra.Command
		publicSub  *cobra.Command
		privateSub *cobra.Command
	)

	BeforeEach(func() {
		root = &cobra.Command{Use: "osac"}
		parent = &cobra.Command{Use: "create"}
		publicSub = &cobra.Command{Use: "cluster", Short: "Create a cluster"}
		privateSub = &cobra.Command{
			Use:         "hub",
			Short:       "Create a hub",
			Annotations: map[string]string{"api": "private"},
		}
		parent.AddCommand(publicSub)
		parent.AddCommand(privateSub)
		root.AddCommand(parent)
	})

	It("hides private subcommands when config has private=false", func() {
		tmpDir := GinkgoT().TempDir()
		err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"private": false}`), 0600)
		Expect(err).ToNot(HaveOccurred())
		root.PersistentFlags().String("config", "", "")
		Expect(root.PersistentFlags().Set("config", tmpDir)).To(Succeed())

		hidePrivateSubcommands(parent)

		Expect(privateSub.Hidden).To(BeTrue())
		Expect(publicSub.Hidden).To(BeFalse())
	})

	It("shows private subcommands when config has private=true", func() {
		tmpDir := GinkgoT().TempDir()
		err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"private": true}`), 0600)
		Expect(err).ToNot(HaveOccurred())
		root.PersistentFlags().String("config", "", "")
		Expect(root.PersistentFlags().Set("config", tmpDir)).To(Succeed())

		hidePrivateSubcommands(parent)

		Expect(privateSub.Hidden).To(BeFalse())
		Expect(publicSub.Hidden).To(BeFalse())
	})

	It("hides private subcommands when no config file exists", func() {
		tmpDir := GinkgoT().TempDir()
		root.PersistentFlags().String("config", "", "")
		Expect(root.PersistentFlags().Set("config", tmpDir)).To(Succeed())

		hidePrivateSubcommands(parent)

		Expect(privateSub.Hidden).To(BeTrue())
		Expect(publicSub.Hidden).To(BeFalse())
	})

	It("reads config from OSAC_CONFIG env var when flag is not explicitly set", func() {
		tmpDir := GinkgoT().TempDir()
		err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"private": true}`), 0600)
		Expect(err).ToNot(HaveOccurred())
		root.PersistentFlags().String("config", "/nonexistent", "")

		original := os.Getenv("OSAC_CONFIG")
		os.Setenv("OSAC_CONFIG", tmpDir)
		DeferCleanup(func() {
			if original == "" {
				os.Unsetenv("OSAC_CONFIG")
			} else {
				os.Setenv("OSAC_CONFIG", original)
			}
		})

		hidePrivateSubcommands(parent)

		Expect(privateSub.Hidden).To(BeFalse())
		Expect(publicSub.Hidden).To(BeFalse())
	})

	It("does nothing when no subcommands have private annotation", func() {
		cmd := &cobra.Command{Use: "get"}
		sub := &cobra.Command{Use: "cluster"}
		cmd.AddCommand(sub)
		root.AddCommand(cmd)
		root.PersistentFlags().String("config", "", "")
		Expect(root.PersistentFlags().Set("config", GinkgoT().TempDir())).To(Succeed())

		hidePrivateSubcommands(cmd)

		Expect(sub.Hidden).To(BeFalse())
	})
})
