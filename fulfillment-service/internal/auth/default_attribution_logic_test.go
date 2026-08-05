/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("Default attribution logic", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Creation", func() {
		It("Succeeds with valid logger", func() {
			logic, err := NewDefaultAttributionLogic().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(logic).ToNot(BeNil())
		})

		It("Returns error when logger is not set", func() {
			logic, err := NewDefaultAttributionLogic().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
			Expect(logic).To(BeNil())
		})
	})

	Describe("Behavior", func() {
		var ctrl *gomock.Controller

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
		})

		AfterEach(func() {
			ctrl.Finish()
		})

		It("Returns username when no resolver is configured", func() {
			logic, err := NewDefaultAttributionLogic().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			subject := &Subject{
				User: "my_creator",
			}
			ctx = ContextWithSubject(ctx, subject)
			creator, err := logic.DetermineAssignedCreator(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(creator).To(Equal("my_creator"))
		})

		It("Returns user ID when resolver successfully resolves username", func() {
			resolver := NewMockUserIDResolver(ctrl)
			resolver.EXPECT().
				GetID(gomock.Any(), "my_creator").
				Return("user-uuid-123", nil)

			logic, err := NewDefaultAttributionLogic().
				SetLogger(logger).
				SetUserIDResolver(resolver).
				Build()
			Expect(err).ToNot(HaveOccurred())
			subject := &Subject{
				User: "my_creator",
			}
			ctx = ContextWithSubject(ctx, subject)
			creator, err := logic.DetermineAssignedCreator(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(creator).To(Equal("user-uuid-123"))
		})

		It("Returns username when resolver returns empty ID", func() {
			resolver := NewMockUserIDResolver(ctrl)
			resolver.EXPECT().
				GetID(gomock.Any(), "service_account").
				Return("", nil)

			logic, err := NewDefaultAttributionLogic().
				SetLogger(logger).
				SetUserIDResolver(resolver).
				Build()
			Expect(err).ToNot(HaveOccurred())
			subject := &Subject{
				User: "service_account",
			}
			ctx = ContextWithSubject(ctx, subject)
			creator, err := logic.DetermineAssignedCreator(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(creator).To(Equal("service_account"))
		})

		It("Returns username when resolver returns an error", func() {
			resolver := NewMockUserIDResolver(ctrl)
			resolver.EXPECT().
				GetID(gomock.Any(), "system").
				Return("", fmt.Errorf("database error"))

			logic, err := NewDefaultAttributionLogic().
				SetLogger(logger).
				SetUserIDResolver(resolver).
				Build()
			Expect(err).ToNot(HaveOccurred())
			subject := &Subject{
				User: "system",
			}
			ctx = ContextWithSubject(ctx, subject)
			creator, err := logic.DetermineAssignedCreator(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(creator).To(Equal("system"))
		})

		It("Panics when there is no subject in the context", func() {
			logic, err := NewDefaultAttributionLogic().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(func() {
				logic.DetermineAssignedCreator(ctx)
			}).To(Panic())
		})
	})
})
