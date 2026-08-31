/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package login

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"

	"github.com/osac-project/osac/fulfillment-service/internal/oauth"
)

var _ = Describe("readTrimmedFile", func() {
	var runner *runnerContext
	var tmpDir string

	BeforeEach(func() {
		runner = &runnerContext{}
		var err error
		tmpDir, err = os.MkdirTemp("", "login-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	It("reads a file and trims a trailing newline", func() {
		testValue := "test-fixture-value"
		f := filepath.Join(tmpDir, "secret.txt")
		Expect(os.WriteFile(f, []byte(testValue+"\n"), 0600)).To(Succeed())
		result, err := runner.readTrimmedFile(f)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(testValue))
	})

	It("reads a file and trims surrounding whitespace", func() {
		testValue := "test-fixture-value"
		f := filepath.Join(tmpDir, "secret.txt")
		Expect(os.WriteFile(f, []byte("  "+testValue+"  \n"), 0600)).To(Succeed())
		result, err := runner.readTrimmedFile(f)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(testValue))
	})

	It("reads an empty file and returns an empty string", func() {
		f := filepath.Join(tmpDir, "empty.txt")
		Expect(os.WriteFile(f, []byte(""), 0600)).To(Succeed())
		result, err := runner.readTrimmedFile(f)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(""))
	})

	It("returns an error when the file does not exist", func() {
		_, err := runner.readTrimmedFile(filepath.Join(tmpDir, "nonexistent.txt"))
		Expect(err).To(HaveOccurred())
	})

	It("returns an error when the path is a directory", func() {
		_, err := runner.readTrimmedFile(tmpDir)
		Expect(err).To(MatchError(ContainSubstring("not a regular file")))
	})
})

// newFileFlagSet creates a minimal pflag.FlagSet that covers all flags read by
// resolveFileFlags and inferFlow, bound to the given runnerContext.
func newFileFlagSet(r *runnerContext) *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.StringVar(&r.args.password, "password", "", "")
	fs.StringVar(&r.args.passwordFile, "password-file", "", "")
	fs.StringVar(&r.args.clientSecret, "client-secret", "", "")
	fs.StringVar(&r.args.clientSecretFile, "client-secret-file", "", "")
	fs.StringVar(&r.args.user, "user", "", "")
	fs.StringVar(&r.args.userFile, "user-file", "", "")
	fs.StringVar(&r.args.clientId, "client-id", "", "")
	fs.StringVar(&r.args.clientIdFile, "client-id-file", "", "")
	// inferFlow and resolveFileFlags also check these deprecated aliases:
	fs.StringVar(&r.args.flow, "flow", defaultFlow, "")
	fs.StringVar(&r.args.flow, "oauth-flow", defaultFlow, "")
	fs.StringVar(&r.args.clientSecret, "oauth-client-secret", "", "")
	fs.StringVar(&r.args.user, "oauth-user", "", "")
	fs.StringVar(&r.args.password, "oauth-password", "", "")
	fs.StringVar(&r.args.clientId, "oauth-client-id", "", "")
	return fs
}

