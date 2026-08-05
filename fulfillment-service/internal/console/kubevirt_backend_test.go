/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package console

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KubeVirt Backend", func() {

	Describe("Build", func() {
		It("should fail without logger", func() {
			_, err := NewKubeVirtBackend().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger"))
		})

		It("should fail with negative ping interval", func() {
			_, err := NewKubeVirtBackend().
				SetLogger(logger).
				SetPingConfig(PingConfig{
					PingInterval: -1 * time.Second,
					PongTimeout:  10 * time.Second,
				}).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ping interval"))
		})

		It("should fail with non-positive pong timeout", func() {
			_, err := NewKubeVirtBackend().
				SetLogger(logger).
				SetPingConfig(PingConfig{
					PingInterval: 15 * time.Second,
					PongTimeout:  0,
				}).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("pong timeout"))
		})

		It("should build successfully with all dependencies", func() {
			backend, err := NewKubeVirtBackend().
				SetLogger(logger).
				Build()
			Expect(err).NotTo(HaveOccurred())
			Expect(backend).NotTo(BeNil())
		})
	})
})
