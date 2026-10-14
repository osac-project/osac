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

package servers

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type computeCatalogItemsListerFunc func(context.Context, *publicv1.ComputeInstanceCatalogItemsListRequest) (*publicv1.ComputeInstanceCatalogItemsListResponse, error)

func (f computeCatalogItemsListerFunc) List(ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsListRequest) (*publicv1.ComputeInstanceCatalogItemsListResponse, error) {
	return f(ctx, request)
}

func TestCatalogPricingInputsListPagesOutput(t *testing.T) {
	item := publicv1.ComputeInstanceCatalogItem_builder{
		Id:       "item-1",
		Metadata: publicv1.Metadata_builder{Name: "vm-small"}.Build(),
		Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
			DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
				Locked: publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
			}.Build(),
			InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
				Locked: publicv1.InstanceTypeReference_builder{Name: "small"}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
	lister := computeCatalogItemsListerFunc(func(ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsListRequest) (*publicv1.ComputeInstanceCatalogItemsListResponse, error) {
		if !catalogPricingEnrichmentDisabled(ctx) {
			t.Fatal("catalog pricing inventory list did not suppress provider price lookups")
		}
		if request.GetOffset() != 0 || request.GetLimit() != catalogPricingInputSourcePageSize {
			t.Fatalf("source request offset/limit = %d/%d, want 0/%d", request.GetOffset(), request.GetLimit(), catalogPricingInputSourcePageSize)
		}
		return publicv1.ComputeInstanceCatalogItemsListResponse_builder{Size: 1, Total: 1, Items: []*publicv1.ComputeInstanceCatalogItem{item}}.Build(), nil
	})
	server, err := NewCatalogPricingInputsServer().SetLogger(newBillingTestLogger()).SetComputeInstanceCatalogItems(lister).Build()
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.List(context.Background(), publicv1.CatalogPricingInputsListRequest_builder{Limit: int32Ptr(1)}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if response.GetTotal() != 2 || response.GetSize() != 1 || response.GetItems()[0].GetDimension() != "image_ref" {
		t.Fatalf("unexpected first page: total=%d size=%d items=%v", response.GetTotal(), response.GetSize(), response.GetItems())
	}
	response, err = server.List(context.Background(), publicv1.CatalogPricingInputsListRequest_builder{Offset: int32Ptr(1), Limit: int32Ptr(1)}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if response.GetTotal() != 2 || response.GetSize() != 1 || response.GetItems()[0].GetDimension() != "instance_type" {
		t.Fatalf("unexpected second page: total=%d size=%d items=%v", response.GetTotal(), response.GetSize(), response.GetItems())
	}
}

func TestCatalogPricingInputsListReturnsEmptyWhenCatalogServicesAreDisabled(t *testing.T) {
	server, err := NewCatalogPricingInputsServer().SetLogger(newBillingTestLogger()).Build()
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.List(context.Background(), &publicv1.CatalogPricingInputsListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetTotal() != 0 || response.GetSize() != 0 || len(response.GetItems()) != 0 {
		t.Fatalf("got non-empty response for disabled catalog services: %v", response)
	}
}

func TestCatalogPricingInputsListRedactsCatalogReadErrors(t *testing.T) {
	lister := computeCatalogItemsListerFunc(func(context.Context, *publicv1.ComputeInstanceCatalogItemsListRequest) (*publicv1.ComputeInstanceCatalogItemsListResponse, error) {
		return nil, errors.New("private catalog response body")
	})
	server, err := NewCatalogPricingInputsServer().SetLogger(newBillingTestLogger()).SetComputeInstanceCatalogItems(lister).Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.List(context.Background(), &publicv1.CatalogPricingInputsListRequest{})
	if got := grpcstatus.Code(err); got != codes.Internal {
		t.Fatalf("status = %v, want INTERNAL; err=%v", got, err)
	}
	if err != nil && err.Error() == "private catalog response body" {
		t.Fatalf("catalog error was not redacted: %v", err)
	}
}

func TestCatalogPricingInputsListRejectsUnsupportedFilterAndOrder(t *testing.T) {
	server, err := NewCatalogPricingInputsServer().SetLogger(newBillingTestLogger()).Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"filter", "order"} {
		request := &publicv1.CatalogPricingInputsListRequest{}
		if field == "filter" {
			request.SetFilter("this.value == 'fedora-16'")
		} else {
			request.SetOrder("value")
		}
		_, err := server.List(context.Background(), request)
		if got := grpcstatus.Code(err); got != codes.InvalidArgument {
			t.Errorf("%s request status = %v, want INVALID_ARGUMENT; err=%v", field, got, err)
		}
	}
}

func TestCatalogPricingInputsListValidatesPagination(t *testing.T) {
	server, err := NewCatalogPricingInputsServer().SetLogger(newBillingTestLogger()).Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*publicv1.CatalogPricingInputsListRequest{
		publicv1.CatalogPricingInputsListRequest_builder{Offset: int32Ptr(-1)}.Build(),
		publicv1.CatalogPricingInputsListRequest_builder{Limit: int32Ptr(-1)}.Build(),
		publicv1.CatalogPricingInputsListRequest_builder{Limit: int32Ptr(catalogPricingInputMaxLimit + 1)}.Build(),
	} {
		_, err := server.List(context.Background(), request)
		if got := grpcstatus.Code(err); got != codes.InvalidArgument {
			t.Errorf("pagination status = %v, want INVALID_ARGUMENT; request=%v err=%v", got, request, err)
		}
	}

	response, err := server.List(context.Background(), publicv1.CatalogPricingInputsListRequest_builder{Limit: int32Ptr(0)}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if response.GetTotal() != 0 || response.GetSize() != 0 || len(response.GetItems()) != 0 {
		t.Fatalf("zero-limit response = %v, want empty response", response)
	}
}
