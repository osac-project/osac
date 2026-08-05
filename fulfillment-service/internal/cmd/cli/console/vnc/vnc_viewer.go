/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vnc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/osac-project/fulfillment-service/internal/exit"
)

// viewer describes a VNC viewer binary and how to format its connection address.
type viewer struct {
	name     string
	addrFunc func(host string, port int) string
	argsFunc func(addr string) []string
}

// Binary names of supported VNC viewers.
const (
	binRemoteViewer = "remote-viewer"
	binVNCViewer    = "vncviewer"
	binRemmina      = "remmina"
)

// detectViewer finds a VNC viewer binary. If explicit is non-empty, it is
// resolved as a path (if it contains /) or looked up on PATH. Otherwise,
// platform-specific auto-detection is used.
func (c *runnerContext) detectViewer(ctx context.Context, explicit string) (*viewer, error) {
	if explicit != "" {
		v, err := c.resolveExplicit(ctx, explicit)
		if err != nil {
			c.console.Render(ctx, "viewer_error.txt", map[string]any{
				"Error": err.Error(),
			})
			return nil, exit.Error(1)
		}
		return v, nil
	}
	var v *viewer
	var err error
	switch runtime.GOOS {
	case "darwin":
		v, err = autoDetectDarwin()
	default:
		v, err = autoDetectLinux()
	}
	if err != nil {
		viewers := "virt-viewer, TigerVNC, or Remmina"
		if runtime.GOOS == "darwin" {
			viewers = "TigerVNC or another VNC viewer"
		}
		c.console.Render(ctx, "no_viewer_found.txt", map[string]any{
			"Viewers": viewers,
		})
		return nil, exit.Error(1)
	}
	return v, nil
}

// resolveExplicit resolves s as a path (if it contains a separator) or as a binary name on PATH.
func (c *runnerContext) resolveExplicit(ctx context.Context, s string) (*viewer, error) {
	var path string
	if strings.ContainsRune(s, os.PathSeparator) {
		if _, err := os.Stat(s); err != nil {
			return nil, fmt.Errorf("viewer not found: %s", s)
		}
		path = s
	} else {
		p, err := exec.LookPath(s)
		if err != nil {
			return nil, fmt.Errorf("viewer %q not found on PATH", s)
		}
		path = p
	}
	af, argf := knownViewerOverrides(path)
	if af == nil {
		c.console.Render(ctx, "unrecognized_viewer.txt", map[string]any{
			"Viewer":    filepath.Base(path),
			"Supported": strings.Join([]string{binRemoteViewer, binVNCViewer, binRemmina}, ", "),
		})
		af = plainAddr
		argf = func(addr string) []string { return []string{path, addr} }
	}
	return &viewer{
		name:     filepath.Base(path),
		addrFunc: af,
		argsFunc: argf,
	}, nil
}

// knownViewerOverrides returns viewer-specific addrFunc and argsFunc for
// recognized binary names. Returns nil, nil when the binary is not recognized.
func knownViewerOverrides(path string) (
	func(string, int) string,
	func(string) []string,
) {
	switch filepath.Base(path) {
	case binRemoteViewer, binRemmina:
		return vncURI, func(addr string) []string { return []string{path, addr} }
	case binVNCViewer:
		if runtime.GOOS == "darwin" {
			return plainAddr, func(addr string) []string {
				return []string{path, "-WarnUnencrypted=0", addr}
			}
		}
		return plainAddr, func(addr string) []string { return []string{path, addr} }
	default:
		return nil, nil
	}
}

// Auto-detect: Linux
// Priority: 1. remote-viewer (virt-viewer), 2. vncviewer (TigerVNC), 3. remmina
func autoDetectLinux() (*viewer, error) {
	if path, err := exec.LookPath(binRemoteViewer); err == nil {
		return &viewer{
			name:     binRemoteViewer,
			addrFunc: vncURI,
			argsFunc: func(addr string) []string { return []string{path, addr} },
		}, nil
	}
	if path, err := exec.LookPath(binVNCViewer); err == nil {
		return &viewer{
			name:     binVNCViewer,
			addrFunc: plainAddr,
			argsFunc: func(addr string) []string { return []string{path, addr} },
		}, nil
	}
	if path, err := exec.LookPath(binRemmina); err == nil {
		return &viewer{
			name:     binRemmina,
			addrFunc: vncURI,
			argsFunc: func(addr string) []string { return []string{path, addr} },
		}, nil
	}
	return nil, fmt.Errorf("no VNC viewer found; install virt-viewer, tigervnc, or remmina")
}

// Auto-detect: macOS
// Priority: 1. TigerVNC Viewer.app, 2. TigerVNC legacy versioned,
//  3. Chicken.app, 4. RealVNC Viewer, 5. remote-viewer (Homebrew)
func autoDetectDarwin() (*viewer, error) {
	// 1. TigerVNC Viewer.app
	tigerPath := "/Applications/TigerVNC Viewer.app"
	if _, err := os.Stat(tigerPath); err == nil {
		return &viewer{
			name:     "TigerVNC Viewer",
			addrFunc: vncURI,
			argsFunc: func(addr string) []string {
				return []string{"open", "-W", "-a", tigerPath, "--args", addr}
			},
		}, nil
	}

	// 2. TigerVNC legacy versioned bundles
	matches, _ := filepath.Glob("/Applications/TigerVNC Viewer *.app")
	if len(matches) > 0 {
		legacyPath := matches[len(matches)-1]
		return &viewer{
			name:     filepath.Base(legacyPath),
			addrFunc: vncURI,
			argsFunc: func(addr string) []string {
				return []string{"open", "-W", "-a", legacyPath, "--args", addr}
			},
		}, nil
	}

	// 3. Chicken.app
	chickenPath := "/Applications/Chicken.app"
	if _, err := os.Stat(chickenPath); err == nil {
		return &viewer{
			name:     "Chicken",
			addrFunc: vncURI,
			argsFunc: func(addr string) []string {
				return []string{"open", "-W", "-a", chickenPath, "--args", addr}
			},
		}, nil
	}

	// 4. RealVNC Viewer
	if path, err := exec.LookPath(binVNCViewer); err == nil {
		return &viewer{
			name:     "RealVNC Viewer",
			addrFunc: plainAddr,
			argsFunc: func(addr string) []string {
				return []string{path, "-WarnUnencrypted=0", addr}
			},
		}, nil
	}

	// 5. remote-viewer (Homebrew)
	if path, err := exec.LookPath(binRemoteViewer); err == nil {
		return &viewer{
			name:     binRemoteViewer,
			addrFunc: vncURI,
			argsFunc: func(addr string) []string { return []string{path, addr} },
		}, nil
	}

	return nil, fmt.Errorf("no VNC viewer found; install TigerVNC or another VNC viewer")
}

// plainAddr formats a connection address as host:port.
func plainAddr(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}

// vncURI formats a connection address as vnc://host:port.
func vncURI(host string, port int) string {
	return fmt.Sprintf("vnc://%s:%d", host, port)
}

// launchViewer starts the viewer process. The returned *exec.Cmd should be
// waited on to detect when the viewer window closes.
func launchViewer(v *viewer, addr string) (*exec.Cmd, error) {
	args := v.argsFunc(addr)
	cmd := exec.Command(args[0], args[1:]...) // #nosec G204 -- viewer binary is auto-detected or user-specified
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", v.name, err)
	}
	return cmd, nil
}
