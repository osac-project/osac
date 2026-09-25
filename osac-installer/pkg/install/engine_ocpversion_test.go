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

var _ = Describe("buildOCPVersionRangeCheck", func() {
	entry := matrixEntry{ID: "ocp-version", Kind: "ocp-version-range", Severity: "warning", Category: "resource", MinVersion: "4.22.4"}

	It("rejects an entry with no minVersion", func() {
		_, err := buildOCPVersionRangeCheck(matrixEntry{ID: "bad", Severity: "warning"})
		Expect(err).To(HaveOccurred())
	})

	It("rejects an entry with an unparseable minVersion", func() {
		_, err := buildOCPVersionRangeCheck(matrixEntry{
			ID: "bad", Severity: "warning", Category: "resource", MinVersion: "not-a-version",
		})
		Expect(err).To(HaveOccurred())
	})

	It("passes when the cluster is at or above minVersion", func() {
		check, err := buildOCPVersionRangeCheck(entry)
		Expect(err).NotTo(HaveOccurred())
		clients := newFakeClients(nil, []runtime.Object{newUnstructuredClusterVersion("4.22.6")})

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Pass))
		Expect(message).To(ContainSubstring("4.22.6"))
	})

	It("fails when the cluster is older than minVersion", func() {
		check, _ := buildOCPVersionRangeCheck(entry)
		clients := newFakeClients(nil, []runtime.Object{newUnstructuredClusterVersion("4.18.1")})

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("older than"))
	})

	It("fails when the ClusterVersion resource can't be read", func() {
		check, _ := buildOCPVersionRangeCheck(entry)
		clients := newFakeClients(nil, nil)

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).NotTo(BeEmpty())
	})
})
