/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package networkclass

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var (
	errNoLogger                    = errors.New("logger is mandatory")
	errNoConnection                = errors.New("client connection is mandatory")
	errNoHubCache                  = errors.New("hub cache is mandatory")
	canonicalHubStatusMessage      = "status.hub"
	canonicalHubStatusStateMessage = "status.state"
	canonicalHubStatusMessageField = "status.message"
)

// FunctionBuilder contains the dependencies needed to build a NetworkClass reconciler.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
	resolver   controllers.NetworkClassHubResolver
	client     controllers.NetworkClassStatusClient
}

// SetLogger sets the logger used by the reconciler.
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

// NewFunction creates a builder for a NetworkClass reconciler function.
func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

// SetConnection sets the fulfillment-service gRPC connection.
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

// SetHubCache sets the Hub client cache.
func (b *FunctionBuilder) SetHubCache(value controllers.HubCache) *FunctionBuilder {
	b.hubCache = value
	return b
}

// SetResolver injects the resolver used to reconcile canonical Hub status.
func (b *FunctionBuilder) SetResolver(value controllers.NetworkClassHubResolver) *FunctionBuilder {
	b.resolver = value
	return b
}

// SetNetworkClassesClient sets the status client used by the reconciler.
func (b *FunctionBuilder) SetNetworkClassesClient(value controllers.NetworkClassStatusClient) *FunctionBuilder {
	b.client = value
	return b
}

// Build creates a NetworkClass reconciliation function.
func (b *FunctionBuilder) Build() (controllers.ReconcilerFunction[*privatev1.NetworkClass], error) {
	if b.logger == nil {
		return nil, errNoLogger
	}
	resolver := b.resolver
	client := b.client
	if resolver == nil {
		if b.connection == nil {
			return nil, errNoConnection
		}
		if b.hubCache == nil {
			return nil, errNoHubCache
		}
		var err error
		client = privatev1.NewNetworkClassesClient(b.connection)
		resolver, err = controllers.NewNetworkingHubResolver().
			SetNetworkClassesClient(privatev1.NewNetworkClassesClient(b.connection)).
			SetHubsClient(privatev1.NewHubsClient(b.connection)).
			SetHubCache(b.hubCache).
			Build()
		if err != nil {
			return nil, err
		}
	}
	if client == nil {
		return nil, errors.New("network classes client is mandatory")
	}

	return (&function{resolver: resolver, client: client}).run, nil
}

type function struct {
	resolver controllers.NetworkClassHubResolver
	client   controllers.NetworkClassStatusClient
}

func (r *function) run(ctx context.Context, networkClass *privatev1.NetworkClass) error {
	if networkClass.HasMetadata() && networkClass.GetMetadata().HasDeletionTimestamp() {
		return nil
	}

	resolution, resolveErr := r.resolver.Resolve(ctx)
	if resolution.State == privatev1.NetworkClassState_NETWORK_CLASS_STATE_UNSPECIFIED {
		return resolveErr
	}
	statusErr := r.updateStatus(ctx, networkClass, resolution)
	if statusErr != nil && resolveErr != nil {
		return fmt.Errorf("%w; failed to update network class status", errors.Join(resolveErr, statusErr))
	}
	if statusErr != nil {
		return fmt.Errorf("failed to update network class status: %w", statusErr)
	}
	return resolveErr
}

func (r *function) updateStatus(
	ctx context.Context,
	networkClass *privatev1.NetworkClass,
	resolution controllers.NetworkingHubResolution,
) error {
	object := proto.Clone(networkClass).(*privatev1.NetworkClass)
	if !object.HasStatus() {
		object.SetStatus(&privatev1.NetworkClassStatus{})
	}
	status := object.GetStatus()
	// A resolution without a HubID means that no canonical Hub is currently
	// selectable (for example, when there are no Hubs or multiple active
	// Hubs). Clear any previously persisted assignment so status describes the
	// current resolution rather than a stale READY binding. Resolutions for an
	// explicitly configured canonical Hub always include its ID, including
	// FAILED and PENDING results, so those assignments remain visible.
	status.SetHub(resolution.HubID)
	status.SetState(resolution.State)
	if resolution.Message == "" {
		status.ClearMessage()
	} else {
		status.SetMessage(resolution.Message)
	}
	if networkClass.HasStatus() && proto.Equal(networkClass.GetStatus(), status) {
		return nil
	}

	_, err := r.client.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
		Object: object,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
			canonicalHubStatusMessage,
			canonicalHubStatusStateMessage,
			canonicalHubStatusMessageField,
		}},
		Lock: true,
	}.Build())
	return err
}