var _ = Describe("resolveFileFlags", func() {
	var runner *runnerContext
	var tmpDir string

	BeforeEach(func() {
		runner = &runnerContext{}
		runner.flags = newFileFlagSet(runner)
		var err error
		tmpDir, err = os.MkdirTemp("", "login-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	writeFile := func(name, content string) string {
		path := filepath.Join(tmpDir, name)
		ExpectWithOffset(1, os.WriteFile(path, []byte(content), 0600)).To(Succeed())
		return path
	}

	Describe("--password-file", func() {
		It("reads password from file and trims whitespace", func() {
			testPassword := "test-pw-fixture"
			path := writeFile("pw.txt", testPassword+"\n")
			Expect(runner.flags.Parse([]string{"--password-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(Succeed())
			Expect(runner.args.password).To(Equal(testPassword))
		})

		It("returns an error when both --password and --password-file are provided", func() {
			path := writeFile("pw.txt", "test-pw-fixture\n")
			Expect(runner.flags.Parse([]string{"--password=test-direct", "--password-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(MatchError(ContainSubstring("mutually exclusive")))
		})

		It("returns an error when the file does not exist", func() {
			Expect(runner.flags.Parse([]string{"--password-file=" + filepath.Join(tmpDir, "missing.txt")})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(HaveOccurred())
		})
	})

	Describe("--client-secret-file", func() {
		It("reads client secret from file and trims whitespace", func() {
			testSecret := "test-secret-fixture"
			path := writeFile("secret.txt", testSecret+"\n")
			Expect(runner.flags.Parse([]string{"--client-secret-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(Succeed())
			Expect(runner.args.clientSecret).To(Equal(testSecret))
		})

		It("returns an error when both --client-secret and --client-secret-file are provided", func() {
			path := writeFile("secret.txt", "test-secret-fixture\n")
			Expect(runner.flags.Parse([]string{"--client-secret=test-direct", "--client-secret-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(MatchError(ContainSubstring("mutually exclusive")))
		})
	})

	Describe("--user-file", func() {
		It("reads user from file and trims whitespace", func() {
			testUser := "test-user-fixture"
			path := writeFile("user.txt", testUser+"\n")
			Expect(runner.flags.Parse([]string{"--user-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(Succeed())
			Expect(runner.args.user).To(Equal(testUser))
		})

		It("returns an error when both --user and --user-file are provided", func() {
			path := writeFile("user.txt", "test-user-fixture\n")
			Expect(runner.flags.Parse([]string{"--user=test-other", "--user-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(MatchError(ContainSubstring("mutually exclusive")))
		})
	})

	Describe("--client-id-file", func() {
		It("reads client ID from file and trims whitespace", func() {
			testClientId := "test-client-fixture"
			path := writeFile("id.txt", testClientId+"\n")
			Expect(runner.flags.Parse([]string{"--client-id-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(Succeed())
			Expect(runner.args.clientId).To(Equal(testClientId))
		})

		It("returns an error when both --client-id and --client-id-file are provided", func() {
			path := writeFile("id.txt", "test-client-fixture\n")
			Expect(runner.flags.Parse([]string{"--client-id=test-other", "--client-id-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(MatchError(ContainSubstring("mutually exclusive")))
		})
	})

	Describe("deprecated oauth-* alias conflicts", func() {
		It("returns an error when --oauth-password and --password-file are both provided", func() {
			path := writeFile("pw.txt", "test-fixture\n")
			Expect(runner.flags.Parse([]string{"--oauth-password=test-direct", "--password-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(MatchError(ContainSubstring("mutually exclusive")))
		})

		It("returns an error when --oauth-client-secret and --client-secret-file are both provided", func() {
			path := writeFile("secret.txt", "test-fixture\n")
			Expect(runner.flags.Parse([]string{"--oauth-client-secret=test-direct", "--client-secret-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(MatchError(ContainSubstring("mutually exclusive")))
		})

		It("returns an error when --oauth-user and --user-file are both provided", func() {
			path := writeFile("user.txt", "test-user-fixture\n")
			Expect(runner.flags.Parse([]string{"--oauth-user=test-other", "--user-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(MatchError(ContainSubstring("mutually exclusive")))
		})

		It("returns an error when --oauth-client-id and --client-id-file are both provided", func() {
			path := writeFile("id.txt", "test-client-fixture\n")
			Expect(runner.flags.Parse([]string{"--oauth-client-id=test-other", "--client-id-file=" + path})).To(Succeed())
			Expect(runner.resolveFileFlags()).To(MatchError(ContainSubstring("mutually exclusive")))
		})
	})
})

var _ = Describe("inferFlow", func() {
	var runner *runnerContext

	BeforeEach(func() {
		runner = &runnerContext{}
		runner.args.flow = defaultFlow
		runner.flags = newFileFlagSet(runner)
	})

	It("sets credentials flow when --client-secret-file is provided", func() {
		Expect(runner.flags.Parse([]string{"--client-secret-file=foo"})).To(Succeed())
		Expect(runner.inferFlow(context.Background())).To(Succeed())
		Expect(runner.args.flow).To(Equal(string(oauth.CredentialsFlow)))
	})

	It("does not infer flow when only --client-id-file is provided (client ID alone is insufficient)", func() {
		Expect(runner.flags.Parse([]string{"--client-id-file=foo"})).To(Succeed())
		Expect(runner.inferFlow(context.Background())).To(Succeed())
		Expect(runner.args.flow).To(Equal(defaultFlow))
	})

	It("sets password flow when --user-file is provided", func() {
		Expect(runner.flags.Parse([]string{"--user-file=foo"})).To(Succeed())
		Expect(runner.inferFlow(context.Background())).To(Succeed())
		Expect(runner.args.flow).To(Equal(string(oauth.PasswordFlow)))
	})

	It("sets password flow when --password-file is provided", func() {
		Expect(runner.flags.Parse([]string{"--password-file=foo"})).To(Succeed())
		Expect(runner.inferFlow(context.Background())).To(Succeed())
		Expect(runner.args.flow).To(Equal(string(oauth.PasswordFlow)))
	})

	It("existing --client-secret still triggers credentials flow (backwards compat)", func() {
		Expect(runner.flags.Parse([]string{"--client-secret=foo"})).To(Succeed())
		Expect(runner.inferFlow(context.Background())).To(Succeed())
		Expect(runner.args.flow).To(Equal(string(oauth.CredentialsFlow)))
	})

	It("existing --password still triggers password flow (backwards compat)", func() {
		Expect(runner.flags.Parse([]string{"--password=foo"})).To(Succeed())
		Expect(runner.inferFlow(context.Background())).To(Succeed())
		Expect(runner.args.flow).To(Equal(string(oauth.PasswordFlow)))
	})

	It("does not change flow when --flow is explicitly set", func() {
		Expect(runner.flags.Parse([]string{"--flow=code", "--client-secret-file=foo"})).To(Succeed())
		Expect(runner.inferFlow(context.Background())).To(Succeed())
		Expect(runner.args.flow).To(Equal("code"))
	})
})
