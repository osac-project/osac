/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"fmt"
)

const (
	// Legacy LoadBalancer fallback constants — used until all strategies write the allocated-address annotation.
	externalIPServiceNamePrefix       = "osac-eip-"
	externalIPDefaultMetalLBNamespace = "metallb-system"
)

var (
	osacExternalIPIDLabel                    string = fmt.Sprintf("%s/externalip-uuid", osacPrefix)
	osacExternalIPFeedbackFinalizer          string = fmt.Sprintf("%s/externalip-feedback", osacPrefix)
	osacExternalIPTargetNamespaceAnnotation  string = fmt.Sprintf("%s/externalip-target-namespace", osacPrefix)
	osacExternalIPDetachFinalizer            string = fmt.Sprintf("%s/externalip-detach", osacPrefix)
	osacExternalIPAllocatedAddressAnnotation string = fmt.Sprintf("%s/allocated-address", osacPrefix)
)
