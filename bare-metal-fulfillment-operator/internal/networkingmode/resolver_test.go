/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package networkingmode

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"k8s.io/utils/ptr"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/controller"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeNetworkClasses struct {
	items []*privatev1.NetworkClass
	err   error
	calls int
}

func (f *fakeNetworkClasses) List(
	context.Context, *privatev1.NetworkClassesListRequest, ...grpc.CallOption,
) (*privatev1.NetworkClassesListResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.NetworkClassesListResponse_builder{Items: f.items}.Build(), nil
}

func class(mode *bool) *privatev1.NetworkClass {
	return privatev1.NetworkClass_builder{
		Id:   "nc-1",
		Spec: privatev1.NetworkClassSpec_builder{ProvisioningEnabled: mode}.Build(),
	}.Build()
}

func TestModeEnabled(t *testing.T) {
	resolver := NewResolver(&fakeNetworkClasses{items: []*privatev1.NetworkClass{class(ptr.To(true))}}, nil)
	enabled, err := resolver.Mode(context.Background())
	if err != nil {
		t.Fatalf("mode: %v", err)
	}
	if !enabled {
		t.Fatalf("enabled = false, want true")
	}
}

func TestModeDisabled(t *testing.T) {
	resolver := NewResolver(&fakeNetworkClasses{items: []*privatev1.NetworkClass{class(ptr.To(false))}}, nil)
	enabled, err := resolver.Mode(context.Background())
	if err != nil {
		t.Fatalf("mode: %v", err)
	}
	if enabled {
		t.Fatalf("enabled = true, want false")
	}
}

func TestModeIsCached(t *testing.T) {
	classes := &fakeNetworkClasses{items: []*privatev1.NetworkClass{class(ptr.To(false))}}
	resolver := NewResolver(classes, nil)
	for range 2 {
		if _, err := resolver.Mode(context.Background()); err != nil {
			t.Fatalf("mode: %v", err)
		}
	}
	if classes.calls != 1 {
		t.Fatalf("list calls = %d, want 1", classes.calls)
	}
}

func TestModeWithoutClassIsUnresolved(t *testing.T) {
	resolver := NewResolver(&fakeNetworkClasses{}, nil)
	_, err := resolver.Mode(context.Background())
	if !errors.Is(err, controller.ErrProvisioningModeUnresolved) {
		t.Fatalf("err = %v, want ErrProvisioningModeUnresolved", err)
	}
}

func TestModeWithoutModeIsUnresolved(t *testing.T) {
	resolver := NewResolver(&fakeNetworkClasses{items: []*privatev1.NetworkClass{class(nil)}}, nil)
	_, err := resolver.Mode(context.Background())
	if !errors.Is(err, controller.ErrProvisioningModeUnresolved) {
		t.Fatalf("err = %v, want ErrProvisioningModeUnresolved", err)
	}
}

func TestModeAPIFailureIsUnresolved(t *testing.T) {
	resolver := NewResolver(&fakeNetworkClasses{err: errors.New("unavailable")}, nil)
	_, err := resolver.Mode(context.Background())
	if !errors.Is(err, controller.ErrProvisioningModeUnresolved) {
		t.Fatalf("err = %v, want ErrProvisioningModeUnresolved", err)
	}
}

func TestConfiguredMismatchFails(t *testing.T) {
	resolver := NewResolver(&fakeNetworkClasses{items: []*privatev1.NetworkClass{class(ptr.To(false))}}, ptr.To(true))
	_, err := resolver.Mode(context.Background())
	if err == nil {
		t.Fatal("expected a mismatch error")
	}
	if !strings.Contains(err.Error(), "cannot change after installation") {
		t.Fatalf("message %q must explain the reinstall requirement", err.Error())
	}
}

func TestStaticMode(t *testing.T) {
	mode := controller.StaticProvisioningMode(true)
	enabled, err := mode.Mode(context.Background())
	if err != nil || !enabled {
		t.Fatalf("mode = %t, err = %v, want true, nil", enabled, err)
	}
}
