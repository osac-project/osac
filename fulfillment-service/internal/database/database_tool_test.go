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
	neturl "net/url"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
)

var _ = Describe("Database tool", func() {
	var tmpDir string

	// writeFile creates a file with the given name and content inside tmpDir and returns its path.
	writeFile := func(name, content string) string {
		path := filepath.Join(tmpDir, name)
		ExpectWithOffset(1, os.WriteFile(path, []byte(content), 0600)).To(Succeed())
		return path
	}

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, tmpDir)
	})

	Describe("URL configuration via builder methods", func() {
		It("Accepts a URL set directly", func() {
			tool, err := NewTool().
				SetLogger(logger).
				SetURL("postgres://user@host:5432/db").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(Equal("postgres://user@host:5432/db"))
		})

		It("Reads the URL from a file", func() {
			urlFile := writeFile("url", "postgres://user@host:5432/db")

			tool, err := NewTool().
				SetLogger(logger).
				SetURLFile(urlFile).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(Equal("postgres://user@host:5432/db"))
		})

		It("Trims whitespace from the URL file content", func() {
			urlFile := writeFile("url", "  postgres://user@host:5432/db\n")

			tool, err := NewTool().
				SetLogger(logger).
				SetURLFile(urlFile).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(Equal("postgres://user@host:5432/db"))
		})

		It("Rejects specifying both URL and URL file", func() {
			urlFile := writeFile("url", "postgres://from-file@host:5432/db")

			_, err := NewTool().
				SetLogger(logger).
				SetURL("postgres://from-direct@host:5432/db").
				SetURLFile(urlFile).
				Build()
			Expect(err).To(MatchError(
				"database connection URL and URL file are incompatible, use one or the other but " +
					"not both",
			))
		})

		It("Returns an error when the URL file does not exist", func() {
			_, err := NewTool().
				SetLogger(logger).
				SetURLFile("/nonexistent/url-file").
				Build()
			Expect(err).To(MatchError(ContainSubstring("/nonexistent/url-file")))
		})

		It("Returns an error when no URL is provided", func() {
			_, err := NewTool().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("connection URL is mandatory"))
		})
	})

	Describe("URL configuration from a directory", func() {
		It("Reads connection settings from a directory", func() {
			writeFile("url", "postgres://user@host:5432/db")
			writeFile("sslmode", "verify-full")

			tool, err := NewTool().
				SetLogger(logger).
				SetURLFile(tmpDir).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(Equal("postgres://user@host:5432/db?sslmode=verify-full"))
		})

		It("Uses file paths as values for sslcert, sslkey, and sslrootcert", func() {
			writeFile("url", "postgres://user@host:5432/db")
			writeFile("sslcert", "CERT CONTENTS")
			writeFile("sslkey", "KEY CONTENTS")
			writeFile("sslrootcert", "CA CONTENTS")

			tool, err := NewTool().
				SetLogger(logger).
				SetURLFile(tmpDir).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(ContainSubstring("sslcert=" + neturl.QueryEscape(tmpDir+"/sslcert")))
			Expect(tool.URL()).To(ContainSubstring("sslkey=" + neturl.QueryEscape(tmpDir+"/sslkey")))
			Expect(tool.URL()).To(ContainSubstring(
				"sslrootcert=" + neturl.QueryEscape(tmpDir+"/sslrootcert"),
			))
		})

		It("Reads host, port, user, password, and database from a directory", func() {
			writeFile("host", "db.example.com")
			writeFile("port", "5433")
			writeFile("user", "admin")
			writeFile("password", "secret")
			writeFile("dbname", "mydb")

			tool, err := NewTool().
				SetLogger(logger).
				SetURLFile(tmpDir).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(Equal("postgres://admin:secret@db.example.com:5433/mydb"))
		})

		It("Combines a base URL from the directory with additional settings", func() {
			writeFile("url", "postgres://user@host:5432/db")
			writeFile("sslmode", "verify-full")
			writeFile("sslcert", "CERT")
			writeFile("connect_timeout", "5")

			tool, err := NewTool().
				SetLogger(logger).
				SetURLFile(tmpDir).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(ContainSubstring("sslmode=verify-full"))
			Expect(tool.URL()).To(ContainSubstring("connect_timeout=5"))
			Expect(tool.URL()).To(ContainSubstring("sslcert=" + neturl.QueryEscape(tmpDir+"/sslcert")))
		})
	})

	Describe("URL configuration via command line flags", func() {
		parseFlags := func(args ...string) *pflag.FlagSet {
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddFlags(flags)
			ExpectWithOffset(1, flags.Parse(args)).To(Succeed())
			return flags
		}

		It("Accepts a URL via the --db-url flag", func() {
			flags := parseFlags("--db-url", "postgres://user@host:5432/db")

			tool, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(Equal("postgres://user@host:5432/db"))
		})

		It("Reads the URL from a file via the --db-url-file flag", func() {
			urlFile := writeFile("url", "postgres://user@host:5432/db")

			flags := parseFlags("--db-url-file", urlFile)

			tool, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(Equal("postgres://user@host:5432/db"))
		})

		It("Reads connection settings from a directory via the --db-url-file flag", func() {
			writeFile("url", "postgres://user@host:5432/db")
			writeFile("sslmode", "verify-full")
			writeFile("sslcert", "CERT")

			flags := parseFlags("--db-url-file", tmpDir)

			tool, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(ContainSubstring("sslmode=verify-full"))
			Expect(tool.URL()).To(ContainSubstring("sslcert=" + neturl.QueryEscape(tmpDir+"/sslcert")))
		})

		It("Trims whitespace from the URL file content", func() {
			urlFile := writeFile("url", "  postgres://user@host:5432/db\n")

			flags := parseFlags("--db-url-file", urlFile)

			tool, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.URL()).To(Equal("postgres://user@host:5432/db"))
		})

		It("Rejects specifying both --db-url and --db-url-file", func() {
			urlFile := writeFile("url", "postgres://from-file@host:5432/db")

			flags := parseFlags(
				"--db-url", "postgres://from-flag@host:5432/db",
				"--db-url-file", urlFile,
			)

			_, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				Build()
			Expect(err).To(MatchError(
				"database connection URL and URL file are incompatible, use one or the other but not both",
			))
		})

		It("Returns an error when the URL file does not exist", func() {
			flags := parseFlags("--db-url-file", "/nonexistent/url-file")

			_, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				Build()
			Expect(err).To(MatchError(ContainSubstring("/nonexistent/url-file")))
		})

		It("Returns an error when no URL is provided", func() {
			flags := parseFlags()

			_, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				Build()
			Expect(err).To(MatchError("connection URL is mandatory"))
		})
	})
})
