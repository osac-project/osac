/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package shutdown

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/puddle/v2"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
)

var _ = Describe("Shutdown sequence", func() {
	Describe("Creation", func() {
		It("Can't be created without a logger", func() {
			sequence, err := NewSequence().Build()
			Expect(err).To(HaveOccurred())
			message := err.Error()
			Expect(message).To(ContainSubstring("logger"))
			Expect(message).To(ContainSubstring("mandatory"))
			Expect(sequence).To(BeNil())
		})

		It("Can't be created with a negative delay", func() {
			sequence, err := NewSequence().
				SetLogger(logger).
				SetDelay(-1 * time.Second).
				Build()
			Expect(err).To(HaveOccurred())
			message := err.Error()
			Expect(message).To(ContainSubstring("delay"))
			Expect(message).To(ContainSubstring("greater"))
			Expect(message).To(ContainSubstring("zero"))
			Expect(message).To(ContainSubstring("-1s"))
			Expect(sequence).To(BeNil())
		})

		It("Can't be created with a negative timeout", func() {
			sequence, err := NewSequence().
				SetLogger(logger).
				SetTimeout(-1 * time.Second).
				Build()
			Expect(err).To(HaveOccurred())
			message := err.Error()
			Expect(message).To(ContainSubstring("timeout"))
			Expect(message).To(ContainSubstring("greater"))
			Expect(message).To(ContainSubstring("zero"))
			Expect(message).To(ContainSubstring("-1s"))
			Expect(sequence).To(BeNil())
		})

		It("Can't be created without an exit function", func() {
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nil).
				Build()
			Expect(err).To(HaveOccurred())
			message := err.Error()
			Expect(message).To(ContainSubstring("exit"))
			Expect(message).To(ContainSubstring("mandatory"))
			Expect(sequence).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		// nopExit is an exit function that does nothing.
		nopExit := func(code int) {}

		It("Executes one action", func() {
			// Create an action function:
			called := false
			action := func(ctx context.Context) error {
				called = true
				return nil
			}

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				Build()
			Expect(err).ToNot(HaveOccurred())
			sequence.AddFunction("my-action", 0, action)

			// Start the shutdown sequence:
			sequence.Start(1)

			// Verify that the action was executed:
			Expect(called).To(BeTrue())
		})

		It("Executes multiple actions", func() {
			// Create action functions:
			called1 := false
			action1 := func(ctx context.Context) error {
				called1 = true
				return nil
			}
			called2 := false
			action2 := func(ctx context.Context) error {
				called2 = true
				return nil
			}
			called3 := false
			action3 := func(ctx context.Context) error {
				called3 = true
				return nil
			}

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddFunction("action1", 0, action1).
				AddFunction("action2", 0, action2).
				AddFunction("action3", 0, action3).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(1)

			// Verify that the actions were called:
			Expect(called1).To(BeTrue())
			Expect(called2).To(BeTrue())
			Expect(called3).To(BeTrue())
		})

		It("Executes action even if previous action failed", func() {
			// Create action functions:
			called := false
			action1 := func(ctx context.Context) error {
				return errors.New("failed")
			}
			action2 := func(ctx context.Context) error {
				called = true
				return nil
			}

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddFunction("action1", 0, action1).
				AddFunction("action2", 0, action2).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(1)

			// Verify that the action was called:
			Expect(called).To(BeTrue())
		})

		It("Calls the exit function with the given code", func() {
			// Create an exit function that saves the exit code:
			var code int
			exit := func(c int) {
				code = c
			}

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(exit).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(123)

			// Verify the code:
			Expect(code).To(Equal(123))
		})

		It("Calls the exit function with the given code even if some actions failed", func() {
			// Create an exit function that saves the exit code:
			var code int
			exit := func(c int) {
				code = c
			}

			// Create the actions:
			action1 := func(ctx context.Context) error {
				return nil
			}
			action2 := func(ctx context.Context) error {
				return errors.New("failed")
			}
			action3 := func(ctx context.Context) error {
				return nil
			}

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(exit).
				AddFunction("action1", 0, action1).
				AddFunction("action2", 0, action2).
				AddFunction("action3", 0, action3).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(123)

			// Verify the code:
			Expect(code).To(Equal(123))
		})

		It("Passes a context with a deadline to the actions", func() {
			// Create a action that verifies that the context has a deadline:
			action := func(ctx context.Context) error {
				defer GinkgoRecover()
				Expect(ctx).ToNot(BeNil())
				deadline, ok := ctx.Deadline()
				Expect(ok).To(BeTrue())
				Expect(deadline).ToNot(BeZero())
				return nil
			}

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddFunction("action", 0, action).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)
		})

		It("Cancels the context passed to actions when the timeout expires", func() {
			// Create a action that waits longer than the timeout configured in the shutdown sequence, and then verifies
			// that the context has been cancelled:
			done := make(chan struct{})
			action := func(ctx context.Context) error {
				defer GinkgoRecover()
				defer close(done)
				time.Sleep(200 * time.Millisecond)
				err := ctx.Err()
				Expect(err).To(Equal(context.DeadlineExceeded))
				return nil
			}

			// Create the shutdown sequence, setting a timeout that will give us time to verify that the context passed
			// to the action is cancelled:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetTimeout(100*time.Millisecond).
				SetExit(nopExit).
				AddFunction("action", 0, action).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// The action will not finish on time, so we need to explicitly wait till it finishes, otherwise the
			// expectation failures will not be reported correctly:
			<-done
		})

		It("Waits the configured delay before running actions", func() {
			// Create a action that does nothing:
			delay := 100 * time.Millisecond
			start := time.Now()
			action := func(ctx context.Context) error {
				defer GinkgoRecover()
				elapsed := time.Since(start)
				Expect(elapsed).To(BeNumerically(">=", delay))
				return nil
			}

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetDelay(delay).
				SetExit(nopExit).
				AddFunction("action", 0, action).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)
		})

		It("Aborts actions after the timeout expires", func() {
			// Create a action that waits longer than the timeout configured in the shutdown sequence:
			highAction := func(ctx context.Context) error {
				time.Sleep(200 * time.Millisecond)
				return nil
			}

			// Create a action that should not be executed because it has a lower priority and the first action times out:
			lowCalled := false
			lowAction := func(ctx context.Context) error {
				lowCalled = true
				return nil
			}

			// Create the shutdown sequence with different priorities so that action1 runs first and action2 runs after:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetTimeout(100*time.Millisecond).
				SetExit(nopExit).
				AddFunction("high", HighPriority, highAction).
				AddFunction("low", LowPriority, lowAction).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Verify that the second action wasn't called:
			Expect(lowCalled).To(BeFalse())
		})

		It("Writes action failure to the log", func() {
			// Create a action that fails with an error easy to locate in the log:
			key := uuid.New().String()
			action := func(ctx context.Context) error {
				return errors.New(key)
			}

			// Create a logger that writes to both the Ginkgo writer and a memory buffer, so that we can inspect it:
			buffer := &bytes.Buffer{}
			writer := io.MultiWriter(GinkgoWriter, buffer)
			options := &slog.HandlerOptions{
				Level: slog.LevelDebug,
			}
			handler := slog.NewJSONHandler(writer, options)
			multi := slog.New(handler)

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(multi).
				SetExit(nopExit).
				AddFunction("failing action", 0, action).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Verify that the log contains the action error:
			Expect(buffer.String()).To(ContainSubstring(key))
		})

		It("Calls the exit function even if actions take longer than the timeout", func() {
			// Create a action that takes longer than the timeout:
			timeout := 100 * time.Millisecond
			action := func(ctx context.Context) error {
				time.Sleep(2 * timeout)
				return nil
			}

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetTimeout(timeout).
				SetExit(nopExit).
				AddFunction("my-slow-action", 0, action).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			start := time.Now()
			sequence.Start(0)

			// Verify that this took approximately the specified timeout. Note that this will never be exact, because the
			// shutdown sequence takes additional time to do its work, that is the reason for the 20 milliseconds of
			// margin.
			elapsed := time.Since(start)
			Expect(elapsed).To(BeNumerically("==", timeout, 20*time.Millisecond))
		})

		It("Starts automatically when it receives a signal", func() {
			// Create a action:
			done := make(chan struct{})
			action := func(ctx context.Context) error {
				close(done)
				return nil
			}

			// Create the shutdown sequence:
			_, err := NewSequence().
				SetLogger(logger).
				AddSignal(syscall.SIGUSR2).
				SetExit(nopExit).
				AddFunction("my-action", 0, action).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Send the signal:
			err = syscall.Kill(syscall.Getpid(), syscall.SIGUSR2)
			Expect(err).ToNot(HaveOccurred())

			// Verify that the action was executed:
			Eventually(done).Should(BeClosed())
		})

		It("Executes higher priority actions before lower priority actions", func() {
			// Create action functions that record the order of execution:
			var order []string
			var lock sync.Mutex

			lowAction := func(ctx context.Context) error {
				lock.Lock()
				defer lock.Unlock()
				order = append(order, "low")
				return nil
			}
			midAction := func(ctx context.Context) error {
				lock.Lock()
				defer lock.Unlock()
				order = append(order, "mid")
				return nil
			}
			highAction := func(ctx context.Context) error {
				lock.Lock()
				defer lock.Unlock()
				order = append(order, "high")
				return nil
			}

			// Create the shutdown sequence with different priorities (action2 has highest priority):
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddFunction("low", 1, lowAction).
				AddFunction("mid", 50, midAction).
				AddFunction("high", 100, highAction).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Verify that actions were executed in order of priority (highest first):
			Expect(order).To(Equal([]string{"high", "mid", "low"}))
		})

		It("Executes actions with the same priority simultaneously", func() {
			// Create action functions that record when they start and finish:
			var action1Started, action2Started time.Time
			var lock sync.Mutex

			action1 := func(ctx context.Context) error {
				lock.Lock()
				action1Started = time.Now()
				lock.Unlock()
				time.Sleep(50 * time.Millisecond)
				return nil
			}
			action2 := func(ctx context.Context) error {
				lock.Lock()
				action2Started = time.Now()
				lock.Unlock()
				time.Sleep(50 * time.Millisecond)
				return nil
			}

			// Create the shutdown sequence with the same priority for both actions:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddFunction("action1", 50, action1).
				AddFunction("action2", 50, action2).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Verify that both actions started at approximately the same time (within 10ms):
			diff := action1Started.Sub(action2Started).Abs()
			Expect(diff).To(BeNumerically("<", 10*time.Millisecond))
		})

		It("Waits for all actions of one priority before starting next priority", func() {
			// Create action functions that record the order:
			var order []string
			var lock sync.Mutex

			highAction1 := func(ctx context.Context) error {
				time.Sleep(50 * time.Millisecond)
				lock.Lock()
				defer lock.Unlock()
				order = append(order, "high1")
				return nil
			}
			highAction2 := func(ctx context.Context) error {
				time.Sleep(50 * time.Millisecond)
				lock.Lock()
				defer lock.Unlock()
				order = append(order, "high2")
				return nil
			}
			lowAction := func(ctx context.Context) error {
				lock.Lock()
				defer lock.Unlock()
				order = append(order, "low")
				return nil
			}

			// Create the shutdown sequence:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddFunction("high1", HighPriority, highAction1).
				AddFunction("high2", HighPriority, highAction2).
				AddFunction("low", LowPriority, lowAction).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Verify that both high priority actions finished before the low priority action started:
			Expect(order).To(HaveLen(3))
			Expect(order[2]).To(Equal("low"))

			// The first two can be in any order since they run simultaneously:
			Expect(order[:2]).To(ContainElements("high1", "high2"))
		})

		It("Calls the cancel function for a context", func() {
			// Create a context with cancellation:
			ctx, cancel := context.WithCancel(context.Background())

			// Create the shutdown sequence with the context cancel function:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddContext("my-context", 0, cancel).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify that the context is not cancelled before the shutdown sequence starts:
			Expect(ctx.Err()).To(Succeed())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Verify that the context was cancelled:
			Expect(ctx.Err()).To(Equal(context.Canceled))
		})

		It("Stops an HTTP server", func() {
			// Create a listener on a random port:
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			address := listener.Addr().String()

			// Create an HTTP server:
			server := &http.Server{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				}),
			}

			// Start the server in a goroutine:
			serverDone := make(chan struct{})
			go func() {
				defer close(serverDone)
				server.Serve(listener)
			}()

			// Verify the server is accepting connections before the shutdown:
			conn, err := net.Dial("tcp", address)
			Expect(err).ToNot(HaveOccurred())
			conn.Close()

			// Create the shutdown sequence with the HTTP server:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddHttpServer("my-http-server", 0, server).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Wait for the server goroutine to finish:
			Eventually(serverDone).Should(BeClosed())

			// Verify the server is no longer accepting connections:
			_, err = net.DialTimeout("tcp", address, 100*time.Millisecond)
			Expect(err).To(HaveOccurred())
		})

		It("Stops a gRPC server", func() {
			// Create a listener on a random port:
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			address := listener.Addr().String()

			// Create a gRPC server:
			server := grpc.NewServer()

			// Start the server in a goroutine:
			done := make(chan struct{})
			go func() {
				defer close(done)
				server.Serve(listener)
			}()

			// Verify the server is accepting connections before the shutdown:
			conn, err := net.Dial("tcp", address)
			Expect(err).ToNot(HaveOccurred())
			conn.Close()

			// Create the shutdown sequence with the gRPC server:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddGrpcServer("my-grpc-server", 0, server).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Wait for the server goroutine to finish:
			Eventually(done).Should(BeClosed())

			// Verify the server is no longer accepting connections:
			_, err = net.DialTimeout("tcp", address, 100*time.Millisecond)
			Expect(err).To(HaveOccurred())
		})

		It("Stops a TCP listener", func() {
			// Create a listener on a random port:
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			address := listener.Addr().String()

			// Verify the listener is accepting connections before the shutdown:
			conn, err := net.Dial("tcp", address)
			Expect(err).ToNot(HaveOccurred())
			conn.Close()

			// Create the shutdown sequence with the listener:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddListener("my-listener", 0, listener).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Verify the listener is no longer accepting connections:
			_, err = net.DialTimeout("tcp", address, 100*time.Millisecond)
			Expect(err).To(HaveOccurred())
		})

		It("Closes a database connection pool", func() {
			// Create a pool configuration pointing to a non-existent server. With number of connections
			// set to zero the pool won't try to connect immediately, allowing us to create the pool
			// without a real database.
			config, err := pgxpool.ParseConfig("postgres://user:pass@localhost:59999/test")
			Expect(err).ToNot(HaveOccurred())
			config.MinConns = 0
			config.MaxConns = 1

			// Create the pool:
			pool, err := pgxpool.NewWithConfig(context.Background(), config)
			Expect(err).ToNot(HaveOccurred())

			// Create the shutdown sequence with the database pool:
			sequence, err := NewSequence().
				SetLogger(logger).
				SetExit(nopExit).
				AddDatabasePool("my-pool", 0, pool).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the shutdown sequence:
			sequence.Start(0)

			// Verify the pool is closed by trying to acquire a connection:
			_, err = pool.Acquire(context.Background())
			Expect(err).To(MatchError(puddle.ErrClosedPool))
		})
	})
})
