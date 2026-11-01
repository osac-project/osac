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

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// VNIKind says which VNI a result carries.
type VNIKind string

const (
	VNIKindL3 VNIKind = "l3"
	VNIKindL2 VNIKind = "l2"
)

// CheckVNIUnique checks a VNI the Fabric Manager returned against every other
// VirtualNetwork and Subnet CR in the namespace, read from the API server
// (EP #427 §4.9): no L2 VNI equals any live L2 or L3 VNI, and no two
// VirtualNetworks share an L3 VNI. A CR exists until its Fabric delete has
// succeeded. When every conflicting CR is being deleted the check is deferred
// (RetryError); otherwise it is final (RejectError). Call it under the VNI
// acceptance lock (Request.SerializeVNI).
func CheckVNIUnique(ctx context.Context, reader client.Reader, namespace, selfUID string, vni int32, kind VNIKind) error {
	var vns v1alpha1.VirtualNetworkList
	if err := reader.List(ctx, &vns, client.InNamespace(namespace)); err != nil {
		return err
	}
	var subnets v1alpha1.SubnetList
	if err := reader.List(ctx, &subnets, client.InNamespace(namespace)); err != nil {
		return err
	}
	live, deleting := 0, 0
	count := func(obj client.Object) {
		if string(obj.GetUID()) == selfUID {
			return
		}
		if obj.GetDeletionTimestamp() != nil {
			deleting++
			return
		}
		live++
	}
	for i := range vns.Items {
		if vns.Items[i].Status.L3VNI == vni {
			count(&vns.Items[i])
		}
	}
	for i := range subnets.Items {
		if seg := subnets.Items[i].Status.NetworkSegment; seg != nil && seg.L2VNI == vni {
			count(&subnets.Items[i])
		}
	}
	log := logf.FromContext(ctx).V(1)
	switch {
	case live > 0:
		log.Info("VNI conflict", "vni", vni, "kind", kind, "live", live, "deleting", deleting)
		return Reject("VNI already in use")
	case deleting > 0:
		log.Info("VNI held by a resource being deleted", "vni", vni, "kind", kind, "deleting", deleting)
		return &RetryError{Reason: "VNI held by a resource being deleted; retrying"}
	}
	return nil
}
