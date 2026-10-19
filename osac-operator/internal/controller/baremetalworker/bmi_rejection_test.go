// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A definitive rejection is a terminal Create response: the server refused the
// request and rolled back its transaction, so no BMI can exist for the attempt.
// Transport-class and unknown outcomes stay conservative.
var definitiveCreateCodes = []codes.Code{
	codes.InvalidArgument,
	codes.NotFound,
	codes.FailedPrecondition,
	codes.PermissionDenied,
	codes.ResourceExhausted,
	codes.OutOfRange,
	codes.Unimplemented,
}

var conservativeCreateCodes = []codes.Code{
	codes.Unavailable,
	codes.DeadlineExceeded,
	codes.Canceled,
	codes.Unauthenticated,
	codes.Unknown,
	codes.Internal,
	codes.DataLoss,
}

var _ = Describe("Definitive BMI create rejection classification", func() {
	for _, code := range definitiveCreateCodes {
		It(fmt.Sprintf("classifies terminal %s as definitive", code), func() {
			Expect(isDefinitiveCreateRejection(status.Error(code, "rejected"))).To(BeTrue())
		})
		It(fmt.Sprintf("classifies wrapped terminal %s as definitive", code), func() {
			wrapped := fmt.Errorf("creating BMI slot: %w", status.Error(code, "rejected"))
			Expect(isDefinitiveCreateRejection(wrapped)).To(BeTrue())
		})
	}
	for _, code := range conservativeCreateCodes {
		It(fmt.Sprintf("keeps %s conservative", code), func() {
			Expect(isDefinitiveCreateRejection(status.Error(code, "failure"))).To(BeFalse())
		})
	}
	It("keeps non-status errors conservative", func() {
		Expect(isDefinitiveCreateRejection(errors.New("interrupted create"))).To(BeFalse())
	})
	It("keeps availability-wrapped transport failures conservative", func() {
		wrapped := fmt.Errorf("%w: %w", ErrFulfillmentServiceUnavailable, status.Error(codes.Unavailable, "outage"))
		Expect(isDefinitiveCreateRejection(wrapped)).To(BeFalse())
	})
	It("treats a nil error as no rejection", func() {
		Expect(isDefinitiveCreateRejection(nil)).To(BeFalse())
	})
})

var _ = Describe("ensureBMI marks definitive Create rejections", func() {
	rejectedCreate := func(code codes.Code, message string) (*nodeSetClient, error) {
		r, fc, co := nodeSetHarness("rejection-mark", nodeRequest("standard", 1))
		fc.createErr = status.Error(code, message)
		cluster := authoritativeTestCluster(context.Background(), r, co)
		_, _, err := r.ensureBMI(context.Background(), co, cluster, co.Spec.NodeRequests[0], "slot", nil, "", "", "")
		return fc, err
	}
	for _, code := range definitiveCreateCodes {
		It(fmt.Sprintf("wraps terminal %s with the rejection sentinel", code), func() {
			fc, err := rejectedCreate(code, "targets a deleting cluster")
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, errBMICreateRejected)).To(BeTrue(), "sentinel missing from %v", err)
			Expect(status.Code(err)).To(Equal(code), "underlying code lost from %v", err)
			Expect(fc.names).To(Equal([]string{"slot"}), "create was not issued for the selected slot")
		})
	}
	It("does not wrap transport-class Create failures", func() {
		_, err := rejectedCreate(codes.Unavailable, "outage")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errBMICreateRejected)).To(BeFalse(), "transport failure marked as rejection: %v", err)
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
	})
	It("does not wrap the AlreadyExists recovery path", func() {
		r, fc, co := nodeSetHarness("rejection-already-exists", nodeRequest("standard", 1))
		fc.createErr = status.Error(codes.AlreadyExists, "exists")
		cluster := authoritativeTestCluster(context.Background(), r, co)
		_, _, err := r.ensureBMI(context.Background(), co, cluster, co.Spec.NodeRequests[0], "slot", nil, "", "", "")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errBMICreateRejected)).To(BeFalse(), "AlreadyExists recovery marked as rejection: %v", err)
	})
	It("keeps non-status Create failures unwrapped", func() {
		r, fc, co := nodeSetHarness("rejection-plain", nodeRequest("standard", 1))
		fc.failCreate = true
		cluster := authoritativeTestCluster(context.Background(), r, co)
		_, _, err := r.ensureBMI(context.Background(), co, cluster, co.Spec.NodeRequests[0], "slot", nil, "", "", "")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errBMICreateRejected)).To(BeFalse(), "interrupted create marked as rejection: %v", err)
	})
})
