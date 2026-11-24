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
	"strings"

	"github.com/gobuffalo/flect"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// CascadeResource is a fulfillment resource with an ID and deletion metadata.
type CascadeResource interface {
	proto.Message
	GetId() string
	GetMetadata() *privatev1.Metadata
}

// CascadeDeleteItemsOptions describes how to process already-listed child resources.
// Listing stays with the caller because each controller has resource-specific filtering,
// pagination, and consistency requirements.
type CascadeDeleteItemsOptions[T CascadeResource] struct {
	Items  []T
	Delete func(context.Context, string) error
	Signal func(context.Context, string) error
}

// CascadeDeleteAllOptions describes how to list and delete every child resource a filter
// matches. The list function returns one page of the filter's results and the total number of
// matching rows.
type CascadeDeleteAllOptions[T CascadeResource] struct {
	Filter string
	List   func(ctx context.Context, filter string, limit, offset int32) (items []T, total int32, err error)
	Delete func(ctx context.Context, id string) error
	Signal func(ctx context.Context, id string) error
}

// CascadeDeleteAll lists every item the filter matches, in pages, deletes each one, and reports
// whether any item is still pending. It lists all pages before deleting, so a tenant with more
// rows than one page drains in a single pass.
func CascadeDeleteAll[T CascadeResource](
	ctx context.Context, options CascadeDeleteAllOptions[T]) (pending bool, err error) {
	items, err := dao.DrainPages(ctx, func(ctx context.Context, limit, offset int32) ([]T, int32, error) {
		return options.List(ctx, options.Filter, limit, offset)
	})
	if err != nil {
		return false, err
	}
	return CascadeDeleteItems(ctx, CascadeDeleteItemsOptions[T]{
		Items:  items,
		Delete: options.Delete,
		Signal: options.Signal,
	})
}

// CascadeDeleteItems processes each listed child. It treats NotFound as success, signals
// children already deleting when requested, and joins independent action errors.
// pending reports whether this pass requested or signaled a deletion.
func CascadeDeleteItems[T CascadeResource](ctx context.Context, options CascadeDeleteItemsOptions[T]) (pending bool, err error) {
	if options.Delete == nil {
		return false, errors.New("cascade deletion requires a delete function")
	}

	var actionErrors []error
	for _, item := range options.Items {
		id := item.GetId()
		resource := cascadeResourceName(item)
		metadata := item.GetMetadata()
		if metadata != nil && metadata.HasDeletionTimestamp() {
			if options.Signal == nil {
				pending = true
				continue
			}
			if signalErr := options.Signal(ctx, id); signalErr != nil {
				if status.Code(signalErr) == codes.NotFound {
					continue
				}
				pending = true
				actionErrors = append(actionErrors,
					fmt.Errorf("failed to signal %s %s: %w", resource, id, signalErr))
				continue
			}
			pending = true
			continue
		}

		if deleteErr := options.Delete(ctx, id); deleteErr != nil {
			if status.Code(deleteErr) == codes.NotFound {
				continue
			}
			pending = true
			actionErrors = append(actionErrors,
				fmt.Errorf("failed to delete %s %s: %w", resource, id, deleteErr))
			continue
		}
		pending = true
	}

	return pending, errors.Join(actionErrors...)
}

func cascadeResourceName(resource CascadeResource) string {
	name := string(resource.ProtoReflect().Descriptor().Name())
	return strings.ReplaceAll(flect.Underscore(name), "_", " ")
}
