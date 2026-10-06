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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("CLI SSH public key Secret lifecycle", Label("cli", "secrets"), func() {
	var homeDir string

	BeforeEach(func() {
		var err error
		homeDir, err = tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(homeDir)).To(Succeed())
		})
	})

	It("creates, gets, and deletes an SSH public key Secret", func(ctx context.Context) {
		_, stderr, exitCode := tool.LoginCLI(ctx, homeDir, userUsername, usersPassword)
		Expect(exitCode).To(Equal(0), "login failed: %s", stderr)

		name := fmt.Sprintf("it-ssh-public-key-%s", uuid.New()[24:32])
		keyPath := filepath.Join(homeDir, "id_ed25519.pub")
		publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"
		Expect(os.WriteFile(keyPath, []byte(publicKey), 0600)).To(Succeed())

		created := false
		DeferCleanup(func(ctx context.Context) {
			if !created {
				return
			}
			_, cleanupStderr, code := tool.RunCLI(ctx, homeDir, "delete", "secrets", name)
			Expect(code).To(Equal(0), "failed to clean up Secret %q: %s", name, cleanupStderr)
		})

		createdOutput, createStderr, code := tool.RunCLI(ctx, homeDir,
			"create", "secret",
			"--name", name,
			"--type", "ssh-public-key",
			"--from-file", "public_key="+keyPath,
		)
		Expect(code).To(Equal(0), "create failed: %s%s", createdOutput, createStderr)
		created = true

		_, idOutput, found := strings.Cut(createdOutput, "(ID: ")
		Expect(found).To(BeTrue(), "create output did not include the Secret ID: %s", createdOutput)
		secretID := strings.TrimRight(strings.TrimSpace(idOutput), ").")
		Expect(secretID).ToNot(BeEmpty())

		getOutput, getStderr, code := tool.RunCLI(ctx, homeDir, "get", "secrets", name)
		Expect(code).To(Equal(0), "get failed: %s%s", getOutput, getStderr)
		Expect(getOutput).To(ContainSubstring(name))
		Expect(getOutput).To(ContainSubstring(secretID))

		_, deleteStderr, code := tool.RunCLI(ctx, homeDir, "delete", "secrets", name)
		Expect(code).To(Equal(0), "delete failed: %s", deleteStderr)
		created = false

		getOutput, getStderr, code = tool.RunCLI(ctx, homeDir, "get", "secrets", name)
		Expect(code).To(Equal(0), "get after delete failed: %s", getStderr)
		Expect(getOutput).To(ContainSubstring("There are no objects matching the given criteria."))
	})
})
