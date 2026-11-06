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

import (
	"fmt"
	"strings"

	stringvalidator "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// nameFilter builds the CEL filter that the fulfillment service uses to select objects by name.
func nameFilter(name string) string {
	return fmt.Sprintf("this.metadata.name == '%s'", name)
}

// requireOneOfNameOrID is the validator set shared by the `name` and `id` attributes of the lookup data sources:
// exactly one of them must be set.
func requireOneOfNameOrID() []validator.String {
	return []validator.String{
		stringvalidator.ExactlyOneOf(
			path.MatchRoot("name"),
			path.MatchRoot("id"),
		),
	}
}

// enumValue strips the enum type prefix from the name of a protobuf enum value, so that values in the Terraform
// state are short: `RUNNING` instead of `BARE_METAL_INSTANCE_STATE_RUNNING`.
func enumValue(prefix, value string) string {
	return strings.TrimPrefix(value, prefix+"_")
}
