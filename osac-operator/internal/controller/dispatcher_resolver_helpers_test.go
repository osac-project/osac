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

package controller

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

// stubNetworkClassesClient is a minimal test double for privatev1.NetworkClassesClient
// used by controller tests exercising manager-resolution paths (e.g. the OSAC-1755
// capabilities reconciler). Embedding the interface (nil) satisfies methods that
// aren't stubbed for a given test, which are not expected to be called.
type stubNetworkClassesClient struct {
	privatev1.NetworkClassesClient
	getFunc    func(ctx context.Context, in *privatev1.NetworkClassesGetRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error)
	listFunc   func(ctx context.Context, in *privatev1.NetworkClassesListRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesListResponse, error)
	updateFunc func(ctx context.Context, in *privatev1.NetworkClassesUpdateRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesUpdateResponse, error)
}

func (s *stubNetworkClassesClient) Get(
	ctx context.Context, in *privatev1.NetworkClassesGetRequest, opts ...grpc.CallOption,
) (*privatev1.NetworkClassesGetResponse, error) {
	if s.getFunc != nil {
		return s.getFunc(ctx, in, opts...)
	}
	return s.NetworkClassesClient.Get(ctx, in, opts...)
}

func (s *stubNetworkClassesClient) List(
	ctx context.Context, in *privatev1.NetworkClassesListRequest, opts ...grpc.CallOption,
) (*privatev1.NetworkClassesListResponse, error) {
	if s.listFunc != nil {
		return s.listFunc(ctx, in, opts...)
	}
	return s.NetworkClassesClient.List(ctx, in, opts...)
}

func (s *stubNetworkClassesClient) Update(
	ctx context.Context, in *privatev1.NetworkClassesUpdateRequest, opts ...grpc.CallOption,
) (*privatev1.NetworkClassesUpdateResponse, error) {
	if s.updateFunc != nil {
		return s.updateFunc(ctx, in, opts...)
	}
	return s.NetworkClassesClient.Update(ctx, in, opts...)
}

// newListingNetworkClassClient returns a stub NetworkClassesClient whose List method
// returns the given NetworkClasses and whose Get method finds one by ID among them.
// Update appends to the captured updates slice so tests can assert on what was saved.
func newListingNetworkClassClient(
	items []*privatev1.NetworkClass, updates *[]*privatev1.NetworkClass,
) *stubNetworkClassesClient {
	return &stubNetworkClassesClient{
		listFunc: func(_ context.Context, _ *privatev1.NetworkClassesListRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesListResponse, error) {
			return &privatev1.NetworkClassesListResponse{Items: items}, nil
		},
		getFunc: func(_ context.Context, in *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
			for _, item := range items {
				if item.GetId() == in.GetId() {
					return &privatev1.NetworkClassesGetResponse{Object: item}, nil
				}
			}
			return nil, fmt.Errorf("network class %q not found", in.GetId())
		},
		updateFunc: func(_ context.Context, in *privatev1.NetworkClassesUpdateRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesUpdateResponse, error) {
			*updates = append(*updates, in.GetObject())
			return &privatev1.NetworkClassesUpdateResponse{Object: in.GetObject()}, nil
		},
	}
}

// newFabricManagerConfigMap builds a fabric-manager registration ConfigMap for tests
// that exercise dispatcher-backed manager discovery.
func newFabricManagerConfigMap(name, namespace, managerName string) *corev1.ConfigMap {
	return newManagerConfigMap(name, namespace, managerName, networkmanager.LabelFabricManager, "ipv4")
}

// newK8sManagerConfigMap builds a k8s-manager registration ConfigMap for tests that
// exercise dispatcher-backed manager discovery.
func newK8sManagerConfigMap(name, namespace, managerName, capabilities string) *corev1.ConfigMap {
	return newManagerConfigMap(name, namespace, managerName, networkmanager.LabelK8sManager, capabilities)
}

func newManagerConfigMap(name, namespace, managerName, labelKey, capabilities string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{labelKey: "true"},
		},
		Data: map[string]string{
			"name":         managerName,
			"capabilities": capabilities,
		},
	}
}
