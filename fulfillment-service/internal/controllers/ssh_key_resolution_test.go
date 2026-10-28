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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ResolveSSHKey", func() {
	It("does not classify a missing Secret as permanent", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		secretsClient := controllers.NewMockSecretsClient(ctrl)
		secretsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(nil, status.Error(codes.NotFound, "secret not found"))

		_, resolutionErr := controllers.ResolveSSHKey(
			context.Background(),
			secretsClient,
			privatev1.SecretReference_builder{Id: "secret-id"}.Build(),
		)

		Expect(resolutionErr).To(HaveOccurred())
		Expect(resolutionErr.Permanent).To(BeFalse())
		Expect(resolutionErr.Reason).To(Equal("SecretNotFound"))
	})
})
