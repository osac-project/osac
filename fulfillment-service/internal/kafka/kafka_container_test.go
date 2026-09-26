/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
	"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package kafka

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Builder", func() {
	It("Should return an error if logger is not set", func() {
		_, err := NewContainer().Build()
		Expect(err).To(MatchError("logger is mandatory"))
	})
})

var _ = Describe("Container", func() {
	It("Starts a broker reachable on the published host port", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		container, err := NewContainer().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		err = container.Start(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
			defer stopCancel()
			Expect(container.Stop(stopCtx)).To(Succeed())
		})

		Expect(container.Brokers()).To(MatchRegexp(`^127\.0\.0\.1:\d+$`))

		client, err := container.Client()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(client.Close)
		Expect(client.Brokers()).ToNot(BeEmpty())
		_, err = client.Topics()
		Expect(err).ToNot(HaveOccurred())
	})
})
