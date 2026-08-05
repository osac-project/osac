/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package packages

import (
	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

// Names of frequently used API packages.
var (
	PublicV1  = string(publicv1.EventType_EVENT_TYPE_UNSPECIFIED.Descriptor().FullName().Parent())
	PrivateV1 = string(privatev1.EventType_EVENT_TYPE_UNSPECIFIED.Descriptor().FullName().Parent())
)

var Public = []string{
	PublicV1,
}

var Private = []string{
	PrivateV1,
}
