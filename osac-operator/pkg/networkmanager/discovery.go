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

package networkmanager

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Discovery provides methods to find registered network managers from labeled ConfigMaps.
type Discovery struct {
	reader    client.Reader
	namespace string
}

// NewDiscovery creates a Discovery that looks for manager ConfigMaps in the given namespace.
// Returns an error if namespace is empty.
func NewDiscovery(reader client.Reader, namespace string) (*Discovery, error) {
	if namespace == "" {
		return nil, fmt.Errorf("networkmanager: namespace must not be empty")
	}
	return &Discovery{
		reader:    reader,
		namespace: namespace,
	}, nil
}

// ListFabricManagers returns all registered fabric managers in the configured namespace.
func (d *Discovery) ListFabricManagers(ctx context.Context) ([]Manager, error) {
	return d.listManagers(ctx, LabelFabricManager, FabricManager)
}

// ListK8sManagers returns all registered k8s managers in the configured namespace.
func (d *Discovery) ListK8sManagers(ctx context.Context) ([]Manager, error) {
	return d.listManagers(ctx, LabelK8sManager, K8sManager)
}

// GetFabricManager finds a fabric manager by its data.name value.
// Returns an error if the manager is not found.
func (d *Discovery) GetFabricManager(ctx context.Context, name string) (*Manager, error) {
	return d.getManager(ctx, name, LabelFabricManager, FabricManager)
}

// GetK8sManager finds a k8s manager by its data.name value.
// Returns an error if the manager is not found.
func (d *Discovery) GetK8sManager(ctx context.Context, name string) (*Manager, error) {
	return d.getManager(ctx, name, LabelK8sManager, K8sManager)
}

// listManagers lists all ConfigMaps matching the given label and parses them as managers.
func (d *Discovery) listManagers(ctx context.Context, labelKey string, mtype ManagerType) ([]Manager, error) {
	cmList := &corev1.ConfigMapList{}
	err := d.reader.List(ctx, cmList,
		client.InNamespace(d.namespace),
		client.MatchingLabels{labelKey: "true"},
	)
	if err != nil {
		return nil, fmt.Errorf("listing %s manager ConfigMaps: %w", mtype, err)
	}

	seen := make(map[string]string, len(cmList.Items))
	managers := make([]Manager, 0, len(cmList.Items))
	for i := range cmList.Items {
		mgr, err := ParseConfigMap(&cmList.Items[i], mtype)
		if err != nil {
			return nil, err
		}
		if prev, exists := seen[mgr.Name]; exists {
			return nil, fmt.Errorf("duplicate %s manager name %q: registered by both %s and %s",
				mtype, mgr.Name, prev, cmList.Items[i].Name)
		}
		seen[mgr.Name] = cmList.Items[i].Name
		managers = append(managers, *mgr)
	}

	return managers, nil
}

// getManager finds a specific manager by name among ConfigMaps with the given label.
func (d *Discovery) getManager(ctx context.Context, name string, labelKey string, mtype ManagerType) (*Manager, error) {
	managers, err := d.listManagers(ctx, labelKey, mtype)
	if err != nil {
		return nil, err
	}

	for i := range managers {
		if managers[i].Name == name {
			return &managers[i], nil
		}
	}

	return nil, &ManagerNotFoundError{Name: name, Type: mtype}
}

// ManagerNotFoundError is returned when a requested manager name does not match
// any registered ConfigMap.
type ManagerNotFoundError struct {
	Name string
	Type ManagerType
}

func (e *ManagerNotFoundError) Error() string {
	return fmt.Sprintf("%s manager %q not found", e.Type, e.Name)
}

// IsManagerNotFound reports whether err is, or wraps, a ManagerNotFoundError.
func IsManagerNotFound(err error) bool {
	var target *ManagerNotFoundError
	return errors.As(err, &target)
}
