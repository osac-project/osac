/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func cascadeTestProject(id string, deleting bool) *privatev1.Project {
	metadata := privatev1.Metadata_builder{}.Build()
	if deleting {
		metadata.SetDeletionTimestamp(timestamppb.Now())
	}
	return privatev1.Project_builder{Id: id, Metadata: metadata}.Build()
}

var _ = Describe("CascadeDeleteItems", func() {
	It("deletes active children and signals children already deleting", func() {
		items := []*privatev1.Project{
			cascadeTestProject("one", false),
			cascadeTestProject("two", true),
			cascadeTestProject("three", false),
		}
		var deleted, signaled []string

		pending, err := CascadeDeleteItems(context.Background(), CascadeDeleteItemsOptions[*privatev1.Project]{
			Items: items,
			Delete: func(_ context.Context, id string) error {
				deleted = append(deleted, id)
				return nil
			},
			Signal: func(_ context.Context, id string) error {
				signaled = append(signaled, id)
				return nil
			},
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(pending).To(BeTrue())
		Expect(deleted).To(Equal([]string{"one", "three"}))
		Expect(signaled).To(Equal([]string{"two"}))
	})

	It("treats NotFound from Delete as already gone", func() {
		pending, err := CascadeDeleteItems(context.Background(), CascadeDeleteItemsOptions[*privatev1.Project]{
			Items: []*privatev1.Project{cascadeTestProject("gone", false)},
			Delete: func(context.Context, string) error {
				return status.Error(codes.NotFound, "already gone")
			},
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(pending).To(BeFalse())
	})

	It("does not wait for a child that is gone when signaled", func() {
		pending, err := CascadeDeleteItems(context.Background(), CascadeDeleteItemsOptions[*privatev1.Project]{
			Items: []*privatev1.Project{cascadeTestProject("gone", true)},
			Delete: func(context.Context, string) error {
				Fail("Delete called for a child already being deleted")
				return nil
			},
			Signal: func(context.Context, string) error {
				return status.Error(codes.NotFound, "already gone")
			},
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(pending).To(BeFalse())
	})

	It("joins child errors and keeps deletion pending", func() {
		wantErr := errors.New("delete failed")
		pending, err := CascadeDeleteItems(context.Background(), CascadeDeleteItemsOptions[*privatev1.Project]{
			Items: []*privatev1.Project{cascadeTestProject("one", false), cascadeTestProject("two", false)},
			Delete: func(_ context.Context, id string) error {
				if id == "one" {
					return wantErr
				}
				return nil
			},
		})

		Expect(pending).To(BeTrue())
		Expect(errors.Is(err, wantErr)).To(BeTrue())
	})

	It("continues processing siblings after a failure", func() {
		wantErr := errors.New("delete failed")
		var deleted []string
		_, err := CascadeDeleteItems(context.Background(), CascadeDeleteItemsOptions[*privatev1.Project]{
			Items: []*privatev1.Project{cascadeTestProject("one", false), cascadeTestProject("two", false)},
			Delete: func(_ context.Context, id string) error {
				deleted = append(deleted, id)
				if id == "one" {
					return wantErr
				}
				return nil
			},
		})

		Expect(errors.Is(err, wantErr)).To(BeTrue())
		Expect(deleted).To(Equal([]string{"one", "two"}))
	})

	It("uses the protobuf descriptor for the resource name in errors", func() {
		resource := privatev1.ProjectMembership_builder{Id: "membership-1"}.Build()
		_, err := CascadeDeleteItems(context.Background(), CascadeDeleteItemsOptions[*privatev1.ProjectMembership]{
			Items: []*privatev1.ProjectMembership{resource},
			Delete: func(context.Context, string) error {
				return errors.New("delete failed")
			},
		})

		Expect(err).To(MatchError(ContainSubstring("failed to delete project membership membership-1")))
	})
})

var _ = Describe("CascadeDeleteAll", func() {
	It("drains a listing larger than one page in a single pass", func() {
		const total = 205
		items := make([]*privatev1.Project, 0, total)
		for i := 0; i < total; i++ {
			items = append(items, cascadeTestProject(fmt.Sprintf("item-%03d", i), false))
		}
		var deleted []string
		pending, err := CascadeDeleteAll(context.Background(), CascadeDeleteAllOptions[*privatev1.Project]{
			List: func(_ context.Context, _ string, limit, offset int32) ([]*privatev1.Project, int32, error) {
				// The whole listing drains before the first deletion, so one pass
				// covers a tenant with more rows than a page:
				Expect(deleted).To(BeEmpty())
				end := offset + limit
				if end > total {
					end = total
				}
				return items[offset:end], total, nil
			},
			Delete: func(_ context.Context, id string) error {
				deleted = append(deleted, id)
				return nil
			},
		})

		Expect(err).ToNot(HaveOccurred())
		// Every deletion was requested, so the parent stays pending:
		Expect(pending).To(BeTrue())
		Expect(deleted).To(HaveLen(total))
		Expect(deleted[0]).To(Equal("item-000"))
		Expect(deleted[total-1]).To(Equal("item-204"))
	})

	It("refuses a page that holds more items than its total", func() {
		_, err := CascadeDeleteAll(context.Background(), CascadeDeleteAllOptions[*privatev1.Project]{
			List: func(context.Context, string, int32, int32) ([]*privatev1.Project, int32, error) {
				return []*privatev1.Project{
					cascadeTestProject("one", false),
					cascadeTestProject("two", false),
				}, 1, nil
			},
			Delete: func(context.Context, string) error {
				Fail("Delete called for an invalid listing")
				return nil
			},
		})

		Expect(err).To(MatchError("the listing returned an invalid page"))
	})

	It("refuses a listing whose total changes between pages", func() {
		var pages int
		_, err := CascadeDeleteAll(context.Background(), CascadeDeleteAllOptions[*privatev1.Project]{
			List: func(context.Context, string, int32, int32) ([]*privatev1.Project, int32, error) {
				pages++
				if pages == 1 {
					return []*privatev1.Project{cascadeTestProject("one", false)}, 2, nil
				}
				return []*privatev1.Project{cascadeTestProject("two", false)}, 3, nil
			},
			Delete: func(context.Context, string) error {
				Fail("Delete called for an inconsistent listing")
				return nil
			},
		})

		Expect(err).To(MatchError("the listing total changed while it was being read"))
	})

	It("refuses an empty page that leaves rows of the total unread", func() {
		var pages int
		_, err := CascadeDeleteAll(context.Background(), CascadeDeleteAllOptions[*privatev1.Project]{
			List: func(context.Context, string, int32, int32) ([]*privatev1.Project, int32, error) {
				pages++
				if pages == 1 {
					return []*privatev1.Project{cascadeTestProject("one", false)}, 2, nil
				}
				return nil, 2, nil
			},
			Delete: func(context.Context, string) error {
				Fail("Delete called for an incomplete listing")
				return nil
			},
		})

		Expect(err).To(MatchError("the listing returned an incomplete page"))
	})
})
