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
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	// TenantAnnotation carries the tenant API ID of a CR; osac_job_vars.tenant comes from it.
	TenantAnnotation = "osac.openshift.io/tenant"
	// apiVersion of every snapshot.
	snapshotAPIVersion = "osac.openshift.io/v1alpha1"
)

// IDLabel is the label holding a CR's API ID: osac.openshift.io/<lowercase kind>-uuid.
func IDLabel(kind string) string {
	return "osac.openshift.io/" + strings.ToLower(kind) + "-uuid"
}

// APIID returns the API ID of a CR from its osac.openshift.io/<kind>-uuid
// label. A NetworkAttachment's ID is its spec.target.id.
func APIID(obj client.Object, kind string) string {
	if na, ok := obj.(*v1alpha1.NetworkAttachment); ok {
		return na.Spec.Target.ID
	}
	return obj.GetLabels()[IDLabel(kind)]
}

// Snapshot builds the contract §4.5 snapshot of a CR:
// {apiVersion, kind, id, metadata{name, namespace, uid, generation}, spec}.
// spec is the CR's spec struct; only the listed camelCase fields are kept, so
// no other field (for example a secret) reaches a manager. With no fields the
// snapshot spec is {}.
func Snapshot(obj client.Object, kind string, spec any, fields ...string) (map[string]any, error) {
	picked := map[string]any{}
	if len(fields) > 0 {
		data, err := json.Marshal(spec)
		if err != nil {
			return nil, fmt.Errorf("snapshot of %s %s: %w", kind, obj.GetName(), err)
		}
		all := map[string]any{}
		if err := json.Unmarshal(data, &all); err != nil {
			return nil, fmt.Errorf("snapshot of %s %s: %w", kind, obj.GetName(), err)
		}
		for _, f := range fields {
			if v, ok := all[f]; ok {
				picked[f] = v
			}
		}
	}
	return map[string]any{
		"apiVersion": snapshotAPIVersion,
		"kind":       kind,
		"id":         APIID(obj, kind),
		"metadata": map[string]any{
			"name":       obj.GetName(),
			"namespace":  obj.GetNamespace(),
			"uid":        string(obj.GetUID()),
			"generation": obj.GetGeneration(),
		},
		"spec": picked,
	}, nil
}

// NewInput starts a stored task input: {resource: <snapshot>, tenant}. tenant
// comes from the osac.openshift.io/tenant annotation and is left out when the
// annotation is absent or for an ExternalIPPool. Add the task's other keys
// (virtual_network, subnet, ipam, target, ...) to the returned map.
func NewInput(obj client.Object, kind string, resource map[string]any) map[string]any {
	input := map[string]any{"resource": resource}
	if tenant := obj.GetAnnotations()[TenantAnnotation]; tenant != "" && kind != "ExternalIPPool" {
		input["tenant"] = tenant
	}
	return input
}

// StoreInput writes the input to status.taskInput. Call it in the mutation
// passed as Request.BeforeLaunch so it lands in the same status write as the
// first Running entry, or alone for a SecurityGroup rule update.
func StoreInput(obj v1alpha1.NetworkInputObject, input map[string]any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("storing task input: %w", err)
	}
	obj.SetTaskInput(&runtime.RawExtension{Raw: data})
	return nil
}

// LoadInput returns the stored task input, or nil when none is stored.
func LoadInput(obj v1alpha1.NetworkInputObject) (map[string]any, error) {
	raw := obj.GetTaskInput()
	if raw == nil || len(raw.Raw) == 0 {
		return nil, nil
	}
	input := map[string]any{}
	if err := json.Unmarshal(raw.Raw, &input); err != nil {
		return nil, fmt.Errorf("reading stored task input: %w", err)
	}
	return input, nil
}

// WithLaunchKeys returns a copy of input with keys added at launch (for
// example network_type and network_segment, which are never stored).
func WithLaunchKeys(input map[string]any, keys map[string]any) map[string]any {
	out := make(map[string]any)
	for k, v := range input {
		out[k] = v
	}
	for k, v := range keys {
		out[k] = v
	}
	return out
}

// SegmentVars is the snake_case network_segment launch key.
func SegmentVars(segment *v1alpha1.NetworkSegment) map[string]any {
	if segment == nil {
		return nil
	}
	return map[string]any{
		"type":   string(segment.Type),
		"l2_vni": segment.L2VNI,
	}
}

// ManagerVars is osac_job_vars.manager.
func ManagerVars(ref *v1alpha1.NetworkManagerRef, manager v1alpha1.NetworkJobManager) map[string]any {
	return map[string]any{"name": ref.Name, "type": string(manager), "ansible_role": ref.AnsibleRole}
}
