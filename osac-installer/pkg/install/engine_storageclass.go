/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// defaultStorageClassAnnotation marks a StorageClass as the cluster default.
const defaultStorageClassAnnotation = "storageclass.kubernetes.io/is-default-class"

// buildStorageClassDefaultCheck builds the "storageclass-default" engine: a
// default StorageClass must exist. Several OSAC components (PostgreSQL,
// Keycloak) create PersistentVolumeClaims without specifying a storage
// class and rely on the cluster default; without one, those PVCs stay
// Pending indefinitely.
func buildStorageClassDefaultCheck(entry matrixEntry) (Check, error) {
	severity, err := parseSeverity(entry.Severity)
	if err != nil {
		return Check{}, err
	}
	category, err := parseCategory(entry.Category)
	if err != nil {
		return Check{}, err
	}
	return Check{
		Name:        entry.ID,
		Description: "a default StorageClass exists",
		Severity:    severity,
		Category:    category,
		Run: func(ctx context.Context, clients *Clients) (Status, string) {
			classes, err := clients.Typed.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
			if err != nil {
				return Failed, fmt.Sprintf("failed to list StorageClasses: %v", err)
			}
			for _, class := range classes.Items {
				if class.Annotations[defaultStorageClassAnnotation] == "true" {
					return Pass, fmt.Sprintf("default StorageClass found: %s", class.Name)
				}
			}
			return Failed, "no default StorageClass found; some components (PostgreSQL, Keycloak) may fail to create PVCs"
		},
	}, nil
}
