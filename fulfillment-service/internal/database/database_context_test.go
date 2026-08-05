/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package database

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("Database context", func() {
	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("Retrieves the transaction manager from the context", func() {
		input := NewMockTxManager(ctrl)
		ctx = TxManagerIntoContext(ctx, input)
		output, err := TxManagerFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(BeIdenticalTo(input))
	})

	It("Returns an error if transaction manager is not in the context", func() {
		_, err := TxManagerFromContext(ctx)
		Expect(err).To(MatchError("failed to get transaction manager from context"))
	})

	It("Retrieves the transaction from the context", func() {
		input := NewMockTx(ctrl)
		ctx = TxIntoContext(ctx, input)
		output, err := TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(BeIdenticalTo(input))
	})

	It("Returns an error if transaction is not in the context", func() {
		_, err := TxFromContext(ctx)
		Expect(err).To(MatchError("failed to get transaction from context"))
	})
})
