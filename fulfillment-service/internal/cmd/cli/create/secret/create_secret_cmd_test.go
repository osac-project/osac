/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package secret

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("parseSecretType", func() {
	DescribeTable("valid types",
		func(value string, expected publicv1.SecretType) {
			actual, err := parseSecretType(value)
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(Equal(expected))
		},
		Entry("opaque", "opaque", publicv1.SecretType_SECRET_TYPE_OPAQUE),
		Entry("pull secret", "pull-secret", publicv1.SecretType_SECRET_TYPE_PULL_SECRET),
		Entry("kubeconfig", "kubeconfig", publicv1.SecretType_SECRET_TYPE_KUBECONFIG),
		Entry("user data", "user-data", publicv1.SecretType_SECRET_TYPE_USER_DATA),
		Entry("value", "value", publicv1.SecretType_SECRET_TYPE_VALUE),
	)

	It("rejects an unknown type", func() {
		_, err := parseSecretType("tls")
		Expect(err).To(MatchError(ContainSubstring("invalid secret type")))
	})
})

var _ = Describe("parseFromFileSpec", func() {
	DescribeTable("valid cases",
		func(value, expectedKey, expectedPath string) {
			key, path, err := parseFromFileSpec(value)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(Equal(expectedKey))
			Expect(path).To(Equal(expectedPath))
		},
		Entry("explicit key and path",
			"tls.crt=/path/to/cert.pem", "tls.crt", "/path/to/cert.pem",
		),
		Entry("path only, basename as key",
			"/path/to/config.yaml", "config.yaml", "/path/to/config.yaml",
		),
		Entry("relative path, basename as key",
			"data.txt", "data.txt", "data.txt",
		),
		Entry("stdin with explicit key",
			"password=-", "password", "-",
		),
		Entry("key with path containing equals",
			"mykey=path/with=equals", "mykey", "path/with=equals",
		),
	)

	DescribeTable("error cases",
		func(value string, errMatcher OmegaMatcher) {
			_, _, err := parseFromFileSpec(value)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(errMatcher)
		},
		Entry("empty value",
			"", ContainSubstring("must not be empty"),
		),
		Entry("empty key with path",
			"=/path/to/file", ContainSubstring("key must not be empty"),
		),
		Entry("key with empty path",
			"mykey=", ContainSubstring("path must not be empty"),
		),
		Entry("stdin without explicit key",
			"-", ContainSubstring("key is required when reading from stdin"),
		),
	)
})

