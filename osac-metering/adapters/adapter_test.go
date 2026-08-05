/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Error Types", func() {
	Describe("RetryableError", func() {
		It("wraps and unwraps the underlying error", func() {
			underlying := errors.New("connection timeout")
			err := &RetryableError{Err: underlying}

			Expect(err.Error()).To(Equal("connection timeout"))
			Expect(errors.Unwrap(err)).To(Equal(underlying))
		})

		It("can be detected with errors.As", func() {
			underlying := errors.New("temporary failure")
			err := &RetryableError{Err: underlying}

			var retryable *RetryableError
			Expect(errors.As(err, &retryable)).To(BeTrue())
		})
	})

	Describe("NonRetryableError", func() {
		It("wraps and unwraps the underlying error", func() {
			underlying := errors.New("malformed event")
			err := &NonRetryableError{Err: underlying}

			Expect(err.Error()).To(Equal("malformed event"))
			Expect(errors.Unwrap(err)).To(Equal(underlying))
		})

		It("can be detected with errors.As", func() {
			underlying := errors.New("permanent failure")
			err := &NonRetryableError{Err: underlying}

			var nonRetryable *NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		})

		It("is not detected as RetryableError", func() {
			err := &NonRetryableError{Err: errors.New("fail")}

			var retryable *RetryableError
			Expect(errors.As(err, &retryable)).To(BeFalse())
		})
	})
})
