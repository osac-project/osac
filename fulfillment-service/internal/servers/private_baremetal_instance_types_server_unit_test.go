/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestApplyBareMetalInstanceTypeUpdateRejectsUnknownMaskPath(t *testing.T) {
	compiler, err := masks.NewPathCompiler[*privatev1.BareMetalInstanceType]().
		SetLogger(slog.Default()).
		Build()
	require.NoError(t, err)

	server := &PrivateBareMetalInstanceTypesServer{
		generic: &GenericServer[*privatev1.BareMetalInstanceType]{
			pathCompiler:  compiler,
			pathCache:     make(map[string]*masks.Path[*privatev1.BareMetalInstanceType]),
			pathCacheLock: &sync.Mutex{},
		},
	}
	err = server.applyBareMetalInstanceTypeUpdate(
		privatev1.BareMetalInstanceType_builder{}.Build(),
		privatev1.BareMetalInstanceType_builder{}.Build(),
		&fieldmaskpb.FieldMask{Paths: []string{"spec.unknown_field"}},
	)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.ErrorContains(t, err, "unknown_field")
}
