//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package provider

// REST paths of the public API in the gateway, one constant per API type, shared by the resources and data sources.
// Note that the naming isn't fully consistent between services: most bare metal services spell 'baremetal' as one
// word, but the instance types use 'bare_metal'. See OSAC-7379.
const (
	bareMetalInstancesPath         = "/api/fulfillment/v1/baremetal_instances"
	bareMetalInstanceTypesPath     = "/api/fulfillment/v1/bare_metal_instance_types"
	bareMetalInstanceTemplatesPath = "/api/fulfillment/v1/baremetal_instance_templates"
	bareMetalInstanceCatalogsPath  = "/api/fulfillment/v1/baremetal_instance_catalog_items"
	diskImagesPath                 = "/api/fulfillment/v1/disk_images"
	secretsPath                    = "/api/fulfillment/v1/secrets"
)
