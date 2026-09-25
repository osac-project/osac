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
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("buildCSVSucceededCheck", func() {
	entry := matrixEntry{
		ID:            "cnv-operator",
		Kind:          "csv-succeeded",
		Severity:      "required",
		Category:      "operator",
		DisplayName:   "OpenShift Virtualization",
		Namespace:     "openshift-cnv",
		PackagePrefix: "kubevirt-hyperconverged",
		MinVersion:    "4.22.0",
	}

	It("rejects an entry with no namespace/packagePrefix", func() {
		_, err := buildCSVSucceededCheck(matrixEntry{ID: "bad", Severity: "required"})
		Expect(err).To(HaveOccurred())
	})

	It("rejects an entry with an unparseable minVersion", func() {
		_, err := buildCSVSucceededCheck(matrixEntry{
			ID: "bad", Severity: "required", Category: "operator",
			Namespace: "ns", PackagePrefix: "pkg", MinVersion: "not-a-version",
		})
		Expect(err).To(HaveOccurred())
	})

	It("passes when a matching CSV has reached Succeeded at or above minVersion", func() {
		check, err := buildCSVSucceededCheck(entry)
		Expect(err).NotTo(HaveOccurred())
		clients := newFakeClients(nil, []runtime.Object{
			newUnstructuredCSV("openshift-cnv", "kubevirt-hyperconverged.v4.22.6", "Succeeded", "4.22.6"),
		})

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Pass))
		Expect(message).To(ContainSubstring("kubevirt-hyperconverged.v4.22.6"))
	})

	It("fails when no CSV for the package exists in the namespace", func() {
		check, _ := buildCSVSucceededCheck(entry)
		clients := newFakeClients(nil, nil)

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("not installed"))
	})

	It("fails when the CSV exists but hasn't reached Succeeded", func() {
		check, _ := buildCSVSucceededCheck(entry)
		clients := newFakeClients(nil, []runtime.Object{
			newUnstructuredCSV("openshift-cnv", "kubevirt-hyperconverged.v4.22.6", "Installing", "4.22.6"),
		})

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("not ready"))
	})

	It("fails when the installed version is older than minVersion", func() {
		check, _ := buildCSVSucceededCheck(entry)
		clients := newFakeClients(nil, []runtime.Object{
			newUnstructuredCSV("openshift-cnv", "kubevirt-hyperconverged.v4.20.0", "Succeeded", "4.20.0"),
		})

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("older than the minimum"))
	})

	It("ignores CSVs in other namespaces", func() {
		check, _ := buildCSVSucceededCheck(entry)
		clients := newFakeClients(nil, []runtime.Object{
			newUnstructuredCSV("other-namespace", "kubevirt-hyperconverged.v4.22.6", "Succeeded", "4.22.6"),
		})

		status, _ := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
	})

	It("passes with no minVersion set, regardless of spec.version", func() {
		check, err := buildCSVSucceededCheck(matrixEntry{
			ID: "no-min", Severity: "required", Category: "operator", Namespace: "ns", PackagePrefix: "pkg",
		})
		Expect(err).NotTo(HaveOccurred())
		clients := newFakeClients(nil, []runtime.Object{
			newUnstructuredCSV("ns", "pkg.v0.0.1", "Succeeded", "not-a-semver-either"),
		})

		status, _ := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Pass))
	})
})
