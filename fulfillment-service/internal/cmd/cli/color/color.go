/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package color decides whether styled CLI output should use ANSI colors.
package color

import (
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const flag = "color"

// AddFlag registers the color override on the root command.
func AddFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().Bool(flag, false, "_[BOOLEAN]_ - Colorize styled output (help, descriptions, JSON/YAML). NO_COLOR takes precedence.")
}

// Enabled applies NO_COLOR, the explicit flag, FORCE_COLOR, then terminal detection, in that order.
func Enabled(cmd *cobra.Command, out io.Writer) bool {
	return enabled(cmd, out, term.IsTerminal)
}

func enabled(cmd *cobra.Command, out io.Writer, isTerminal func(int) bool) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	flags := cmd.Root().PersistentFlags()
	if flags.Changed(flag) {
		enabled, err := flags.GetBool(flag)
		if err == nil {
			return enabled
		}
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	file, ok := out.(*os.File)
	return ok && isTerminal(int(file.Fd()))
}
