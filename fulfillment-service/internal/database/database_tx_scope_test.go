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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("WithNewTx", func() {
	var (
		ctx     context.Context
		ctrl    *gomock.Controller
		manager *MockTxManager
		tx      *MockTx
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		manager = NewMockTxManager(ctrl)
		tx = NewMockTx(ctrl)
		ctx = TxManagerIntoContext(ctx, manager)
	})

	It("Returns the callback result when it succeeds", func() {
		manager.EXPECT().Begin(gomock.Any()).Return(tx, nil)
		tx.EXPECT().ReportError(gomock.Any())
		tx.EXPECT().End(gomock.Any()).Return(nil)

		result, err := WithNewTx(ctx, func(txCtx context.Context) (string, error) {
			return "hello", nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal("hello"))
	})

	It("Propagates callback errors and reports them to the transaction", func() {
		callbackErr := errors.New("callback failed")
		manager.EXPECT().Begin(gomock.Any()).Return(tx, nil)
		tx.EXPECT().ReportError(gomock.Any()).Do(func(err *error) {
			Expect(*err).To(MatchError("callback failed"))
		})
		tx.EXPECT().End(gomock.Any()).Return(nil)

		_, err := WithNewTx(ctx, func(txCtx context.Context) (string, error) {
			return "", callbackErr
		})
		Expect(err).To(MatchError("callback failed"))
	})

	It("Returns an error when the transaction manager is not in the context", func() {
		_, err := WithNewTx(context.Background(), func(txCtx context.Context) (string, error) {
			return "should not reach", nil
		})
		Expect(err).To(MatchError(ContainSubstring("get transaction manager from context")))
	})

	It("Returns an error when Begin fails", func() {
		manager.EXPECT().Begin(gomock.Any()).Return(nil, errors.New("pool exhausted"))

		_, err := WithNewTx(ctx, func(txCtx context.Context) (string, error) {
			return "should not reach", nil
		})
		Expect(err).To(MatchError(ContainSubstring("begin transaction")))
		Expect(err).To(MatchError(ContainSubstring("pool exhausted")))
	})

	It("Surfaces End error when the callback succeeds", func() {
		manager.EXPECT().Begin(gomock.Any()).Return(tx, nil)
		tx.EXPECT().ReportError(gomock.Any())
		tx.EXPECT().End(gomock.Any()).Return(errors.New("commit failed"))

		_, err := WithNewTx(ctx, func(txCtx context.Context) (string, error) {
			return "value", nil
		})
		Expect(err).To(MatchError(ContainSubstring("end transaction")))
		Expect(err).To(MatchError(ContainSubstring("commit failed")))
	})

	It("Prefers callback error over End error", func() {
		manager.EXPECT().Begin(gomock.Any()).Return(tx, nil)
		tx.EXPECT().ReportError(gomock.Any())
		tx.EXPECT().End(gomock.Any()).Return(errors.New("rollback failed"))

		_, err := WithNewTx(ctx, func(txCtx context.Context) (string, error) {
			return "", errors.New("domain error")
		})
		Expect(err).To(MatchError("domain error"))
	})

	It("Rolls back and re-panics when the callback panics", func() {
		manager.EXPECT().Begin(gomock.Any()).Return(tx, nil)
		tx.EXPECT().ReportError(gomock.Any()).Do(func(err *error) {
			Expect(*err).To(MatchError(ContainSubstring("panic")))
		})
		tx.EXPECT().End(gomock.Any()).Return(nil)

		Expect(func() {
			WithNewTx(ctx, func(txCtx context.Context) (string, error) {
				panic("test panic")
			})
		}).To(PanicWith("test panic"))
	})

	It("Shadows the outer transaction in the callback context", func() {
		outerTx := NewMockTx(ctrl)
		ctxWithOuterTx := TxIntoContext(ctx, outerTx)

		manager.EXPECT().Begin(gomock.Any()).Return(tx, nil)
		tx.EXPECT().ReportError(gomock.Any())
		tx.EXPECT().End(gomock.Any()).Return(nil)

		_, err := WithNewTx(ctxWithOuterTx, func(txCtx context.Context) (string, error) {
			innerTx, txErr := TxFromContext(txCtx)
			Expect(txErr).ToNot(HaveOccurred())
			Expect(innerTx).To(BeIdenticalTo(tx))
			Expect(innerTx).ToNot(BeIdenticalTo(outerTx))
			return "ok", nil
		})
		Expect(err).ToNot(HaveOccurred())
	})
})
