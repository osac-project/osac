/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package network

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = Describe("AddressParser", func() {
	Describe("Creation", func() {
		It("Can be created with all required parameters", func() {
			parser, err := NewAddressParser().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(parser).ToNot(BeNil())
		})

		It("Can't be created without a logger", func() {
			parser, err := NewAddressParser().
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(parser).To(BeNil())
		})
	})

	DescribeTable(
		"Successful parsing",
		func(input string, expectedAddress string, expectedPlaintext bool) {
			parser, err := NewAddressParser().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(parser).ToNot(BeNil())

			address, plaintext, err := parser.Parse(input)
			Expect(err).ToNot(HaveOccurred())
			Expect(address).To(Equal(expectedAddress))
			Expect(plaintext).To(Equal(expectedPlaintext))
		},
		Entry(
			"host:port format",
			"example.com:8080",
			"example.com:8080",
			false,
		),
		Entry(
			"IP:port format",
			"192.168.1.1:443",
			"192.168.1.1:443",
			false,
		),
		Entry(
			"hostname without port (adds default 443)",
			"example.com",
			"example.com:443",
			false,
		),
		Entry(
			"IP address without port (adds default 443)",
			"192.168.1.1",
			"192.168.1.1:443",
			false,
		),
		Entry(
			"http:// URL with port",
			"http://example.com:8080",
			"example.com:8080",
			true,
		),
		Entry(
			"http:// URL without port (uses default 80)",
			"http://example.com",
			"example.com:80",
			true,
		),
		Entry(
			"http:// URL with IP address",
			"http://192.168.1.1:9090",
			"192.168.1.1:9090",
			true,
		),
		Entry(
			"http:// URL with IP and default port",
			"http://192.168.1.1",
			"192.168.1.1:80",
			true,
		),
		Entry(
			"https:// URL with port",
			"https://example.com:8443",
			"example.com:8443",
			false,
		),
		Entry(
			"https:// URL without port (uses default 443)",
			"https://example.com",
			"example.com:443",
			false,
		),
		Entry(
			"https:// URL with IP address",
			"https://192.168.1.1:9443",
			"192.168.1.1:9443",
			false,
		),
		Entry(
			"https:// URL with IP and default port",
			"https://192.168.1.1",
			"192.168.1.1:443",
			false,
		),
	)

	DescribeTable(
		"Parsing errors",
		func(input string, expected string) {
			parser, err := NewAddressParser().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(parser).ToNot(BeNil())
			address, plaintext, err := parser.Parse(input)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expected))
			Expect(address).To(BeEmpty())
			Expect(plaintext).To(BeFalse())
		},
		Entry(
			"ftp:// scheme is not supported",
			"ftp://example.com:21",
			"unsupported scheme 'ftp'",
		),
		Entry(
			"ws:// scheme is not supported",
			"ws://example.com",
			"unsupported scheme 'ws'",
		),
		Entry(
			"wss:// scheme is not supported",
			"wss://example.com",
			"unsupported scheme 'wss'",
		),
	)
})
