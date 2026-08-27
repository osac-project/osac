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

var (
	osacExternalIPNameAnnotation              string = fmt.Sprintf("%s/externalip-name", osacPrefix)
	osacExternalIPAttachmentIDLabel           string = fmt.Sprintf("%s/externalipattachment-uuid", osacPrefix)
	osacExternalIPAttachmentFeedbackFinalizer string = fmt.Sprintf("%s/externalipattachment-feedback", osacPrefix)
	osacExternalIPTargetIPAnnotation          string = fmt.Sprintf("%s/target-ip", osacPrefix)
	// osacVirtualNetworkNameAnnotation carries the tenant VirtualNetwork CR name to the
	// AAP job so the provisioning role can resolve the target network by name and
	// create the NAT rule in the correct tenant network scope.
	osacVirtualNetworkNameAnnotation string = fmt.Sprintf("%s/virtual-network-name", osacPrefix)
)