var _ = Describe("parseFromFileFlags", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "secret-test-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			os.RemoveAll(tmpDir)
		})
	})

	It("should read a single file with explicit key", func() {
		filePath := filepath.Join(tmpDir, "cert.pem")
		Expect(os.WriteFile(filePath, []byte("cert-data"), 0644)).To(Succeed())

		data, err := parseFromFileFlags([]string{"tls.crt=" + filePath})
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(1))
		Expect(data["tls.crt"]).To(Equal([]byte("cert-data")))
	})

	It("should read a file using basename as key", func() {
		filePath := filepath.Join(tmpDir, "config.yaml")
		Expect(os.WriteFile(filePath, []byte("config-content"), 0644)).To(Succeed())

		data, err := parseFromFileFlags([]string{filePath})
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(1))
		Expect(data["config.yaml"]).To(Equal([]byte("config-content")))
	})

	It("should read multiple files", func() {
		file1 := filepath.Join(tmpDir, "cert.pem")
		file2 := filepath.Join(tmpDir, "key.pem")
		Expect(os.WriteFile(file1, []byte("cert"), 0644)).To(Succeed())
		Expect(os.WriteFile(file2, []byte("key"), 0644)).To(Succeed())

		data, err := parseFromFileFlags([]string{"tls.crt=" + file1, "tls.key=" + file2})
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(HaveLen(2))
		Expect(data["tls.crt"]).To(Equal([]byte("cert")))
		Expect(data["tls.key"]).To(Equal([]byte("key")))
	})

	It("should read binary content as raw bytes", func() {
		filePath := filepath.Join(tmpDir, "binary.bin")
		binaryData := []byte{0x00, 0x01, 0xFF, 0xFE, 0x80}
		Expect(os.WriteFile(filePath, binaryData, 0644)).To(Succeed())

		data, err := parseFromFileFlags([]string{"binary=" + filePath})
		Expect(err).NotTo(HaveOccurred())
		Expect(data["binary"]).To(Equal(binaryData))
	})

	It("should read an empty file", func() {
		filePath := filepath.Join(tmpDir, "empty")
		Expect(os.WriteFile(filePath, []byte{}, 0644)).To(Succeed())

		data, err := parseFromFileFlags([]string{"empty=" + filePath})
		Expect(err).NotTo(HaveOccurred())
		Expect(data["empty"]).To(Equal([]byte{}))
	})

	It("should reject duplicate keys", func() {
		file1 := filepath.Join(tmpDir, "a.txt")
		file2 := filepath.Join(tmpDir, "b.txt")
		Expect(os.WriteFile(file1, []byte("a"), 0644)).To(Succeed())
		Expect(os.WriteFile(file2, []byte("b"), 0644)).To(Succeed())

		_, err := parseFromFileFlags([]string{"key=" + file1, "key=" + file2})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("duplicate key"))
	})

	It("should reject multiple stdin references", func() {
		_, err := parseFromFileFlags([]string{"a=-", "b=-"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("stdin (-) can only be referenced once"))
	})

	It("should return error for nonexistent file", func() {
		_, err := parseFromFileFlags([]string{"key=/nonexistent/file"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to read"))
	})
})

var _ = Describe("parseLabels", func() {
	DescribeTable("valid cases",
		func(labels []string, expected map[string]string) {
			result, err := parseLabels(labels)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(expected))
		},
		Entry("nil input returns nil",
			[]string(nil), nil,
		),
		Entry("empty input returns nil",
			[]string{}, nil,
		),
		Entry("single label",
			[]string{"env=prod"}, map[string]string{"env": "prod"},
		),
		Entry("multiple labels",
			[]string{"env=prod", "team=backend"},
			map[string]string{"env": "prod", "team": "backend"},
		),
		Entry("value containing equals",
			[]string{"config=key=value"},
			map[string]string{"config": "key=value"},
		),
		Entry("empty value is valid",
			[]string{"key="},
			map[string]string{"key": ""},
		),
	)

	DescribeTable("error cases",
		func(labels []string, errMatcher OmegaMatcher) {
			_, err := parseLabels(labels)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(errMatcher)
		},
		Entry("missing equals sign",
			[]string{"noequalssign"},
			ContainSubstring("must be in key=value format"),
		),
		Entry("empty key",
			[]string{"=value"},
			ContainSubstring("key must not be empty"),
		),
		Entry("duplicate keys",
			[]string{"env=prod", "env=staging"},
			ContainSubstring("duplicate label key"),
		),
	)
})

var _ = Describe("Command registration", func() {
	It("should create the command", func() {
		cmd := Cmd()
		Expect(cmd).NotTo(BeNil())
		Expect(cmd.Name()).To(Equal("secret"))
	})

	It("should register the --name flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("name")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Shorthand).To(Equal("n"))
	})

	It("should register the --from-file flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("from-file")
		Expect(flag).NotTo(BeNil())
	})

	It("should register the --label flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("label")
		Expect(flag).NotTo(BeNil())
	})

	It("should register the --type flag with the opaque default", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("type")
		Expect(flag).NotTo(BeNil())
		Expect(flag.DefValue).To(Equal("opaque"))
	})

	It("should have the protobuf alias", func() {
		cmd := Cmd()
		Expect(cmd.Aliases).To(ContainElement("osac.public.v1.Secret"))
	})
})
