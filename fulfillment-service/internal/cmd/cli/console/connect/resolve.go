/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package connect

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/lookup"
)

// ResolveInstance resolves a name or ID to a compute instance ID.
func ResolveInstance(ctx context.Context, conn *grpc.ClientConn, key string) (string, error) {
	client := publicv1.NewComputeInstancesClient(conn)
	ci, err := lookup.Find(key, "compute instance", func(filter string, limit int32) ([]*publicv1.ComputeInstance, error) {
		resp, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to look up compute instance: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return "", err
	}
	return ci.GetId(), nil
}
