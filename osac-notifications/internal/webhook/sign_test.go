/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package webhook

import (
	"encoding/base64"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SignHeaders", func() {
	It("emits Standard Webhooks headers", func() {
		secret := "whsec_" + base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
		headers, err := SignHeaders("msg_1", secret, time.Unix(1_700_000_000, 0).UTC(), []byte(`{"a":1}`))
		Expect(err).ToNot(HaveOccurred())
		Expect(headers["webhook-id"]).To(Equal("msg_1"))
		Expect(headers["webhook-timestamp"]).To(Equal("1700000000"))
		Expect(headers["webhook-signature"]).To(HavePrefix("v1,"))
	})
})

var _ = Describe("Retry helpers", func() {
	It("follows the designed delay schedule", func() {
		Expect(NextAttemptDelay(1)).To(Equal(5 * time.Second))
		Expect(NextAttemptDelay(2)).To(Equal(30 * time.Second))
		Expect(NextAttemptDelay(7)).To(Equal(time.Hour))
	})

	It("detects sustained failure after five days", func() {
		now := time.Now().UTC()
		Expect(SustainedFailure(now.Add(-5*24*time.Hour), now)).To(BeTrue())
		Expect(SustainedFailure(now.Add(-24*time.Hour), now)).To(BeFalse())
	})
})
