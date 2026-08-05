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
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

var _ = Describe("Viewer detection", func() {
	var (
		c   *runnerContext
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		console, err := terminal.NewConsole().
			SetLogger(slog.Default()).
			SetStdout(io.Discard).
			SetStderr(io.Discard).
			Build()
		Expect(err).NotTo(HaveOccurred())
		err = console.AddTemplates(templatesFS, "templates")
		Expect(err).NotTo(HaveOccurred())
		c = &runnerContext{
			logger:  slog.Default(),
			console: console,
		}
	})

	Describe("resolveExplicit", func() {
		It("should resolve a path containing /", func() {
			v, err := c.resolveExplicit(ctx, "/bin/sh")
			Expect(err).NotTo(HaveOccurred())
			Expect(v.name).To(Equal("sh"))
			args := v.argsFunc("test-addr")
			Expect(args[0]).To(Equal("/bin/sh"))
		})

		It("should fail for a non-existent path", func() {
			_, err := c.resolveExplicit(ctx, "/nonexistent/viewer")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("viewer not found"))
		})

		It("should resolve a name on PATH", func() {
			// "sh" should be on PATH in any test environment.
			v, err := c.resolveExplicit(ctx, "sh")
			Expect(err).NotTo(HaveOccurred())
			Expect(v.name).To(Equal("sh"))
		})

		It("should fail for a name not on PATH", func() {
			_, err := c.resolveExplicit(ctx, "nonexistent-vnc-viewer-binary")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found on PATH"))
		})
	})

	Describe("address formatting", func() {
		It("should format plain address", func() {
			Expect(plainAddr("127.0.0.1", 5900)).To(Equal("127.0.0.1:5900"))
		})

		It("should format VNC URI", func() {
			Expect(vncURI("127.0.0.1", 5900)).To(Equal("vnc://127.0.0.1:5900"))
		})
	})

	Describe("resolveExplicit with known viewers", func() {
		var tmpDir string

		BeforeEach(func() {
			tmpDir = GinkgoT().TempDir()
		})

		createFakeBinary := func(name string) string {
			p := filepath.Join(tmpDir, name)
			err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0755)
			Expect(err).NotTo(HaveOccurred())
			return p
		}

		It("should use vncURI for remote-viewer", func() {
			path := createFakeBinary(binRemoteViewer)
			v, err := c.resolveExplicit(ctx, path)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.name).To(Equal(binRemoteViewer))
			Expect(v.addrFunc("127.0.0.1", 5900)).To(Equal("vnc://127.0.0.1:5900"))
			args := v.argsFunc("vnc://127.0.0.1:5900")
			Expect(args).To(Equal([]string{path, "vnc://127.0.0.1:5900"}))
		})

		It("should use vncURI for remmina", func() {
			path := createFakeBinary(binRemmina)
			v, err := c.resolveExplicit(ctx, path)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.name).To(Equal(binRemmina))
			Expect(v.addrFunc("127.0.0.1", 5900)).To(Equal("vnc://127.0.0.1:5900"))
			args := v.argsFunc("vnc://127.0.0.1:5900")
			Expect(args).To(Equal([]string{path, "vnc://127.0.0.1:5900"}))
		})

		It("should use plainAddr for vncviewer", func() {
			path := createFakeBinary(binVNCViewer)
			v, err := c.resolveExplicit(ctx, path)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.name).To(Equal(binVNCViewer))
			Expect(v.addrFunc("127.0.0.1", 5900)).To(Equal("127.0.0.1:5900"))
		})

		It("should add -WarnUnencrypted=0 for vncviewer on macOS only", func() {
			path := createFakeBinary(binVNCViewer)
			v, err := c.resolveExplicit(ctx, path)
			Expect(err).NotTo(HaveOccurred())
			args := v.argsFunc("127.0.0.1:5900")
			if runtime.GOOS == "darwin" {
				Expect(args).To(Equal([]string{path, "-WarnUnencrypted=0", "127.0.0.1:5900"}))
			} else {
				Expect(args).To(Equal([]string{path, "127.0.0.1:5900"}))
			}
		})

		It("should fall back to generic for unknown viewer", func() {
			path := createFakeBinary("my-custom-vnc")
			v, err := c.resolveExplicit(ctx, path)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.name).To(Equal("my-custom-vnc"))
			Expect(v.addrFunc("127.0.0.1", 5900)).To(Equal("127.0.0.1:5900"))
			args := v.argsFunc("127.0.0.1:5900")
			Expect(args).To(Equal([]string{path, "127.0.0.1:5900"}))
		})
	})

	Describe("detectViewer", func() {
		It("should resolve an explicit viewer by path", func() {
			tmpDir := GinkgoT().TempDir()
			fakeBin := filepath.Join(tmpDir, "fake-viewer")
			err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0755)
			Expect(err).NotTo(HaveOccurred())

			v, err := c.detectViewer(ctx, fakeBin)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.name).To(Equal("fake-viewer"))
			args := v.argsFunc("test-addr")
			Expect(args[0]).To(Equal(fakeBin))
		})

		It("should return error for explicit viewer that does not exist", func() {
			_, err := c.detectViewer(ctx, "/tmp/nonexistent-vnc-viewer-binary")
			Expect(err).To(HaveOccurred())
		})
	})
})
