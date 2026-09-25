/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package render

import (
	"io"
	"os"
	"os/exec"
	"strings"

	xterm "golang.org/x/term"
)

// Page writes content to w, running it through a pager (less by default,
// $PAGER if set) when w is a real terminal, so a long checks table can be
// scrolled with the pager's own keys (arrows, PgUp/PgDn, q to quit) instead
// of the terminal's own scrollback. Falls back to writing content directly
// to w -- unpaged -- when w isn't a terminal (piped/redirected output,
// tests), when no pager binary can be found, or if the pager itself fails
// partway through, so the results are never simply lost.
func Page(w io.Writer, content string) error {
	if file, ok := w.(*os.File); ok && xterm.IsTerminal(int(file.Fd())) {
		name, args := pagerCommand()
		if path, err := exec.LookPath(name); err == nil {
			cmd := exec.Command(path, args...) // #nosec G204 -- binary resolved via exec.LookPath
			cmd.Stdin = strings.NewReader(content)
			cmd.Stdout = file
			cmd.Stderr = os.Stderr
			if cmd.Run() == nil {
				return nil
			}
			// Fall through to a plain write: the pager may have drawn a
			// partial screen, but the caller still gets the full content.
		}
	}
	_, err := io.WriteString(w, content)
	return err
}

// pagerCommand resolves the pager to run from $PAGER (read fresh on every
// call, not cached at package init, so tests can override it), defaulting
// to less with the same flags git's default pager uses: -F (exit
// immediately if the content fits on one screen, so short results aren't
// hidden behind a pager for nothing), -R (interpret color escape codes
// instead of showing them literally -- this is what keeps the checks
// table's PASS/FAIL colors visible through the pager instead of printing
// raw escape sequences), -X (don't clear the screen on exit, so the table
// stays in scrollback afterward). A custom $PAGER is used exactly as given,
// with no flags appended -- if it doesn't itself understand color escapes,
// that's the user's own pager choice, not something to second-guess here.
func pagerCommand() (string, []string) {
	if pager := os.Getenv("PAGER"); pager != "" {
		fields := strings.Fields(pager)
		return fields[0], fields[1:]
	}
	return "less", []string{"-F", "-R", "-X"}
}
