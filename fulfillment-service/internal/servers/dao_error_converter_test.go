/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
)

var _ = Describe("ConvertDAOErrorToGRPC", func() {

	Context("when err is nil", func() {
		It("returns nil", func() {
			err := ConvertDAOErrorToGRPC(nil, "create", "test-id")
			Expect(err).To(Not(HaveOccurred()))
		})
	})

	Context("with NotFound errors", func() {
		It("converts to NotFound with objectID", func() {
			err := &dao.ErrNotFound{IDs: []string{"test-id"}}
			result := ConvertDAOErrorToGRPC(err, "get", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.NotFound))
			Expect(result.Error()).To(ContainSubstring("test-id"))
		})

		It("converts to NotFound without objectID", func() {
			err := &dao.ErrNotFound{IDs: []string{"some-id"}}
			result := ConvertDAOErrorToGRPC(err, "get", "")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.NotFound))
			Expect(result.Error()).To(ContainSubstring("some-id"))
		})
	})

	Context("with AlreadyExists errors", func() {
		It("converts regular AlreadyExists to AlreadyExists code", func() {
			err := &dao.ErrAlreadyExists{
				Kind: "compute instance",
				ID:   "test-id",
			}
			result := ConvertDAOErrorToGRPC(err, "create", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.AlreadyExists))
			Expect(result.Error()).To(ContainSubstring("compute instance"))
		})

		It("converts singleton constraint violations to FailedPrecondition", func() {
			err := &dao.ErrAlreadyExists{
				Kind:           "network class",
				ConstraintName: "network_classes_singleton",
			}
			result := ConvertDAOErrorToGRPC(err, "create", "")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.FailedPrecondition))
			Expect(result.Error()).To(ContainSubstring("singleton invariant"))
		})

		It("converts single_default constraint violations to FailedPrecondition", func() {
			err := &dao.ErrAlreadyExists{
				Kind:           "network class",
				ConstraintName: "network_classes_single_default",
			}
			result := ConvertDAOErrorToGRPC(err, "create", "")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.FailedPrecondition))
			Expect(result.Error()).To(ContainSubstring("singleton invariant"))
		})
	})

	Context("with NotUnique errors", func() {
		It("converts to AlreadyExists", func() {
			err := &dao.ErrNotUnique{Reason: "domain already assigned"}
			result := ConvertDAOErrorToGRPC(err, "create", "")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.AlreadyExists))
			Expect(result.Error()).To(ContainSubstring("domain already assigned"))
		})
	})

	Context("with Conflict errors", func() {
		It("converts to Aborted", func() {
			err := &dao.ErrConflict{
				ID:             "test-id",
				RequestVersion: 1,
				CurrentVersion: 2,
			}
			result := ConvertDAOErrorToGRPC(err, "update", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.Aborted))
			Expect(result.Error()).To(ContainSubstring("modified"))
		})
	})

	Context("with Deadlock errors", func() {
		It("converts to Aborted", func() {
			err := &dao.ErrDeadlock{}
			result := ConvertDAOErrorToGRPC(err, "update", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.Aborted))
			Expect(result.Error()).To(ContainSubstring("concurrent modification"))
		})
	})

	Context("with Denied errors", func() {
		It("converts to PermissionDenied", func() {
			err := &dao.ErrDenied{Reason: "operation not allowed"}
			result := ConvertDAOErrorToGRPC(err, "delete", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.PermissionDenied))
			Expect(result.Error()).To(ContainSubstring("operation not allowed"))
		})
	})

	Context("with Immutable errors", func() {
		It("converts to InvalidArgument", func() {
			err := &dao.ErrImmutable{Fields: []string{"metadata.name"}}
			result := ConvertDAOErrorToGRPC(err, "update", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.InvalidArgument))
			Expect(result.Error()).To(ContainSubstring("immutable"))
		})
	})

	Context("with Reference errors", func() {
		It("converts to InvalidArgument for create operations", func() {
			err := &dao.ErrReference{Reason: "subnet not found"}
			result := ConvertDAOErrorToGRPC(err, "create", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.InvalidArgument))
			Expect(result.Error()).To(ContainSubstring("subnet not found"))
		})

		It("converts to FailedPrecondition for update operations", func() {
			err := &dao.ErrReference{Reason: "subnet not found"}
			result := ConvertDAOErrorToGRPC(err, "update", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.FailedPrecondition))
			Expect(result.Error()).To(ContainSubstring("subnet not found"))
		})

		It("converts to FailedPrecondition for delete operations", func() {
			err := &dao.ErrReference{Reason: "subnet not found"}
			result := ConvertDAOErrorToGRPC(err, "delete", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.FailedPrecondition))
			Expect(result.Error()).To(ContainSubstring("subnet not found"))
		})
	})

	Context("with InUse errors", func() {
		It("converts to FailedPrecondition", func() {
			err := &dao.ErrInUse{Reason: "still referenced by compute instances"}
			result := ConvertDAOErrorToGRPC(err, "delete", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.FailedPrecondition))
			Expect(result.Error()).To(ContainSubstring("still referenced"))
		})
	})

	Context("with Validation errors", func() {
		It("converts to InvalidArgument", func() {
			err := &dao.ErrValidation{Reason: "invalid CIDR"}
			result := ConvertDAOErrorToGRPC(err, "create", "")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.InvalidArgument))
			Expect(result.Error()).To(ContainSubstring("invalid CIDR"))
		})
	})

	Context("with InvalidFilter errors", func() {
		It("converts to InvalidArgument", func() {
			err := &dao.ErrInvalidFilter{Reason: "syntax error in filter"}
			result := ConvertDAOErrorToGRPC(err, "list", "")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.InvalidArgument))
			Expect(result.Error()).To(ContainSubstring("syntax error"))
		})
	})

	Context("with unknown errors", func() {
		It("converts to Internal with objectID", func() {
			err := errors.New("unexpected database error")
			result := ConvertDAOErrorToGRPC(err, "create", "test-id")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.Internal))
			Expect(result.Error()).To(ContainSubstring("failed to create"))
			Expect(result.Error()).To(ContainSubstring("test-id"))
		})

		It("converts to Internal without objectID", func() {
			err := errors.New("unexpected database error")
			result := ConvertDAOErrorToGRPC(err, "list", "")
			Expect(grpcstatus.Code(result)).To(Equal(grpccodes.Internal))
			Expect(result.Error()).To(ContainSubstring("failed to list"))
		})
	})
})

var _ = Describe("ValidateRequiredString", func() {
	It("returns nil for non-empty strings", func() {
		err := ValidateRequiredString("name", "test-value")
		Expect(err).To(Not(HaveOccurred()))
	})

	It("returns InvalidArgument for empty strings", func() {
		err := ValidateRequiredString("name", "")
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("name"))
		Expect(err.Error()).To(ContainSubstring("mandatory"))
	})
})

var _ = Describe("ValidateRequiredObject", func() {
	It("returns nil for non-nil objects", func() {
		obj := struct{ Name string }{Name: "test"}
		err := ValidateRequiredObject("object", obj)
		Expect(err).To(Not(HaveOccurred()))
	})

	It("returns InvalidArgument for nil objects", func() {
		err := ValidateRequiredObject("object", nil)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("object"))
		Expect(err.Error()).To(ContainSubstring("required"))
	})

	It("returns InvalidArgument for typed nil pointers", func() {
		var obj *struct{ Name string }
		err := ValidateRequiredObject("object", obj)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("object"))
		Expect(err.Error()).To(ContainSubstring("required"))
	})
})
