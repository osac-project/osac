/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provisioning

import (
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = ginkgo.Describe("Job helpers", func() {
	jobs := func(ids ...string) []v1alpha1.JobStatus {
		result := make([]v1alpha1.JobStatus, 0, len(ids))
		for _, id := range ids {
			result = append(result, v1alpha1.JobStatus{JobID: id, Type: v1alpha1.JobTypeProvision})
		}
		return result
	}

	ginkgo.Describe("FindJobByID", func() {
		ginkgo.It("returns nil for an empty slice", func() {
			Expect(FindJobByID(nil, "job-1")).To(BeNil())
		})

		ginkgo.It("returns nil when the ID is not found", func() {
			Expect(FindJobByID(jobs("job-1", "job-2"), "job-9")).To(BeNil())
		})

		ginkgo.It("returns a pointer into the slice when found", func() {
			list := jobs("job-1", "job-2")
			found := FindJobByID(list, "job-2")
			Expect(found).NotTo(BeNil())
			found.Message = "changed"
			Expect(list[1].Message).To(Equal("changed"))
		})
	})

	ginkgo.Describe("AppendJob", func() {
		ginkgo.It("appends to an empty slice", func() {
			Expect(AppendJob(nil, v1alpha1.JobStatus{JobID: "job-1"}, 3)).To(HaveLen(1))
		})

		ginkgo.It("keeps every job under the history limit", func() {
			list := AppendJob(jobs("job-1"), v1alpha1.JobStatus{JobID: "job-2"}, 3)
			Expect(list).To(HaveLen(2))
			Expect(list[1].JobID).To(Equal("job-2"))
		})

		ginkgo.It("drops the oldest jobs over the history limit", func() {
			list := jobs("job-1", "job-2", "job-3")
			list = AppendJob(list, v1alpha1.JobStatus{JobID: "job-4"}, 3)
			list = AppendJob(list, v1alpha1.JobStatus{JobID: "job-5"}, 3)
			Expect(list).To(HaveLen(3))
			Expect(list[0].JobID).To(Equal("job-3"))
			Expect(list[2].JobID).To(Equal("job-5"))
		})
	})

	ginkgo.Describe("UpdateJob", func() {
		ginkgo.It("returns false for an empty slice", func() {
			Expect(UpdateJob(nil, v1alpha1.JobStatus{JobID: "job-1"})).To(BeFalse())
		})

		ginkgo.It("returns false when the ID is not found", func() {
			Expect(UpdateJob(jobs("job-1"), v1alpha1.JobStatus{JobID: "job-9"})).To(BeFalse())
		})

		ginkgo.It("replaces every field of the matching job only", func() {
			list := jobs("job-1", "job-2")
			updated := v1alpha1.JobStatus{
				JobID:   "job-2",
				Type:    v1alpha1.JobTypeDeprovision,
				State:   v1alpha1.JobStateFailed,
				Message: "Failed with error",
			}
			Expect(UpdateJob(list, updated)).To(BeTrue())
			Expect(list[1]).To(Equal(updated))
			Expect(list[0].JobID).To(Equal("job-1"))
			Expect(list[0].Type).To(Equal(v1alpha1.JobTypeProvision))
		})
	})
})
