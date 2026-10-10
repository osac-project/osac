// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// cleanupAgentFaultClient omits Agents only from cached observation. Deletes
// still delegate to the real apiserver, including all caller preconditions.
// beforeDelete models a same-name recreation between authoritative List and Delete.
type cleanupAgentFaultClient struct {
	client.Client
	omitAgents    bool
	omittedLists  int
	beforeDelete  func()
	deleteErr     error
	deleteOptions *client.DeleteOptions
}

func (c *cleanupAgentFaultClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if agents, ok := list.(*unstructured.UnstructuredList); ok && agents.GetKind() == "AgentList" && c.omitAgents {
		c.omittedLists++
		agents.Items = nil
		return nil
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *cleanupAgentFaultClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if obj.GetObjectKind().GroupVersionKind() == agentGVK {
		c.deleteOptions = (&client.DeleteOptions{}).ApplyOptions(opts)
		if c.beforeDelete != nil {
			before := c.beforeDelete
			c.beforeDelete = nil
			before()
		}
		c.deleteErr = c.Client.Delete(ctx, obj, opts...)
		return c.deleteErr
	}
	return c.Client.Delete(ctx, obj, opts...)
}

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
