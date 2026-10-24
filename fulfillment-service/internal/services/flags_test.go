/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package services

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
)

var _ = Describe("Service flags", func() {
	Describe("EnableAllIfNoneSet", func() {
		DescribeTable("enables all services when none are set",
			func(initial, expected Flags) {
				initial.EnableAllIfNoneSet(nil)
				Expect(initial).To(Equal(expected))
			},
			Entry("all false enables all",
				Flags{},
				Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true},
			),
			Entry("one set preserves explicit flags",
				Flags{CaaS: true, VMaaS: true},
				Flags{CaaS: true, VMaaS: true, BMaaS: false, MaaS: false},
			),
			Entry("only BMaaS set preserves",
				Flags{BMaaS: true},
				Flags{CaaS: false, VMaaS: false, BMaaS: true, MaaS: false},
			),
			Entry("all already set remains unchanged",
				Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true},
				Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true},
			),
		)
	})

	Describe("Validate", func() {
		DescribeTable("validates service flag combinations",
			func(flags Flags, valid bool, errSubstring string) {
				err := flags.Validate()
				if valid {
					Expect(err).ToNot(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred())
					if errSubstring != "" {
						Expect(err.Error()).To(ContainSubstring(errSubstring))
					}
				}
			},
			Entry("all enabled is valid",
				Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true}, true, "",
			),
			Entry("CaaS with VMaaS is valid",
				Flags{CaaS: true, VMaaS: true}, true, "",
			),
			Entry("CaaS with BMaaS is valid",
				Flags{CaaS: true, BMaaS: true}, true, "",
			),
			Entry("VMaaS only is valid",
				Flags{VMaaS: true}, true, "",
			),
			Entry("all disabled is invalid",
				Flags{}, false, "at least one of CaaS, VMaaS, BMaaS, or MaaS must be enabled",
			),
			Entry("BMaaS only is valid",
				Flags{BMaaS: true}, true, "",
			),
			Entry("CaaS without VMaaS or BMaaS is invalid",
				Flags{CaaS: true}, false, "CaaS requires at least one of VMaaS or BMaaS",
			),
			Entry("CaaS with MaaS but no VMaaS or BMaaS is invalid",
				Flags{CaaS: true, MaaS: true}, false, "CaaS requires at least one of VMaaS or BMaaS",
			),
			Entry("MaaS without CaaS is invalid",
				Flags{MaaS: true, VMaaS: true}, false, "MaaS requires CaaS",
			),
			Entry("MaaS alone is invalid",
				Flags{MaaS: true}, false, "",
			),
		)

		It("validates after EnableAllIfNoneSet", func() {
			f := Flags{}
			f.EnableAllIfNoneSet(nil)
			Expect(f.Validate()).ToNot(HaveOccurred())
		})
	})

	Describe("RegisterFlags", func() {
		It("registers and parses flags correctly", func() {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags := RegisterFlags(fs)

			Expect(fs.Parse([]string{"--enable-caas", "--enable-vmaas"})).To(Succeed())
			Expect(flags.CaaS).To(BeTrue())
			Expect(flags.VMaaS).To(BeTrue())
			Expect(flags.BMaaS).To(BeFalse())
			Expect(flags.MaaS).To(BeFalse())
		})

		It("defaults all flags to false", func() {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags := RegisterFlags(fs)

			Expect(fs.Parse([]string{})).To(Succeed())
			Expect(flags.CaaS).To(BeFalse())
			Expect(flags.VMaaS).To(BeFalse())
			Expect(flags.BMaaS).To(BeFalse())
			Expect(flags.MaaS).To(BeFalse())
		})

		It("allows all flags to be set", func() {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags := RegisterFlags(fs)

			Expect(fs.Parse([]string{
				"--enable-caas", "--enable-vmaas", "--enable-bmaas", "--enable-maas",
			})).To(Succeed())
			Expect(flags.CaaS).To(BeTrue())
			Expect(flags.VMaaS).To(BeTrue())
			Expect(flags.BMaaS).To(BeTrue())
			Expect(flags.MaaS).To(BeTrue())
		})

		It("rejects an explicit all-false configuration", func() {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags := RegisterFlags(fs)

			Expect(fs.Parse([]string{
				"--enable-caas=false", "--enable-vmaas=false",
				"--enable-bmaas=false", "--enable-maas=false",
			})).To(Succeed())
			flags.EnableAllIfNoneSet(fs)
			Expect(*flags).To(Equal(Flags{}))
			Expect(flags.Validate()).To(MatchError(
				"at least one of CaaS, VMaaS, BMaaS, or MaaS must be enabled"))
		})
	})

	Describe("EnableAllIfNoneSet with a flag set", func() {
		It("enables all services when no flag was supplied", func() {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags := RegisterFlags(fs)

			Expect(fs.Parse([]string{})).To(Succeed())
			flags.EnableAllIfNoneSet(fs)
			Expect(*flags).To(Equal(Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true}))
		})

		It("preserves an explicit all-false configuration", func() {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags := RegisterFlags(fs)

			Expect(fs.Parse([]string{
				"--enable-caas=false", "--enable-vmaas=false",
				"--enable-bmaas=false", "--enable-maas=false",
			})).To(Succeed())
			flags.EnableAllIfNoneSet(fs)
			Expect(*flags).To(Equal(Flags{}))
		})

		It("preserves a single explicit false flag without defaulting", func() {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags := RegisterFlags(fs)

			Expect(fs.Parse([]string{"--enable-vmaas=false"})).To(Succeed())
			flags.EnableAllIfNoneSet(fs)
			Expect(*flags).To(Equal(Flags{}))
		})

		It("preserves explicit true flags", func() {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags := RegisterFlags(fs)

			Expect(fs.Parse([]string{"--enable-caas", "--enable-vmaas"})).To(Succeed())
			flags.EnableAllIfNoneSet(fs)
			Expect(*flags).To(Equal(Flags{CaaS: true, VMaaS: true}))
		})
	})

	Describe("EnabledServices", func() {
		DescribeTable("returns enabled service names",
			func(flags Flags, expected []string) {
				Expect(flags.EnabledServices()).To(Equal(expected))
			},
			Entry("all enabled",
				Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true},
				[]string{"caas", "vmaas", "bmaas", "maas"},
			),
			Entry("none enabled",
				Flags{},
				[]string{},
			),
			Entry("partial",
				Flags{CaaS: true, BMaaS: true},
				[]string{"caas", "bmaas"},
			),
		)
	})
})
