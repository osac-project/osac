/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package connect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"google.golang.org/grpc"
)

// ProxyOptions configures the bidirectional I/O bridge between a local
// reader/writer pair and a gRPC console stream.
type ProxyOptions struct {
	// Reader is the local input source (os.Stdin, net.Conn, etc.).
	Reader io.Reader
	// Writer is the local output destination (os.Stdout, net.Conn, etc.).
	Writer io.Writer
	// BufSize is the read buffer size. Zero means 256.
	BufSize int
	// InputFilter, if non-nil, is called with each chunk read from Reader
	// before it is sent to the stream. Return true to stop the proxy cleanly.
	InputFilter func(data []byte) bool
	// OnDisconnect, if non-nil, is called when the server sends DISCONNECTED status.
	OnDisconnect func(message string)
	// InterruptRead, if non-nil, is called during cleanup to unblock a
	// pending Reader.Read(). It returns a function that restores the reader
	// for reuse (e.g., clearing a read deadline). If nil, Proxy does not
	// attempt to interrupt the reader.
	InterruptRead func() (restore func())
}

// Proxy runs a bidirectional I/O bridge: Reader -> gRPC stream, gRPC stream -> Writer.
// It blocks until the stream ends, ctx is cancelled, or an error occurs.
func Proxy(ctx context.Context,
	cancel context.CancelFunc,
	stream grpc.BidiStreamingClient[publicv1.ConsoleProxyConnectRequest, publicv1.ConsoleProxyConnectResponse],
	opts ProxyOptions) error {

	bufSize := opts.BufSize
	if bufSize <= 0 {
		bufSize = 256
	}

	errCh := make(chan error, 2)

	var wg sync.WaitGroup

	// Stream -> Writer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			resp, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					errCh <- nil
				} else {
					errCh <- err
				}
				return
			}

			if output := resp.GetOutput(); output != nil {
				data := output.GetData()
				if len(data) > 0 {
					if _, writeErr := opts.Writer.Write(data); writeErr != nil {
						errCh <- errors.Join(ErrLocalIOFailed, writeErr)
						return
					}
				}
			}

			if st := resp.GetStatus(); st != nil {
				switch st.GetState() {
				case publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_DISCONNECTED:
					if opts.OnDisconnect != nil {
						opts.OnDisconnect(st.GetMessage())
					}
					errCh <- nil
					return
				case publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_ERROR:
					errCh <- fmt.Errorf("server error: %s", st.GetMessage())
					return
				}
			}
		}
	}()

	// Reader -> Stream.
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, bufSize)
		for {
			n, err := opts.Reader.Read(buf)
			if ctx.Err() != nil {
				return
			}
			if n > 0 {
				data := buf[:n]
				if opts.InputFilter != nil && opts.InputFilter(data) {
					errCh <- nil
					return
				}
				sendErr := stream.Send(publicv1.ConsoleProxyConnectRequest_builder{
					Input: publicv1.ConsoleInput_builder{
						Data: bytes.Clone(data),
					}.Build(),
				}.Build())
				if sendErr != nil {
					errCh <- sendErr
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					errCh <- nil
				} else {
					errCh <- errors.Join(ErrLocalIOFailed, err)
				}
				return
			}
		}
	}()

	// Wait for the first goroutine to signal or context cancellation.
	var result error
	select {
	case err := <-errCh:
		result = err
	case <-ctx.Done():
		result = nil
	}

	// Cancel the stream context to unblock both stream.Recv() and
	// stream.Send(), then unblock opts.Reader.Read(). Wait for both
	// goroutines to exit before returning, so no stale goroutine
	// races on the Reader or stream during a reconnect.
	cancel()
	var resetReader func()
	if opts.InterruptRead != nil {
		resetReader = opts.InterruptRead()
	}
	wg.Wait()
	if resetReader != nil {
		resetReader()
	}

	return result
}
