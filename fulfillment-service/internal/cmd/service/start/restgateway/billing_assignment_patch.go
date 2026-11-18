/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
AS IS BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package restgateway

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type patchURLKey struct{}

func withPatchURL(next runtime.HandlerFunc) runtime.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, params map[string]string) {
		if r.Method == http.MethodPatch {
			r = r.WithContext(context.WithValue(r.Context(), patchURLKey{}, r.URL))
		}
		next(w, r, params)
	}
}

// The generated PATCH handlers infer a mask from the object body, then apply
// query parameters. Reject conflicting aliases and normalize only inferred masks.
func normalizeBillingAssignmentPatchMask(ctx context.Context, method string, req, reply any,
	conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	var mask *fieldmaskpb.FieldMask
	switch request := req.(type) {
	case *publicv1.TenantBillingAssignmentsUpdateRequest:
		mask = request.GetUpdateMask()
	case *privatev1.TenantBillingAssignmentsUpdateRequest:
		mask = request.GetUpdateMask()
	default:
		return invoke(ctx, method, req, reply, conn, opts...)
	}
	patchURL, ok := ctx.Value(patchURLKey{}).(*url.URL)
	if !ok {
		return invoke(ctx, method, req, reply, conn, opts...)
	}
	protoName, jsonName := explicitUpdateMaskAliases(patchURL.Query())
	if protoName && jsonName {
		// The gateway populates both aliases into the same mask in map iteration
		// order. Reject the query rather than forwarding that ambiguous result.
		return status.Error(codes.InvalidArgument, "query must not specify both update_mask and updateMask")
	}
	if len(mask.GetPaths()) > 0 && !protoName && !jsonName {
		const atomicPath = "spec.billing_account"
		paths := mask.Paths[:0]
		seenAtomic := false
		for _, path := range mask.Paths {
			switch path {
			case atomicPath, atomicPath + ".id", atomicPath + ".name":
				if seenAtomic {
					continue
				}
				seenAtomic = true
				path = atomicPath
			}
			paths = append(paths, path)
		}
		mask.Paths = paths
	}
	return invoke(ctx, method, req, reply, conn, opts...)
}

func explicitUpdateMaskAliases(query url.Values) (protoName, jsonName bool) {
	for key := range query {
		// grpc-gateway also accepts bracketed query parameters for repeated fields.
		if i := strings.LastIndexByte(key, '['); i >= 0 && strings.HasSuffix(key, "]") {
			key = key[:i]
		}
		switch key {
		case "update_mask", "update_mask.paths":
			protoName = true
		case "updateMask", "updateMask.paths":
			jsonName = true
		}
	}
	return protoName, jsonName
}
