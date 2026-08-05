/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package database

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Migrations", func() {
	It("All migrations have the '.up.sql' or '.down.sql' suffix", func() {
		files, err := filepath.Glob("migrations/*.sql")
		Expect(err).ToNot(HaveOccurred())
		Expect(files).ToNot(BeEmpty())
		for _, file := range files {
			Expect(file).To(MatchRegexp(`\.(up|down)\.sql$`))
		}
	})

	It("Has no duplicate migration numbers", func() {
		files, err := filepath.Glob("migrations/*.sql")
		Expect(err).ToNot(HaveOccurred())
		Expect(files).ToNot(BeEmpty())

		seen := map[string][]string{}
		for _, file := range files {
			base := filepath.Base(file)
			parts := strings.SplitN(base, "_", 2)
			if len(parts) < 2 {
				continue
			}
			n, err := strconv.Atoi(parts[0])
			Expect(err).ToNot(HaveOccurred(), "failed to parse migration number from %s", base)
			var direction string
			switch {
			case strings.HasSuffix(base, ".up.sql"):
				direction = "up"
			case strings.HasSuffix(base, ".down.sql"):
				direction = "down"
			default:
				continue
			}
			key := fmt.Sprintf("%d_%s", n, direction)
			seen[key] = append(seen[key], base)
		}

		var duplicates []string
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if len(seen[key]) > 1 {
				duplicates = append(duplicates, fmt.Sprintf("%s: %v", key, seen[key]))
			}
		}
		Expect(duplicates).To(BeEmpty(), "found duplicate migration numbers: %v", duplicates)
	})

	It("Has migration filenames that follow naming convention", func() {
		files, err := filepath.Glob("migrations/*.sql")
		Expect(err).ToNot(HaveOccurred())
		Expect(files).ToNot(BeEmpty())

		pattern := regexp.MustCompile(`^(\d+)_[a-z][a-z0-9_]*[a-z0-9]\.(up|down)\.sql$`)
		var violations []string
		for _, file := range files {
			base := filepath.Base(file)
			if !pattern.MatchString(base) {
				violations = append(violations, base)
			}
		}
		Expect(violations).To(BeEmpty(), "migration filenames violate naming convention: %v", violations)
	})

	It("Has an up-to-date migrations hash", func() {
		// Read the stored hash:
		storedHashFile, err := filepath.Abs("migrations.sha256")
		Expect(err).ToNot(HaveOccurred())
		storedHashBytes, err := os.ReadFile(storedHashFile)
		Expect(err).ToNot(HaveOccurred())
		storedHashText := strings.TrimSpace(string(storedHashBytes))

		// Get the names of the migration migrationFiles and sort them:
		migrationFiles, err := filepath.Glob("migrations/*.up.sql")
		Expect(err).ToNot(HaveOccurred())
		Expect(migrationFiles).ToNot(BeEmpty())
		migrationNames := make([]string, len(migrationFiles))
		for i, file := range migrationFiles {
			migrationNames[i] = filepath.Base(file)
		}
		sort.Strings(migrationNames)

		// Compute the SHA-256 hash of the migration file list:
		computedHashSource := &bytes.Buffer{}
		for _, migrationName := range migrationNames {
			_, err := fmt.Fprintf(computedHashSource, "%s\n", migrationName)
			Expect(err).ToNot(HaveOccurred())
		}
		computedHashBytes := sha256.Sum256(computedHashSource.Bytes())
		computedHashText := fmt.Sprintf("%x", computedHashBytes)

		// Compare the computed hash with the stored hash:
		if computedHashText != storedHashText {
			Fail(fmt.Sprintf(
				"Database migrations hash in '%s' is outdated, run 'uv run dev.py update hashes' "+
					"to update it",
				storedHashFile,
			))
		}
	})

	It("Has no unexpected gaps in migration numbering", func() {
		files, err := filepath.Glob("migrations/*.up.sql")
		Expect(err).ToNot(HaveOccurred())
		Expect(files).ToNot(BeEmpty())

		knownGaps := map[int]bool{18: true}

		present := map[int]bool{}
		maxNum := 0
		for _, file := range files {
			base := filepath.Base(file)
			parts := strings.SplitN(base, "_", 2)
			if len(parts) < 2 {
				continue
			}
			n, err := strconv.Atoi(parts[0])
			Expect(err).ToNot(HaveOccurred(), "failed to parse migration number from %s", base)
			present[n] = true
			if n > maxNum {
				maxNum = n
			}
		}

		var gaps []int
		for i := 0; i <= maxNum; i++ {
			if !present[i] && !knownGaps[i] {
				gaps = append(gaps, i)
			}
		}
		Expect(gaps).To(BeEmpty(), "unexpected gaps in migration numbering: %v", gaps)
	})
})
