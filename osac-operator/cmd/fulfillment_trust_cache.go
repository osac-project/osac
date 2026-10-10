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

package main

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func fulfillmentTrustCacheOptions(sourceNamespace, sourceName, networkingNamespace string) cache.Options {
	namespaces := map[string]cache.Config{}
	if sourceNamespace != "" {
		namespaces[sourceNamespace] = cache.Config{
			FieldSelector: fields.OneTermEqualSelector("metadata.name", sourceName),
		}
	}
	if networkingNamespace != "" {
		// Network-manager discovery shares the ConfigMap informer. When both
		// consumers use one namespace, retain every ConfigMap there.
		namespaces[networkingNamespace] = cache.Config{}
	}
	return cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.ConfigMap{}: {
				Namespaces: namespaces,
			},
		},
	}
}
