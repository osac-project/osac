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
	"io"
	"os"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

type colorCase struct {
	noColor, forceColor, flag string
	tty, buffer, want         bool
}

var _ = Describe("CLI color", func() {
	DescribeTable("enabled",
		func(tc colorCase) {
			GinkgoT().Setenv("NO_COLOR", tc.noColor)
			GinkgoT().Setenv("FORCE_COLOR", tc.forceColor)
			root := &cobra.Command{Use: "osac"}
			AddFlag(root)
			if tc.flag != "" {
				Expect(root.PersistentFlags().Set("color", tc.flag)).To(Succeed())
			}

			var out io.Writer
			if tc.buffer {
				out = &bytes.Buffer{}
			} else {
				file, err := os.CreateTemp(GinkgoT().TempDir(), "stdout")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { Expect(file.Close()).To(Succeed()) })
				out = file
			}
			Expect(enabled(root, out, func(int) bool { return tc.tty })).To(Equal(tc.want))
		},
		Entry("terminal output", colorCase{tty: true, want: true}),
		Entry("redirected file", colorCase{}),
		Entry("buffer even with a terminal predicate", colorCase{tty: true, buffer: true}),
		Entry("FORCE_COLOR redirects color to a buffer", colorCase{forceColor: "1", buffer: true, want: true}),
		Entry("--color=false overrides FORCE_COLOR", colorCase{forceColor: "1", flag: "false", tty: true}),
		Entry("--color enables redirected output", colorCase{flag: "true", buffer: true, want: true}),
		Entry("NO_COLOR overrides --color", colorCase{noColor: "1", flag: "true", tty: true}),
	)
})
