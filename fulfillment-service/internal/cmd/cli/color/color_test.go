/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package color

import (
	"bytes"
	"os"
	"testing"

	"github.com/spf13/cobra"
)

func TestEnabled(t *testing.T) {
	root := &cobra.Command{Use: "osac"}
	AddFlag(root)
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	terminal := func(int) bool { return true }
	redirected := func(int) bool { return false }
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")

	if !enabled(root, file, terminal) {
		t.Fatal("TTY should have color")
	}
	if enabled(root, file, redirected) || enabled(root, &bytes.Buffer{}, terminal) {
		t.Fatal("redirected output should be plain")
	}
	t.Setenv("FORCE_COLOR", "1")
	if !enabled(root, &bytes.Buffer{}, redirected) {
		t.Fatal("FORCE_COLOR should color redirected output")
	}
	if err := root.PersistentFlags().Set("color", "false"); err != nil {
		t.Fatal(err)
	}
	if enabled(root, file, terminal) {
		t.Fatal("--color=false should override FORCE_COLOR")
	}
	if err := root.PersistentFlags().Set("color", "true"); err != nil {
		t.Fatal(err)
	}
	if !enabled(root, &bytes.Buffer{}, redirected) {
		t.Fatal("--color should color redirected output")
	}
	t.Setenv("NO_COLOR", "1")
	if enabled(root, file, terminal) {
		t.Fatal("NO_COLOR should override --color")
	}
}
