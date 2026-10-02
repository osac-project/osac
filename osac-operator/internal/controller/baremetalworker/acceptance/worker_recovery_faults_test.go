// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Faults belong to the test, not the production transport. Different IDs on
// repeated requests also ensure fake name uniqueness is not just ID equality.
type lostWorkerResponseClient struct {
	baremetalworker.FulfillmentClient
	lost       bool
	successful int
}

func (f *lostWorkerResponseClient) CreateBareMetalInstance(ctx context.Context, request *privatev1.BareMetalInstance) (*privatev1.BareMetalInstance, error) {
	request = proto.Clone(request).(*privatev1.BareMetalInstance)
	request.SetId(uuid.NewString())
	bmi, err := f.FulfillmentClient.CreateBareMetalInstance(ctx, request)
	if err != nil {
		return nil, err
	}
	f.successful++
	if !f.lost {
		f.lost = true
		return nil, status.Error(codes.DeadlineExceeded, "lost successful Create acknowledgement")
	}
	return bmi, nil
}

// Simulates an omitted first List followed by the AlreadyExists re-list. The
// invalid candidates need not be representable by a healthy unique-name store.
type workerRelistClient struct {
	baremetalworker.FulfillmentClient
	candidates       []*privatev1.BareMetalInstance
	visibleInitially bool
	lists            int
	creates          int
}

func (f *workerRelistClient) ListBareMetalInstances(_ context.Context, _ string) ([]*privatev1.BareMetalInstance, error) {
	f.lists++
	if f.lists == 1 && !f.visibleInitially {
		return nil, nil
	}
	return f.candidates, nil
}

func (f *workerRelistClient) CreateBareMetalInstance(_ context.Context, _ *privatev1.BareMetalInstance) (*privatev1.BareMetalInstance, error) {
	f.creates++
	return nil, status.Error(codes.AlreadyExists, "reserved name already exists")
}
