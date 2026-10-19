/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inventory

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
)

var _ = Describe("NetBox inventory client", func() {
	Describe("GetHostLogicalPortMACs", func() {
		It("returns a not-implemented error", func() {
			macs, err := (&NetBoxClient{}).GetHostLogicalPortMACs(context.Background(), "baremetal/netbox-42")

			Expect(errors.Is(err, errNetBoxClientNotImplemented)).To(BeTrue())
			Expect(macs).To(BeNil())
		})
	})

	Describe("ParseNetBoxOptions", func() {
		DescribeTable("rejects invalid options",
			func(options map[string]any, wantErr string) {
				_, err := ParseNetBoxOptions(options)
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
			},
			Entry("missing options", map[string]any{}, "netbox options not found in config"),
			Entry("wrong value type", map[string]any{"netbox": "not-a-map"}, "failed to parse netbox options"),
			Entry("missing URL", map[string]any{"netbox": map[string]any{"tokenFile": "/var/run/secrets/netbox/token"}}, "netbox url is required in config"),
			Entry("missing token file", map[string]any{"netbox": map[string]any{"url": "https://netbox.example.test"}}, "netbox tokenFile is required in config"),
		)

		It("parses valid options", func() {
			options := map[string]any{"netbox": map[string]any{
				"url":               "https://netbox.example.test/",
				"tokenFile":         "/var/run/secrets/netbox/token",
				"caFile":            "/var/run/secrets/netbox/ca.crt",
				"allowInsecureHTTP": true,
			}}

			got, err := ParseNetBoxOptions(options)

			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(&NetBoxClientConfig{
				URL:               "https://netbox.example.test/",
				TokenFile:         "/var/run/secrets/netbox/token",
				CAFile:            "/var/run/secrets/netbox/ca.crt",
				AllowInsecureHTTP: true,
			}))
		})

		It("rejects values that cannot be marshaled", func() {
			_, err := ParseNetBoxOptions(map[string]any{"netbox": make(chan int)})

			Expect(err).To(MatchError(ContainSubstring("failed to parse netbox options")))
		})
	})

	Describe("NewNetBoxClient", func() {
		It("retains the injected API, manager, and host class", func() {
			tokenFile := filepath.Join(GinkgoT().TempDir(), "token")
			Expect(os.WriteFile(tokenFile, []byte("test-token\n"), 0o600)).To(Succeed())

			api, err := NewNetBoxAPI(&Config{Options: map[string]any{"netbox": map[string]any{
				"url":       "https://netbox.example.test/",
				"tokenFile": tokenFile,
			}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(api).NotTo(BeNil())

			manager := baremetalhost.NewManager(nil, nil, "metal3-system")
			client := NewNetBoxClient(api, manager, netBoxHostClass)

			Expect(client.api).To(BeIdenticalTo(api))
			Expect(client.bmhManager).To(BeIdenticalTo(manager))
			Expect(client.bmhManager.Namespace()).To(Equal("metal3-system"))
			Expect(client.hostClass).To(Equal(netBoxHostClass))
		})
	})

	Describe("NewNetBoxAPI", func() {
		DescribeTable("rejects invalid configuration",
			func(config *Config, wantErr string) {
				_, err := NewNetBoxAPI(config)
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
			},
			Entry("nil config", (*Config)(nil), "netbox inventory config is required"),
			Entry("missing NetBox options", &Config{}, "netbox options not found in config"),
			Entry("HTTP URL without opt-in", &Config{Options: map[string]any{"netbox": map[string]any{
				"url":       "http://netbox.example.test",
				"tokenFile": "/unused/token",
			}}}, "NetBox HTTP endpoints require AllowInsecureHTTP"),
		)

		It("rejects an unreadable token file", func() {
			tokenFile := filepath.Join(GinkgoT().TempDir(), "missing-token")
			config := &Config{Options: map[string]any{"netbox": map[string]any{
				"url":       "https://netbox.example.test",
				"tokenFile": tokenFile,
			}}}

			_, err := NewNetBoxAPI(config)

			Expect(err).To(MatchError(ContainSubstring("token file is missing or invalid")))
		})
	})

	Describe("stub methods", func() {
		It("return not-implemented errors", func() {
			client := &NetBoxClient{}
			ctx := context.Background()

			host, err := client.FindFreeHost(ctx, map[string]string{"region": "lab"})
			Expect(host).To(BeNil())
			Expect(errors.Is(err, errNetBoxClientNotImplemented)).To(BeTrue())

			host, err = client.AssignHost(ctx, "baremetal/netbox-42", "provider-42", map[string]string{"region": "lab"})
			Expect(host).To(BeNil())
			Expect(errors.Is(err, errNetBoxClientNotImplemented)).To(BeTrue())

			err = client.UnassignHost(ctx, "baremetal/netbox-42", []string{"192.0.2.42"})
			Expect(errors.Is(err, errNetBoxClientNotImplemented)).To(BeTrue())

			nics, err := client.GetHostNICs(ctx, "baremetal/netbox-42")
			Expect(nics).To(BeNil())
			Expect(errors.Is(err, errNetBoxClientNotImplemented)).To(BeTrue())
		})
	})
})
