/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	windowsUserDataCLISentinel = "OSAC-7712-CLI-SECRET-SENTINEL"
	windowsUserDataMaxBytes    = 65_536
)

var _ = Describe("CLI Windows user-data Secret lifecycle", Label("cli", "secrets"), func() {
	var homeDir string

	BeforeEach(func() {
		var err error
		homeDir, err = tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(homeDir)).To(Succeed())
		})
	})

	DescribeTable("creates, describes, and deletes a Windows user-data Secret", func(ctx context.Context, xmlData []byte) {
		_, stderr, exitCode := tool.LoginCLI(ctx, homeDir, userUsername, usersPassword)
		Expect(exitCode).To(Equal(0), "login failed: %s", stderr)

		name := fmt.Sprintf("it-windows-user-data-%s", uuid.New()[24:32])
		xmlPath := writeWindowsUserDataFile(homeDir, "Unattend.xml", xmlData)

		created := false
		DeferCleanup(func(ctx context.Context) {
			if !created {
				return
			}
			_, cleanupStderr, code := tool.RunCLI(ctx, homeDir, "delete", "secrets", name)
			Expect(code).To(Equal(0), "failed to clean up Secret %q: %s", name, cleanupStderr)
		})

		createdOutput, createStderr, code := runCreateWindowsUserDataCLI(ctx, homeDir, name, "userdata="+xmlPath)
		Expect(code).To(Equal(0), "create failed: %s%s", createdOutput, createStderr)
		created = true

		Expect(createdOutput).To(ContainSubstring(name))
		Expect(createdOutput + createStderr).ToNot(ContainSubstring(windowsUserDataCLISentinel))

		_, idOutput, found := strings.Cut(createdOutput, "(ID: ")
		Expect(found).To(BeTrue(), "create output did not include the Secret ID: %s", createdOutput)
		secretID := strings.TrimRight(strings.TrimSpace(idOutput), ").")
		Expect(secretID).ToNot(BeEmpty())

		client := publicv1.NewSecretsClient(tool.ExternalView().UserConn())
		response, err := client.Get(ctx, publicv1.SecretsGetRequest_builder{
			Id: secretID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetType()).To(Equal(publicv1.SecretType_SECRET_TYPE_WINDOWS_USER_DATA))
		Expect(response.GetObject().GetData()).To(Equal(map[string][]byte{"userdata": xmlData}))

		describeOutput, describeStderr, code := tool.RunCLI(ctx, homeDir, "describe", "secret", name)
		Expect(code).To(Equal(0), "describe failed: %s%s", describeOutput, describeStderr)
		Expect(describeOutput).To(ContainSubstring("Windows User Data"))
		Expect(describeOutput + describeStderr).ToNot(ContainSubstring(windowsUserDataCLISentinel))

		_, deleteStderr, code := tool.RunCLI(ctx, homeDir, "delete", "secrets", name)
		Expect(code).To(Equal(0), "delete failed: %s", deleteStderr)
		deletionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		Eventually(func() codes.Code {
			_, err := client.Get(deletionCtx, publicv1.SecretsGetRequest_builder{Id: secretID}.Build())
			return status.Code(err)
		}).WithContext(deletionCtx).WithTimeout(30 * time.Second).Should(Equal(codes.NotFound))
		created = false
	},
		Entry("nested UTF-8 XML with a BOM and declaration whitespace",
			[]byte("\xef\xbb\xbf<?xml version='1.0' ?><unattend><settings>"+windowsUserDataCLISentinel+"</settings></unattend>")),
		Entry("exactly 64 KiB", []byte("<root>"+windowsUserDataCLISentinel+
			strings.Repeat("a", windowsUserDataMaxBytes-len("<root></root>")-len(windowsUserDataCLISentinel))+"</root>")),
	)

	DescribeTable("rejects invalid Windows user-data Secrets without exposing content",
		func(ctx context.Context, key string, data []byte, wantMessage string) {
			_, stderr, exitCode := tool.LoginCLI(ctx, homeDir, userUsername, usersPassword)
			Expect(exitCode).To(Equal(0), "login failed: %s", stderr)

			name := "it-windows-user-data-invalid-" + uuid.New()[24:32]
			xmlPath := writeWindowsUserDataFile(homeDir, "invalid.xml", data)
			client := publicv1.NewSecretsClient(tool.ExternalView().UserConn())
			filter := fmt.Sprintf("this.metadata.name == %q", name)
			// Clean up even if a regression incorrectly persists the rejected input.
			DeferCleanup(func(ctx context.Context) {
				list, err := client.List(ctx, publicv1.SecretsListRequest_builder{Filter: &filter}.Build())
				Expect(err).ToNot(HaveOccurred())
				for _, secret := range list.GetItems() {
					_, err = client.Delete(ctx, publicv1.SecretsDeleteRequest_builder{Id: secret.GetId()}.Build())
					Expect(err).ToNot(HaveOccurred())
				}
			})

			stdout, stderr, code := runCreateWindowsUserDataCLI(ctx, homeDir, name, key+"="+xmlPath)
			output := stdout + "\n" + stderr
			Expect(code).ToNot(Equal(0), "create unexpectedly succeeded: %s", output)
			Expect(output).To(ContainSubstring(wantMessage))
			Expect(output).ToNot(ContainSubstring(windowsUserDataCLISentinel))

			list, err := client.List(ctx, publicv1.SecretsListRequest_builder{Filter: &filter}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(list.GetItems()).To(BeEmpty(), "rejected Secret was persisted")
		},
		Entry("missing userdata key", "not-userdata", []byte("<root>"+windowsUserDataCLISentinel+"</root>"),
			`requires a non-empty data["userdata"]`),
		Entry("empty userdata", "userdata", []byte{}, `requires a non-empty data["userdata"]`),
		Entry("malformed XML", "userdata", []byte("<root>"+windowsUserDataCLISentinel), "not well-formed XML"),
		Entry("unsupported XML encoding", "userdata",
			[]byte(`<?xml version="1.0" encoding="UTF-16"?><root>`+windowsUserDataCLISentinel+`</root>`), "unsupported encoding"),
		Entry("raw invalid UTF-8", "userdata", []byte("<root>\xff"+windowsUserDataCLISentinel+"</root>"), "unsupported encoding"),
		Entry("DTD", "userdata", []byte("<!DOCTYPE root><root>"+windowsUserDataCLISentinel+"</root>"), "prohibited XML directive"),
		Entry("oversized XML", "userdata",
			[]byte("<root>"+strings.Repeat("a", windowsUserDataMaxBytes)+windowsUserDataCLISentinel+"</root>"),
			"exceeds the maximum of 65536 bytes"),
	)
})

func writeWindowsUserDataFile(homeDir, name string, data []byte) string {
	path := filepath.Join(homeDir, name)
	ExpectWithOffset(1, os.WriteFile(path, data, 0600)).To(Succeed())
	return path
}

func runCreateWindowsUserDataCLI(ctx context.Context, homeDir, name, fromFile string) (string, string, int) {
	return tool.RunCLI(ctx, homeDir,
		"create", "secret",
		"--name", name,
		"--type", "windows-user-data",
		"--from-file", fromFile,
	)
}
