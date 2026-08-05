/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package serial

// escapeDetector detects the SSH-style escape sequence: CR ~ .
type escapeDetector struct {
	state int // 0 = normal, 1 = after CR, 2 = after CR + ~
}

func newEscapeDetector() *escapeDetector {
	return &escapeDetector{}
}

// feed processes input bytes and returns true if a disconnect sequence is detected.
// Supported sequences: CR/LF followed by ~. (SSH style), or Ctrl+] (0x1D, telnet style).
func (e *escapeDetector) feed(data []byte) bool {
	for _, b := range data {
		// Ctrl+] works at any time, no preceding Enter needed.
		if b == 0x1D {
			return true
		}
		switch e.state {
		case 0:
			if b == '\r' || b == '\n' {
				e.state = 1
			}
		case 1:
			switch b {
			case '~':
				e.state = 2
			case '\r', '\n':
				e.state = 1
			default:
				e.state = 0
			}
		case 2:
			if b == '.' {
				return true
			}
			if b == '\r' || b == '\n' {
				e.state = 1
			} else {
				e.state = 0
			}
		}
	}
	return false
}
