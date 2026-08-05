package database

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

var _ = Describe("Transactions interceptor", func() {
	var (
		ctx     context.Context
		ctrl    *gomock.Controller
		manager *MockTxManager
	)

	BeforeEach(func() {
		ctx = context.Background()

		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		manager = NewMockTxManager(ctrl)
	})

	Describe("Creation", func() {
		It("Can't be created without a logger", func() {
			interceptor, err := NewTxInterceptor().
				SetManager(manager).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(interceptor).To(BeNil())
		})

		It("Can't be created without a transaction manager", func() {
			interceptor, err := NewTxInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(interceptor).To(BeNil())
		})

		It("Can be created with all the required parameters", func() {
			interceptor, err := NewTxInterceptor().
				SetLogger(logger).
				SetManager(manager).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(interceptor).ToNot(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var interceptor *TxInterceptor

		BeforeEach(func() {
			var err error
			interceptor, err = NewTxInterceptor().
				SetLogger(logger).
				SetManager(manager).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Begins transaction before calling unary method and ends it after", func() {
			// Prepare the manager mock:
			tx := NewMockTx(ctrl)
			manager.EXPECT().Begin(ctx).Return(tx, nil).Times(1)
			tx.EXPECT().End(ctx).Return(nil).Times(1)

			// Call the interceptor:
			var request any
			info := &grpc.UnaryServerInfo{}
			handler := func(ctx context.Context, request any) (response any, err error) {
				return
			}
			_, err := interceptor.UnaryServer(ctx, request, info, handler)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Adds the transaction to the context", func() {
			// Prepare the manager mock:
			tx := NewMockTx(ctrl)
			manager.EXPECT().Begin(ctx).Return(tx, nil).Times(1)
			tx.EXPECT().End(ctx).Return(nil).Times(1)

			// Call the interceptor:
			var request any
			info := &grpc.UnaryServerInfo{}
			handler := func(ctx context.Context, request any) (response any, err error) {
				txFromContext, err := TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(txFromContext).To(BeIdenticalTo(tx))
				return
			}
			_, err := interceptor.UnaryServer(ctx, request, info, handler)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Adds the transaction manager to the context", func() {
			// Prepare the manager mock:
			tx := NewMockTx(ctrl)
			manager.EXPECT().Begin(ctx).Return(tx, nil).Times(1)
			tx.EXPECT().End(ctx).Return(nil).Times(1)

			// Call the interceptor:
			var request any
			info := &grpc.UnaryServerInfo{}
			handler := func(ctx context.Context, request any) (response any, err error) {
				managerFromContext, err := TxManagerFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(managerFromContext).To(BeIdenticalTo(manager))
				return
			}
			_, err := interceptor.UnaryServer(ctx, request, info, handler)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns error if begin fails", func() {
			// Prepare the manager mock:
			err := errors.New("tx error")
			manager.EXPECT().Begin(ctx).Return(nil, err).Times(1)

			// Call the interceptor:
			var request any
			info := &grpc.UnaryServerInfo{}
			handler := func(ctx context.Context, request any) (response any, err error) {
				return
			}
			_, err = interceptor.UnaryServer(ctx, request, info, handler)
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Internal))
			Expect(status.Message()).To(Equal("failed to begin transaction"))
		})

		It("Doesn't call the handler if begin fails", func() {
			// Prepare the manager mock:
			err := errors.New("tx error")
			manager.EXPECT().Begin(ctx).Return(nil, err).Times(1)

			// Call the interceptor:
			var request any
			info := &grpc.UnaryServerInfo{}
			handler := func(ctx context.Context, request any) (response any, err error) {
				Fail("Shouldn't be called")
				return
			}
			_, err = interceptor.UnaryServer(ctx, request, info, handler)
			Expect(err).To(HaveOccurred())
		})

		It("Returns handler error if transaction end doesn't fail", func() {
			// Prepare the manager mock:
			tx := NewMockTx(ctrl)
			manager.EXPECT().Begin(ctx).Return(tx, nil).Times(1)
			tx.EXPECT().End(ctx).Return(nil).Times(1)

			// Call the interceptor:
			var request any
			info := &grpc.UnaryServerInfo{}
			handler := func(ctx context.Context, request any) (response any, err error) {
				err = grpcstatus.Error(grpccodes.NotFound, "not found")
				return
			}
			_, err := interceptor.UnaryServer(ctx, request, info, handler)
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
			Expect(status.Message()).To(Equal("not found"))
		})

		It("Returns handler error if both handler and transaction fail", func() {
			// Prepare the manager mock:
			tx := NewMockTx(ctrl)
			err := errors.New("tx error")
			manager.EXPECT().Begin(ctx).Return(tx, nil).Times(1)
			tx.EXPECT().End(ctx).Return(err).Times(1)

			// Call the interceptor:
			var request any
			info := &grpc.UnaryServerInfo{}
			handler := func(ctx context.Context, request any) (response any, err error) {
				err = grpcstatus.Error(grpccodes.NotFound, "not found")
				return
			}
			_, err = interceptor.UnaryServer(ctx, request, info, handler)
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
			Expect(status.Message()).To(Equal("not found"))
		})
	})
})
