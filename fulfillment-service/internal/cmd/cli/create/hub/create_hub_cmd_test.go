/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package hub

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Create hub", func() {
	It("registers --kubeconfig as a secret name flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("kubeconfig")

		Expect(flag).NotTo(BeNil())
		Expect(flag.DefValue).To(BeEmpty())
		Expect(flag.Usage).To(ContainSubstring("Secret resource"))
	})

	It("builds a kubeconfig secret reference without inline credentials", func() {
		runner := &runnerContext{
			id:         "test-hub",
			kubeconfig: "hub-kubeconfig",
			namespace:  "clusters",
		}

		hub := runner.hub()

		Expect(hub.GetId()).To(Equal("test-hub"))
		Expect(hub.GetSpec().GetNamespace()).To(Equal("clusters"))
		Expect(hub.GetSpec().GetKubeconfigSecret().GetName()).To(Equal("hub-kubeconfig"))
		Expect(hub.GetSpec().GetKubeconfig()).To(BeEmpty())
	})
})
