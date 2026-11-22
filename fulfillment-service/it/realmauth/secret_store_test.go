/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package realmauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type secretStoreFixture struct {
	path       string
	clients    map[string]string
	realmAdmin string
}

func newSecretStoreFixture(dir string) secretStoreFixture {
	GinkgoHelper()
	// Preserve the existing shared-secret behavior while establishing regressions.
	shared := rand.Text()
	fixture := secretStoreFixture{
		path: filepath.Join(dir, "oc"),
		clients: map[string]string{
			"osac-controller":   shared,
			"osac-admin":        shared,
			"osac-csi-driver":   shared,
			"osac-ui-backend":   shared,
			"osac-mcp-exchange": shared,
		},
		realmAdmin: shared,
	}
	Expect(os.WriteFile(fixture.path, []byte(secretStore), 0700)).To(Succeed())
	return fixture
}

func (f secretStoreFixture) environment() []string {
	return append(os.Environ(), "REALM_TEST_SECRET="+f.clients["osac-csi-driver"])
}

func (f secretStoreFixture) command(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, f.path, args...)
	command.Env = f.environment()
	return command
}

func (f secretStoreFixture) read(ctx context.Context, clientID string) string {
	GinkgoHelper()
	output, err := f.command(ctx, "get", "secret", "keycloak-client-secrets", "-n", "keycloak",
		"-o", "jsonpath={.data."+clientID+"}").Output()
	// Never let an assertion format command output, credentials or the environment.
	if err != nil {
		Fail("the known client-secret lookup must succeed (output withheld)")
	}
	decoded, err := base64.StdEncoding.DecodeString(string(output))
	if err != nil {
		Fail("the secret-store output must be base64 (value withheld)")
	}
	return string(decoded)
}

var _ = Describe("Realm secret-store fixture", func() {
	var fixture secretStoreFixture
	BeforeEach(func() {
		fixture = newSecretStoreFixture(GinkgoT().TempDir())
	})

	It("does not confuse the controller and CSI lookup values", func(ctx SpecContext) {
		controller := fixture.read(ctx, "osac-controller")
		csi := fixture.read(ctx, "osac-csi-driver")
		distinct := controller != csi
		Expect(distinct).To(BeTrue(), "controller and CSI lookups must return distinct secrets")
	})

	It("keeps all client credentials separate from each other and the realm administrator", func(ctx SpecContext) {
		seen := map[string]bool{fixture.realmAdmin: true}
		for _, clientID := range []string{
			"osac-controller", "osac-admin", "osac-csi-driver", "osac-ui-backend", "osac-mcp-exchange",
		} {
			value := fixture.read(ctx, clientID)
			Expect(seen[value]).To(BeFalse(), "each client and the realm administrator need independent credentials")
			seen[value] = true
		}
	})

	DescribeTable("supports the real resolver existence probes", func(ctx SpecContext, args []string) {
		succeeded := fixture.command(ctx, args...).Run() == nil
		Expect(succeeded).To(BeTrue())
	},
		Entry("prerequisites", []string{"get", "secret", "keycloak-client-secrets"}),
		Entry("Helm", []string{"get", "secret", "keycloak-client-secrets", "-n", "keycloak"}),
	)

	DescribeTable("returns the configured value for a supported key", func(ctx SpecContext, clientID string) {
		value := fixture.read(ctx, clientID)
		Expect(value != "" && value == fixture.clients[clientID]).To(BeTrue(), "lookup must match its own seeded value")
	},
		Entry("controller", "osac-controller"),
		Entry("administrator", "osac-admin"),
		Entry("CSI", "osac-csi-driver"),
		Entry("UI backend", "osac-ui-backend"),
		Entry("MCP key checked by the Helm resolver", "osac-mcp-exchange"),
	)

	DescribeTable("rejects unsupported invocations", func(ctx SpecContext, args []string) {
		rejected := fixture.command(ctx, args...).Run() != nil
		Expect(rejected).To(BeTrue(), "unsupported secret-store invocation must fail")
	},
		Entry("unknown JSONPath key", []string{
			"get", "secret", "keycloak-client-secrets", "-o", "jsonpath={.data.unknown}",
		}),
		Entry("different Secret with the same prefix", []string{
			"get", "secret", "keycloak-client-secrets-backup", "-o", "jsonpath={.data.osac-csi-driver}",
		}),
		Entry("unsupported output operation", []string{"get", "secret", "keycloak-client-secrets", "-o", "yaml"}),
		Entry("extra argument", []string{
			"get", "secret", "keycloak-client-secrets", "-o", "jsonpath={.data.osac-csi-driver}", "extra",
		}),
		Entry("different namespace", []string{
			"get", "secret", "keycloak-client-secrets", "-n", "other", "-o", "jsonpath={.data.osac-csi-driver}",
		}),
		Entry("joined arguments", []string{"get secret keycloak-client-secrets"}),
		Entry("mutation", []string{"delete", "secret", "keycloak-client-secrets"}),
	)
})
