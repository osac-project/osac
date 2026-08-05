package v1alpha1_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-operator/api/v1alpha1"
)

var _ = Describe("JobState", func() {
	DescribeTable("IsTerminal",
		func(state v1alpha1.JobState, expected bool) {
			Expect(state.IsTerminal()).To(Equal(expected))
		},
		Entry("pending is not terminal", v1alpha1.JobStatePending, false),
		Entry("waiting is not terminal", v1alpha1.JobStateWaiting, false),
		Entry("running is not terminal", v1alpha1.JobStateRunning, false),
		Entry("unknown is not terminal", v1alpha1.JobStateUnknown, false),
		Entry("succeeded is terminal", v1alpha1.JobStateSucceeded, true),
		Entry("failed is terminal", v1alpha1.JobStateFailed, true),
		Entry("canceled is terminal", v1alpha1.JobStateCanceled, true),
	)

	DescribeTable("IsSuccessful",
		func(state v1alpha1.JobState, expected bool) {
			Expect(state.IsSuccessful()).To(Equal(expected))
		},
		Entry("pending is not successful", v1alpha1.JobStatePending, false),
		Entry("waiting is not successful", v1alpha1.JobStateWaiting, false),
		Entry("running is not successful", v1alpha1.JobStateRunning, false),
		Entry("unknown is not successful", v1alpha1.JobStateUnknown, false),
		Entry("succeeded is successful", v1alpha1.JobStateSucceeded, true),
		Entry("failed is not successful", v1alpha1.JobStateFailed, false),
		Entry("canceled is not successful", v1alpha1.JobStateCanceled, false),
	)
})
