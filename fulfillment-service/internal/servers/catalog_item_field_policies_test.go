/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func policyTestInt32(value int32) *int32 { return &value }

func policyTestSubnet(name string) *privatev1.SubnetLocalReference {
	return privatev1.SubnetLocalReference_builder{Name: name}.Build()
}

func policyTestSecurityGroup(name string) *privatev1.SecurityGroupLocalReference {
	return privatev1.SecurityGroupLocalReference_builder{Name: name}.Build()
}
