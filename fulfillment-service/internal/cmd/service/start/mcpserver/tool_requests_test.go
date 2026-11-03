package mcpserver

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("safe missing Secret guidance", func() {
	It("returns a safe not-found envelope with UI guidance", func() {
		err := safeMissingSecretReferenceError("pull-secret")
		envelope := decodeToolError(err)

		Expect(envelope.Category).To(Equal("not_found"))
		Expect(envelope.GRPCCode).To(Equal("NotFound"))
		Expect(envelope.Retryable).To(BeFalse())
		Expect(envelope.Message).To(ContainSubstring("/secrets/create"))
		Expect(envelope.Details).To(ConsistOf("Secret reference pull-secret is missing"))

		var document map[string]any
		Expect(json.Unmarshal([]byte(err.Error()), &document)).To(Succeed())
		Expect(document).To(HaveLen(6))
		Expect(document).ToNot(HaveKey("secret"))
		Expect(document).ToNot(HaveKey("value"))
		Expect(document).ToNot(HaveKey("data"))
		Expect(document).ToNot(HaveKey("token"))
	})
})
