/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("DefaultChecks", func() {
	It("with no services requested, includes only the 'all' entries", func() {
		checks, err := DefaultChecks(CheckOptions{})

		Expect(err).NotTo(HaveOccurred())
		names := checkNames(checks)
		Expect(names).To(ContainElements("cert-manager-crds", "cert-manager-operator", "aap-operator", "default-storageclass", "ocp-version"))
		Expect(names).NotTo(ContainElement("cnv-operator"))
		Expect(names).NotTo(ContainElement("metal3-baremetalhost-crd"))
	})

	It("includes a service's entries only when that service is requested", func() {
		checks, err := DefaultChecks(CheckOptions{Services: []string{"vmaas"}})

		Expect(err).NotTo(HaveOccurred())
		names := checkNames(checks)
		Expect(names).To(ContainElement("cnv-operator"))
		Expect(names).NotTo(ContainElement("mce-operator"))
		Expect(names).NotTo(ContainElement("kafka-operator"))
	})

	It("excludes Metal3 entries unless Metal3 is set, even when bmaas is requested", func() {
		checks, err := DefaultChecks(CheckOptions{Services: []string{"bmaas"}})

		Expect(err).NotTo(HaveOccurred())
		names := checkNames(checks)
		Expect(names).NotTo(ContainElement("metal3-baremetalhost-crd"))
		Expect(names).NotTo(ContainElement("metal3-provisioning-watch-all-namespaces"))
	})

	It("includes the Metal3 entries when both bmaas and Metal3 are set", func() {
		checks, err := DefaultChecks(CheckOptions{Services: []string{"bmaas"}, Metal3: true})

		Expect(err).NotTo(HaveOccurred())
		names := checkNames(checks)
		Expect(names).To(ContainElements("metal3-baremetalhost-crd", "metal3-provisioning-watch-all-namespaces"))
	})

	It("never returns two checks with the same name, for any combination of options", func() {
		optionsToTry := []CheckOptions{
			{},
			{Services: []string{"vmaas", "caas", "bmaas", "maas", "metering"}, Metal3: true},
		}
		for _, opts := range optionsToTry {
			checks, err := DefaultChecks(opts)
			Expect(err).NotTo(HaveOccurred())
			seen := map[string]bool{}
			for _, name := range checkNames(checks) {
				Expect(seen[name]).To(BeFalse(), "duplicate check name %q", name)
				seen[name] = true
			}
		}
	})
})

func checkNames(checks []Check) []string {
	names := make([]string, len(checks))
	for i, check := range checks {
		names[i] = check.Name
	}
	return names
}
