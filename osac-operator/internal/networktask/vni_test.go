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

package networktask

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func deletionMeta(name, uid string, deleting bool) metav1.ObjectMeta {
	meta := metav1.ObjectMeta{Name: name, Namespace: "osac-networking", UID: types.UID(uid)}
	if deleting {
		now := metav1.Now()
		meta.DeletionTimestamp = &now
		meta.Finalizers = []string{"osac.openshift.io/test"}
	}
	return meta
}

func vnWithL3(name, uid string, l3 int32, deleting bool) *v1alpha1.VirtualNetwork {
	return &v1alpha1.VirtualNetwork{
		ObjectMeta: deletionMeta(name, uid, deleting),
		Status:     v1alpha1.VirtualNetworkStatus{L3VNI: l3},
	}
}

func subnetWithL2(name, uid string, l2 int32, deleting bool) *v1alpha1.Subnet {
	return &v1alpha1.Subnet{
		ObjectMeta: deletionMeta(name, uid, deleting),
		Status:     v1alpha1.SubnetStatus{NetworkSegment: &v1alpha1.NetworkSegment{Type: "evpn", L2VNI: l2}},
	}
}

func TestCheckVNIUnique(t *testing.T) {
	const reject = "VNI already in use"
	tests := []struct {
		name   string
		objs   []client.Object
		vni    int32
		kind   VNIKind
		reject bool
		retry  bool
	}{
		{"l2 vs live subnet l2", []client.Object{subnetWithL2("a", "ua", 10100, false)}, 10100, VNIKindL2, true, false},
		{"l2 vs virtual network l3", []client.Object{vnWithL3("a", "ua", 10100, false)}, 10100, VNIKindL2, true, false},
		{"l3 vs virtual network l3", []client.Object{vnWithL3("a", "ua", 20001, false)}, 20001, VNIKindL3, true, false},
		{"l3 vs subnet l2", []client.Object{subnetWithL2("a", "ua", 20001, false)}, 20001, VNIKindL3, true, false},
		{"l2 vs deleting subnet only", []client.Object{subnetWithL2("a", "ua", 10100, true)}, 10100, VNIKindL2, false, true},
		{"l2 vs deleting and live subnets", []client.Object{subnetWithL2("a", "ua", 10100, true), subnetWithL2("b", "ub", 10100, false)}, 10100, VNIKindL2, true, false},
		{"itself", []client.Object{subnetWithL2("self", "me", 10100, false)}, 10100, VNIKindL2, false, false},
		{"no conflicts", []client.Object{subnetWithL2("a", "ua", 10101, false), vnWithL3("b", "ub", 20001, false)}, 10100, VNIKindL2, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tt.objs...).Build()
			err := CheckVNIUnique(context.Background(), c, "osac-networking", "me", tt.vni, tt.kind)
			var rej *RejectError
			var retry *RetryError
			switch {
			case tt.reject:
				if !errors.As(err, &rej) || rej.Reason != reject {
					t.Fatalf("want reject %q, got %v", reject, err)
				}
			case tt.retry:
				if !errors.As(err, &retry) || retry.Reason != "VNI held by a resource being deleted; retrying" {
					t.Fatalf("want retry, got %v", err)
				}
			case err != nil:
				t.Fatalf("want nil, got %v", err)
			}
		})
	}
}
