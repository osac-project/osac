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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFulfillmentTrustCacheOptionsOnlyWatchesSourceBundle(t *testing.T) {
	options := fulfillmentTrustCacheOptions("osac", "custom-ca-bundle", "networking")
	config, found := configMapCache(options.ByObject)
	if !found {
		t.Fatal("ConfigMap cache configuration is missing")
	}
	if len(config.Namespaces) != 2 {
		t.Fatalf("ConfigMap cache namespaces = %v, want osac and networking", config.Namespaces)
	}
	if _, found := config.Namespaces["osac"]; !found {
		t.Fatalf("ConfigMap cache namespaces = %v, want only osac", config.Namespaces)
	}
	if got := config.Namespaces["osac"].FieldSelector.String(); got != "metadata.name=custom-ca-bundle" {
		t.Fatalf("ConfigMap cache field selector = %q, want metadata.name=custom-ca-bundle", got)
	}
	if got := config.Namespaces["networking"].FieldSelector; got != nil {
		t.Fatalf("networking ConfigMap cache field selector = %v, want no selector", got)
	}
}

func TestFulfillmentTrustCacheOptionsPreservesNetworkingConfigMapsInSharedNamespace(t *testing.T) {
	options := fulfillmentTrustCacheOptions("osac", "ca-bundle", "osac")
	config, found := configMapCache(options.ByObject)
	if !found {
		t.Fatal("ConfigMap cache configuration is missing")
	}
	if got := config.Namespaces["osac"].FieldSelector; got != nil {
		t.Fatalf("shared ConfigMap cache field selector = %v, want no selector", got)
	}
}

func configMapCache(objects map[client.Object]cache.ByObject) (cache.ByObject, bool) {
	for object, config := range objects {
		if _, ok := object.(*corev1.ConfigMap); ok {
			return config, true
		}
	}
	return cache.ByObject{}, false
}
