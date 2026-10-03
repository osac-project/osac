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

package trustadmission

import "time"

const (
	ConfigMapName            = "osac-fulfillment-ca"
	BundleDataKey            = "bundle.pem"
	TenantAnnotation         = "osac.openshift.io/tenant"
	OwnerReferenceAnnotation = "osac.openshift.io/owner-reference"
	BundleHashAnnotation     = "osac.openshift.io/fulfillment-ca-sha256"
)

type RecordKey struct {
	ClusterOrderUID string
	TenantNamespace string
	ConfigMapName   string
	BundleSHA256    string
}

type ExpectedBundle struct {
	Key            RecordKey
	Tenant         string
	OwnerReference string
	BundlePEM      []byte
	ExpiresAt      time.Time
}
