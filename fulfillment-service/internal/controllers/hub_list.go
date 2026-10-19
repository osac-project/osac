/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package controllers

import (
	"context"
	"errors"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ListAllHubs fetches the complete hub catalog in bounded pages and rejects
// responses that would make a partial catalog look complete to a reconciler.
func ListAllHubs(ctx context.Context, client privatev1.HubsClient) ([]*privatev1.Hub, error) {
	limit := int32(100)
	var result []*privatev1.Hub
	var offset int32
	var expectedTotal *int32
	for {
		response, err := client.List(ctx, privatev1.HubsListRequest_builder{
			Offset: &offset,
			Limit:  &limit,
		}.Build())
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("hub search returned an empty response")
		}
		items := response.GetItems()
		total, size := response.GetTotal(), response.GetSize()
		if total < 0 || size < 0 || int64(size) != int64(len(items)) {
			return nil, errors.New("hub search returned an inconsistent page size or total")
		}
		if expectedTotal == nil {
			expectedTotal = &total
		} else if total != *expectedTotal {
			return nil, errors.New("hub search total changed between pages")
		}
		result = append(result, items...)
		if int64(len(result)) > int64(total) {
			return nil, errors.New("hub search returned more items than its total")
		}
		if int64(len(result)) == int64(total) {
			return result, nil
		}
		if len(items) == 0 {
			return nil, errors.New("hub search returned an incomplete page")
		}
		offset += size
	}
}
