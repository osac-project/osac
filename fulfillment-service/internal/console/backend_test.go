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
	"bytes"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Target", func() {
	var (
		target Target
		token  string
	)

	BeforeEach(func() {
		token = "super-secret-bearer-token-value"
		target = Target{
			ResourceType: "compute_instance",
			BackendURI:   "wss://hub-1.example.com/api/v1/vnc",
			BackendToken: token,
		}
	})

	Describe("LogValue", func() {
		It("should not contain the backend token", func() {
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, nil)
			log := slog.New(handler)

			log.Info("test", slog.Any("target", target))
			output := buf.String()

			Expect(output).NotTo(ContainSubstring(token))
		})

		It("should not contain the backend_token key", func() {
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, nil)
			log := slog.New(handler)

			log.Info("test", slog.Any("target", target))
			output := buf.String()

			Expect(output).NotTo(ContainSubstring("backend_token"))
		})

		It("should contain all safe fields", func() {
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, nil)
			log := slog.New(handler)

			log.Info("test", slog.Any("target", target))
			output := buf.String()

			Expect(output).To(ContainSubstring("compute_instance"))
			Expect(output).To(ContainSubstring("wss://hub-1.example.com/api/v1/vnc"))
		})

		It("should not contain the token when BackendURI is empty", func() {
			target.BackendURI = ""
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, nil)
			log := slog.New(handler)

			log.Info("test", slog.Any("target", target))
			output := buf.String()

			Expect(output).NotTo(ContainSubstring(token))
		})
	})
})
