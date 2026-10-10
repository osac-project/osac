/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type finalizerClient struct {
	privatev1.TenantsClient
	update func(*privatev1.TenantsUpdateRequest) (*privatev1.TenantsUpdateResponse, error)
}

func (c finalizerClient) Update(_ context.Context, req *privatev1.TenantsUpdateRequest, _ ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
	return c.update(req)
}

func TestTenantBarrierPersistence(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict=%t", conflict), func(t *testing.T) {
			tenant := privatev1.Tenant_builder{
				Id:       "tenant",
				Metadata: privatev1.Metadata_builder{Finalizers: []string{"unrelated"}}.Build(),
			}.Build()
			calls := 0
			r := &function{
				maskCalculator: masks.NewCalculator().Build(),
				tenantsClient: finalizerClient{update: func(req *privatev1.TenantsUpdateRequest) (*privatev1.TenantsUpdateResponse, error) {
					calls++
					if !req.GetLock() {
						t.Fatal("finalizer update must be locked")
					}
					got := req.GetObject().GetMetadata().GetFinalizers()
					if !slices.Contains(got, finalizers.TenantLifecycle) || !slices.Contains(got, finalizers.TenantOnboarding) || !slices.Contains(got, "unrelated") {
						t.Fatalf("unexpected barriers: %v", got)
					}
					if conflict {
						return nil, status.Error(codes.Aborted, "version conflict")
					}
					return privatev1.TenantsUpdateResponse_builder{Object: req.GetObject()}.Build(), nil
				}},
			}
			// No provisioning dependencies are configured: initialization must return first.
			err := r.Run(context.Background(), tenant)
			if conflict && status.Code(err) != codes.Aborted {
				t.Fatalf("expected conflict, got %v", err)
			}
			if !conflict && err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("expected one locked update, got %d", calls)
			}
		})
	}
}

func TestCompletedTenantCleanup(t *testing.T) {
	tenant := privatev1.Tenant_builder{
		Metadata: privatev1.Metadata_builder{
			Finalizers:        []string{finalizers.TenantOnboarding, "unrelated"},
			DeletionTimestamp: timestamppb.Now(),
		}.Build(),
	}.Build()
	// An absent owned barrier must skip cleanup and must not trigger an Update.
	r := &function{maskCalculator: masks.NewCalculator().Build()}
	if err := r.Run(context.Background(), tenant); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(tenant.GetMetadata().GetFinalizers(), finalizers.TenantLifecycle) {
		t.Fatal("completed cleanup restored its barrier")
	}
}

func TestTenantCleanupBarriers(t *testing.T) {
	for _, otherPending := range []bool{false, true} {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprintf("other-pending=%t failure=%t", otherPending, failure), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				projects := NewMockProjectsClient(ctrl)
				r := &function{projectsClient: projects}
				if failure {
					projects.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("cleanup failed"))
				} else {
					projects.EXPECT().List(gomock.Any(), gomock.Any()).Return(privatev1.ProjectsListResponse_builder{}.Build(), nil).Times(2)
				}

				list := []string{finalizers.TenantLifecycle, "unrelated"}
				if otherPending {
					list = append(list, finalizers.TenantOnboarding)
				}
				tenant := privatev1.Tenant_builder{Metadata: privatev1.Metadata_builder{
					Finalizers: list, DeletionTimestamp: timestamppb.Now(),
				}.Build()}.Build()
				task := &task{r: r, tenant: tenant}
				err := task.delete(context.Background())
				if (err != nil) != failure {
					t.Fatalf("unexpected cleanup result: %v", err)
				}
				got := tenant.GetMetadata().GetFinalizers()
				if slices.Contains(got, finalizers.TenantLifecycle) != failure {
					t.Fatalf("owned barrier: %v", got)
				}
				if slices.Contains(got, finalizers.TenantOnboarding) != otherPending || !slices.Contains(got, "unrelated") {
					t.Fatalf("other barriers changed: %v", got)
				}
				if !failure {
					if err := task.delete(context.Background()); err != nil {
						t.Fatal(err)
					}
					if slices.Contains(tenant.GetMetadata().GetFinalizers(), finalizers.TenantLifecycle) {
						t.Fatal("barrier restored")
					}
				}
			})
		}
	}
}
