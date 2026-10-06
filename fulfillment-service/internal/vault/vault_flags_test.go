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
	. "github.com/onsi/ginkgo/v2"
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
			Expect(cfg.KeycloakAudience).To(Equal("osac-api"))
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
			Expect(cfg.Role).To(Equal(""))
		})
	})

	Describe("ValidateBaseKeycloakConfig", func() {
		It("returns nil when all required fields are set", func() {
			cfg := BaseConfig{
				Endpoint:                 "https://vault.example.com",
				KeycloakIssuerURL:        "https://kc/realms/osac",
				KeycloakClientID:         "vault-client",
				KeycloakClientSecretFile: "/etc/secret",
			}
			Expect(ValidateBaseKeycloakConfig(cfg)).To(Succeed())
		})

		It("returns error when issuer URL is missing", func() {
			cfg := BaseConfig{
				KeycloakClientID:         "vault-client",
				KeycloakClientSecretFile: "/etc/secret",
			}
			Expect(ValidateBaseKeycloakConfig(cfg)).To(HaveOccurred())
		})

		It("returns error when client ID is missing", func() {
			cfg := BaseConfig{
				KeycloakIssuerURL:        "https://kc/realms/osac",
				KeycloakClientSecretFile: "/etc/secret",
			}
			Expect(ValidateBaseKeycloakConfig(cfg)).To(HaveOccurred())
		})

		It("returns error when client secret file is missing", func() {
			cfg := BaseConfig{
				KeycloakIssuerURL: "https://kc/realms/osac",
				KeycloakClientID:  "vault-client",
			}
			Expect(ValidateBaseKeycloakConfig(cfg)).To(HaveOccurred())
		})
	})

	Describe("ValidateBaseConfig", func() {
		valid := BaseConfig{
			Endpoint:                 "https://vault.example.com",
			Namespace:                "osac",
			KVMountPath:              "secret",
			KeycloakIssuerURL:        "https://kc/realms/osac",
			KeycloakClientID:         "vault-client",
			KeycloakClientSecretFile: "/etc/secret",
		}

		It("returns nil when all required fields are set", func() {
			Expect(ValidateBaseConfig(valid)).To(Succeed())
		})

		DescribeTable("rejects missing required fields", func(update func(*BaseConfig), message string) {
			cfg := valid
			update(&cfg)
			Expect(ValidateBaseConfig(cfg)).To(MatchError(ContainSubstring(message)))
		},
			Entry("endpoint", func(cfg *BaseConfig) { cfg.Endpoint = "" }, "--vault-endpoint"),
			Entry("namespace", func(cfg *BaseConfig) { cfg.Namespace = "" }, "--vault-namespace"),
			Entry("KV mount path", func(cfg *BaseConfig) { cfg.KVMountPath = "" }, "--vault-kv-mount-path"),
			Entry("issuer URL", func(cfg *BaseConfig) { cfg.KeycloakIssuerURL = "" }, "--vault-keycloak-issuer-url"),
			Entry("client ID", func(cfg *BaseConfig) { cfg.KeycloakClientID = "" }, "--vault-keycloak-client-id"),
			Entry("client secret file", func(cfg *BaseConfig) { cfg.KeycloakClientSecretFile = "" }, "--vault-keycloak-client-secret-file"),
		)
	})

	Describe("ValidateLifecycleConfig", func() {
		It("returns nil when all required fields are set", func() {
			cfg := LifecycleConfig{
				Role: "lifecycle",
			}
			Expect(ValidateLifecycleConfig(cfg)).To(Succeed())
		})

		It("returns error when required fields are missing", func() {
			Expect(ValidateLifecycleConfig(LifecycleConfig{})).To(HaveOccurred())
		})
	})
})
