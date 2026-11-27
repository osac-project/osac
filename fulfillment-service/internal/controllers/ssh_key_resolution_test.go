/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controllers_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ResolveSSHKey", func() {
	DescribeTable("classifies lookup failures by reason", func(code codes.Code, reason string, permanent bool) {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		secretsClient := controllers.NewMockSecretsClient(ctrl)
		secretsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(nil, status.Error(code, "lookup failed"))

		_, err := controllers.ResolveSSHKey(
			context.Background(),
			secretsClient,
			privatev1.SecretReference_builder{Id: "secret-id"}.Build(),
		)

		var resolutionErr *controllers.SSHKeyResolutionError
		Expect(errors.As(err, &resolutionErr)).To(BeTrue())
		Expect(resolutionErr.Reason).To(Equal(reason))
		Expect(resolutionErr.Permanent()).To(Equal(permanent))
		// OSAC-7245: the resolver classifies only; callers decide the retry
		// policy by wrapping the error with RequeueAfter.
		Expect(controllers.IsRequeueable(err)).To(BeFalse())
	},
		Entry("not found", codes.NotFound, controllers.SSHKeyResolutionReasonSecretNotFound, true),
		Entry("invalid", codes.InvalidArgument, controllers.SSHKeyResolutionReasonSecretInvalid, true),
		Entry("unavailable", codes.Unavailable, controllers.SSHKeyResolutionReasonSecretUnavailable, false),
		Entry("deadline exceeded", codes.DeadlineExceeded, controllers.SSHKeyResolutionReasonSecretUnavailable, false),
		Entry("resource exhausted", codes.ResourceExhausted, controllers.SSHKeyResolutionReasonSecretUnavailable, false),
		Entry("canceled", codes.Canceled, controllers.SSHKeyResolutionReasonInternalError, false),
		Entry("internal", codes.Internal, controllers.SSHKeyResolutionReasonInternalError, false),
	)
})
