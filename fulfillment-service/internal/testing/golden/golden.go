/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package golden compares test output against golden files in the calling package's testdata directory, rewriting
// them when UPDATE_GOLDEN is set. It is the single home of the write-then-compare dance every golden spec needs.
package golden

import (
	"os"
	"path/filepath"

	"github.com/onsi/gomega"
)

// Assert compares actual against the golden file name in the calling package's testdata directory. Set UPDATE_GOLDEN
// to rewrite the file instead; review the diff before committing it. Run from a ginkgo suite: the assertions fail
// through the suite's registered gomega handler.
func Assert(name string, actual string) {
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		gomega.Expect(os.WriteFile(path, []byte(actual), 0o644)).To(gomega.Succeed())
	}
	expected, err := os.ReadFile(path)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(actual).To(gomega.Equal(string(expected)))
}
