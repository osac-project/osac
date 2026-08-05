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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var _ = Describe("Listener", func() {
	const channel = "my_channel"

	Describe("Creation", func() {
		// This doesn't need to be a working URL, just enough to be able to create the object.
		const url = "postgresql://myserver/mydb"

		It("Can be created when all the required parameters are set", func() {
			listener, err := NewListener().
				SetLogger(logger).
				SetUrl(url).
				SetChannel(channel).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(listener).ToNot(BeNil())
		})

		It("Can't be created without a logger", func() {
			listener, err := NewListener().
				SetChannel(channel).
				SetUrl(url).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(listener).To(BeNil())
		})

		It("Can't be created without a channel", func() {
			listener, err := NewListener().
				SetLogger(logger).
				SetUrl(url).
				Build()
			Expect(err).To(MatchError("channel is mandatory"))
			Expect(listener).To(BeNil())
		})

		It("Can't be created without database connection URL", func() {
			listener, err := NewListener().
				SetLogger(logger).
				SetChannel(channel).
				Build()
			Expect(err).To(MatchError("database connection URL is mandatory"))
			Expect(listener).To(BeNil())
		})

		It("Checks that wait timeout is positive", func() {
			listener, err := NewListener().
				SetLogger(logger).
				SetChannel(channel).
				SetUrl(url).
				SetWaitTimeout(-1 * time.Second).
				Build()
			Expect(err).To(MatchError("wait timeout should be positive, but it is -1s"))
			Expect(listener).To(BeNil())
		})

		It("Checks that retry interval is positive", func() {
			listener, err := NewListener().
				SetLogger(logger).
				SetChannel(channel).
				SetUrl(url).
				SetRetryInterval(-1 * time.Second).
				Build()
			Expect(err).To(MatchError("retry interval should be positive, but it is -1s"))
			Expect(listener).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			ctx      context.Context
			tm       TxManager
			payloads chan proto.Message
			listener *Listener
			notifier *Notifier
		)

		BeforeEach(func() {
			var err error

			// Create a cancelable context that will be used to stop the listener:
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(context.Background())
			DeferCleanup(cancel)

			// Prepare the database pool:
			db, err := server.NewInstance().Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(db.Close)
			pool, err := db.Pool(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pool.Close)

			// Create a listener that writes the paylaods to a channel:
			url, err := db.Url(ctx)
			Expect(err).ToNot(HaveOccurred())
			payloads = make(chan proto.Message)
			listener, err = NewListener().
				SetLogger(logger).
				SetUrl(url).
				SetChannel(channel).
				SetWaitTimeout(100 * time.Millisecond).
				SetRetryInterval(10 * time.Millisecond).
				Build()
			Expect(err).ToNot(HaveOccurred())
			go func() {
				defer GinkgoRecover()
				err := listener.Listen(
					ctx,
					func(ctx context.Context, payload proto.Message) error {
						payloads <- payload
						return nil
					},
				)
				Expect(err).To(MatchError(context.Canceled))
			}()

			// Wait until the listener is ready:
			Eventually(listener.Ready).Should(BeTrue())

			// Create the notifier:
			notifier, err = NewNotifier().
				SetLogger(logger).
				SetChannel(channel).
				SetPool(pool).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Prepare the transaction manager:
			tm, err = NewTxManager().
				SetLogger(logger).
				SetPool(pool).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		// notify sends a payload through the database notification channel.
		notify := func(ctx context.Context, payload proto.Message) {
			err := tm.Run(ctx, func(ctx context.Context) {
				err := notifier.Notify(ctx, payload)
				Expect(err).ToNot(HaveOccurred())
			})
			Expect(err).ToNot(HaveOccurred())
		}

		It("Receives one notification", func() {
			sent := wrapperspb.String("my payload")
			notify(ctx, sent)
			var received *wrapperspb.StringValue
			Eventually(payloads).Should(Receive(&received))
			Expect(proto.Equal(received, sent)).To(BeTrue())
		})

		It("Receives multiple notifications", func() {
			sent := []string{
				"cero",
				"uno",
				"dos",
				"tres",
				"cuatro",
				"cinco",
				"seis",
				"siete",
				"ocho",
				"nueve",
			}
			for _, value := range sent {
				notify(ctx, wrapperspb.String(value))
			}
			var received []string
			for range len(sent) {
				var payload *wrapperspb.StringValue
				Eventually(payloads).Should(Receive(&payload))
				received = append(received, payload.GetValue())
			}
			Expect(received).To(Equal(sent))
		})
	})
})
