/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package validation

import (
	"fmt"
	"strings"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ValidateBareMetalDiskImageArchitecture returns an actionable mismatch error
// containing only resource identities and their declared architectures.
func ValidateBareMetalDiskImageArchitecture(instanceType *privatev1.BareMetalInstanceType, diskImage *privatev1.DiskImage) error {
	cpu := instanceType.GetSpec().GetHardware().GetCpu().GetArchitecture()
	var required privatev1.Architecture
	switch cpu {
	case "amd64":
		required = privatev1.Architecture_ARCHITECTURE_AMD64
	case "arm64":
		required = privatev1.Architecture_ARCHITECTURE_ARM64
	case "s390x":
		required = privatev1.Architecture_ARCHITECTURE_S390X
	}
	for _, architecture := range diskImage.GetSpec().GetArchitecture() {
		if required != privatev1.Architecture_ARCHITECTURE_UNSPECIFIED && architecture == required {
			return nil
		}
	}
	names := map[privatev1.Architecture]string{
		privatev1.Architecture_ARCHITECTURE_AMD64: "amd64",
		privatev1.Architecture_ARCHITECTURE_ARM64: "arm64",
		privatev1.Architecture_ARCHITECTURE_S390X: "s390x",
	}
	supported := make([]string, 0, len(diskImage.GetSpec().GetArchitecture()))
	for _, architecture := range diskImage.GetSpec().GetArchitecture() {
		supported = append(supported, names[architecture])
	}
	typeName := instanceType.GetMetadata().GetName()
	if typeName == "" {
		typeName = instanceType.GetId()
	}
	imageName := diskImage.GetMetadata().GetName()
	if imageName == "" {
		imageName = diskImage.GetId()
	}
	return fmt.Errorf("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE: DiskImage '%s' supports [%s]; BareMetalInstanceType '%s' requires %s. Select a compatible DiskImage or BareMetalInstanceType",
		imageName, strings.Join(supported, ", "), typeName, cpu)
}
