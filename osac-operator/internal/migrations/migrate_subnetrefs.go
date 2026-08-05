/*
Copyright 2025.

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

package migrations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

const envComputeInstanceNamespace = "OSAC_COMPUTE_INSTANCE_NAMESPACE"

// migrateSubnetRefs migrates ComputeInstance CRs from the legacy spec.subnetRef
// field to spec.networkAttachments[0].subnetRef. It uses the unstructured client
// to read raw JSON from etcd, which retains the stored subnetRef even after the
// field is removed from the CRD schema.
//
// This function is idempotent: CRs that already have networkAttachments or lack
// subnetRef are skipped. It is safe for concurrent execution by multiple replicas.
func migrateSubnetRefs(ctx context.Context, c client.Client) error {
	log := ctrllog.FromContext(ctx).WithName("migrate-subnetrefs")
	namespace := os.Getenv(envComputeInstanceNamespace)

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "osac.openshift.io",
		Version: "v1alpha1",
		Kind:    "ComputeInstanceList",
	})

	listOpts := []client.ListOption{}
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}
	if err := c.List(ctx, list, listOpts...); err != nil {
		return fmt.Errorf("failed to list ComputeInstances for migration: %w", err)
	}

	migrated := 0
	for i := range list.Items {
		item := &list.Items[i]
		spec, _ := item.Object["spec"].(map[string]interface{})
		if spec == nil {
			continue
		}

		subnetRef, _ := spec["subnetRef"].(string)
		if subnetRef == "" {
			continue
		}

		if na, ok := spec["networkAttachments"]; ok {
			if naSlice, ok := na.([]interface{}); ok && len(naSlice) > 0 {
				continue
			}
		}

		mergePatch := map[string]interface{}{
			"spec": map[string]interface{}{
				"networkAttachments": []map[string]interface{}{
					{"subnetRef": subnetRef},
				},
			},
		}
		patchBytes, err := json.Marshal(mergePatch)
		if err != nil {
			return fmt.Errorf("failed to marshal patch for %s/%s: %w",
				item.GetNamespace(), item.GetName(), err)
		}

		if err := c.Patch(ctx, item, client.RawPatch(types.MergePatchType, patchBytes)); err != nil {
			return fmt.Errorf("failed to migrate %s/%s: %w",
				item.GetNamespace(), item.GetName(), err)
		}

		migrated++
		log.Info("Migrated ComputeInstance from subnetRef to networkAttachments",
			"namespace", item.GetNamespace(),
			"name", item.GetName(),
			"subnetRef", subnetRef,
		)
	}

	if migrated > 0 {
		log.Info("SubnetRef migration complete", "migrated", migrated)
	}
	return nil
}
