/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vault

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
)

var _ = Describe("Vault flags", func() {
	Describe("BaseConfigFromFlags", func() {
		It("reads registered flags with defaults", func() {
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddBaseFlags(flags)

			cfg, err := BaseConfigFromFlags(flags)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Endpoint).To(Equal(""))
			Expect(cfg.Namespace).To(Equal("osac"))
			Expect(cfg.KVMountPath).To(Equal("secret"))
		})

		It("reads overridden flag values", func() {
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddBaseFlags(flags)
			Expect(flags.Set("vault-endpoint", "https://vault.example.com")).To(Succeed())
			Expect(flags.Set("vault-namespace", "custom")).To(Succeed())

			cfg, err := BaseConfigFromFlags(flags)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Endpoint).To(Equal("https://vault.example.com"))
			Expect(cfg.Namespace).To(Equal("custom"))
		})
	})

	Describe("LifecycleConfigFromFlags", func() {
		It("reads registered flags with defaults", func() {
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddLifecycleFlags(flags)

			cfg, err := LifecycleConfigFromFlags(flags)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.MountPath).To(Equal("jwt"))
			Expect(cfg.KeycloakAudience).To(Equal("osac-api"))
			Expect(cfg.Role).To(Equal(""))
		})
	})

	Describe("ValidateLifecycleConfig", func() {
		It("returns nil when all required fields are set", func() {
			cfg := LifecycleConfig{
				Role:                     "lifecycle",
				KeycloakIssuerURL:        "https://kc/realms/osac",
				KeycloakTokenEndpoint:    "https://kc/realms/osac/protocol/openid-connect/token",
				KeycloakClientID:         "vault-client",
				KeycloakClientSecretFile: "/etc/secret",
			}
			Expect(ValidateLifecycleConfig(cfg)).To(Succeed())
		})

		It("returns error when required fields are missing", func() {
			Expect(ValidateLifecycleConfig(LifecycleConfig{})).To(HaveOccurred())
		})
	})
})
