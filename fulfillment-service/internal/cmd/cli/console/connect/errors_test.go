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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ = Describe("IsPermanentError", func() {
	DescribeTable("permanent errors",
		func(code codes.Code) {
			err := status.Error(code, "test")
			Expect(IsPermanentError(err)).To(BeTrue())
		},
		Entry("PermissionDenied", codes.PermissionDenied),
		Entry("NotFound", codes.NotFound),
		Entry("Unauthenticated", codes.Unauthenticated),
		Entry("FailedPrecondition", codes.FailedPrecondition),
		Entry("InvalidArgument", codes.InvalidArgument),
		Entry("Unimplemented", codes.Unimplemented),
	)

	DescribeTable("transient errors",
		func(code codes.Code) {
			err := status.Error(code, "test")
			Expect(IsPermanentError(err)).To(BeFalse())
		},
		Entry("Unavailable", codes.Unavailable),
		Entry("Internal", codes.Internal),
	)

	It("should classify non-gRPC errors as transient", func() {
		err := &time.ParseError{}
		Expect(IsPermanentError(err)).To(BeFalse())
	})
})
