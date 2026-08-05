/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

// testProxyStream implements ConsoleProxy_ConnectServer for unit testing
// the grpcStreamRW wrapper.
type testProxyStream struct {
	grpc.ServerStream
	recvQueue []*publicv1.ConsoleProxyConnectRequest
	recvIdx   int
	sent      []*publicv1.ConsoleProxyConnectResponse
	ctx       context.Context
}

func (s *testProxyStream) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *testProxyStream) SendHeader(metadata.MD) error { return nil }
func (s *testProxyStream) SetHeader(metadata.MD) error  { return nil }
func (s *testProxyStream) SetTrailer(metadata.MD)       {}
func (s *testProxyStream) SendMsg(m any) error          { return nil }
func (s *testProxyStream) RecvMsg(m any) error          { return nil }

func (s *testProxyStream) Recv() (*publicv1.ConsoleProxyConnectRequest, error) {
	if s.recvIdx >= len(s.recvQueue) {
		return nil, io.EOF
	}
	msg := s.recvQueue[s.recvIdx]
	s.recvIdx++
	return msg, nil
}

func (s *testProxyStream) Send(resp *publicv1.ConsoleProxyConnectResponse) error {
	s.sent = append(s.sent, resp)
	return nil
}

// blockingProxyStream blocks on Recv until its context is cancelled.
type blockingProxyStream struct {
	testProxyStream
	blockCh chan struct{}
}

func newBlockingProxyStream() *blockingProxyStream {
	return &blockingProxyStream{
		testProxyStream: testProxyStream{ctx: context.Background()},
		blockCh:         make(chan struct{}),
	}
}

func (s *blockingProxyStream) Recv() (*publicv1.ConsoleProxyConnectRequest, error) {
	<-s.blockCh
	return nil, io.EOF
}

var _ = Describe("grpcStreamRW", func() {
	It("should read input data from stream messages", func() {
		stream := &testProxyStream{
			recvQueue: []*publicv1.ConsoleProxyConnectRequest{
				publicv1.ConsoleProxyConnectRequest_builder{
					Input: publicv1.ConsoleInput_builder{
						Data: []byte("hello"),
					}.Build(),
				}.Build(),
			},
		}

		rw := newGrpcStreamRW(stream)
		defer rw.Close()
		buf := make([]byte, 64)
		n, err := rw.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(Equal("hello"))
	})

	It("should buffer partial reads when caller buffer is smaller than message", func() {
		stream := &testProxyStream{
			recvQueue: []*publicv1.ConsoleProxyConnectRequest{
				publicv1.ConsoleProxyConnectRequest_builder{
					Input: publicv1.ConsoleInput_builder{
						Data: []byte("abcdefghij"),
					}.Build(),
				}.Build(),
			},
		}

		rw := newGrpcStreamRW(stream)
		defer rw.Close()

		// First read: small buffer, should get partial data.
		buf := make([]byte, 4)
		n, err := rw.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(4))
		Expect(string(buf[:n])).To(Equal("abcd"))

		// Second read: should get the remainder from the buffer.
		buf2 := make([]byte, 64)
		n, err = rw.Read(buf2)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf2[:n])).To(Equal("efghij"))
	})

	It("should write output data as ConsoleProxyConnectResponse", func() {
		stream := &testProxyStream{}
		rw := newGrpcStreamRW(stream)
		defer rw.Close()

		n, err := rw.Write([]byte("output-data"))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(len("output-data")))

		Expect(stream.sent).To(HaveLen(1))
		Expect(stream.sent[0].GetOutput().GetData()).To(Equal([]byte("output-data")))
	})

	It("should return io.EOF when stream ends", func() {
		stream := &testProxyStream{
			recvQueue: nil,
		}

		rw := newGrpcStreamRW(stream)
		defer rw.Close()
		buf := make([]byte, 64)
		_, err := rw.Read(buf)
		Expect(err).To(Equal(io.EOF))
	})

	It("should handle multiple sequential reads from different messages", func() {
		stream := &testProxyStream{
			recvQueue: []*publicv1.ConsoleProxyConnectRequest{
				publicv1.ConsoleProxyConnectRequest_builder{
					Input: publicv1.ConsoleInput_builder{Data: []byte("first")}.Build(),
				}.Build(),
				publicv1.ConsoleProxyConnectRequest_builder{
					Input: publicv1.ConsoleInput_builder{Data: []byte("second")}.Build(),
				}.Build(),
			},
		}

		rw := newGrpcStreamRW(stream)
		defer rw.Close()
		buf := make([]byte, 64)

		n, err := rw.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(Equal("first"))

		n, err = rw.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(Equal("second"))

		_, err = rw.Read(buf)
		Expect(err).To(Equal(io.EOF))
	})

	It("should return io.ErrClosedPipe when Close unblocks a pending Read", func() {
		stream := newBlockingProxyStream()
		rw := newGrpcStreamRW(stream)

		errCh := make(chan error, 1)
		go func() {
			buf := make([]byte, 64)
			_, err := rw.Read(buf)
			errCh <- err
		}()

		// Close should unblock the pending Read via the done channel.
		rw.Close()
		Eventually(errCh).Should(Receive(Equal(io.ErrClosedPipe)))

		// Unblock the recv goroutine so it can exit cleanly.
		close(stream.blockCh)
	})

	It("should implement io.ReadWriteCloser", func() {
		var _ io.ReadWriteCloser = (*grpcStreamRW)(nil)
	})
})
