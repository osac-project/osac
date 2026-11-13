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

	mapvalidator "github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	stringvalidator "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// withLookup adapts a data source schema generated from a GET by identifier operation so that the object can also be
// resolved by name or by labels: the `id` attribute becomes optional, and `name` and `labels` attributes are added,
// with exactly one of the three required. The name and label lookups are resolved to the identifier by the data
// source implementation, which the generator can't do because there is no read-by-name or read-by-labels operation
// in the API.
func withLookup(generated schema.Schema, kind string) schema.Schema {
	identifier, ok := generated.Attributes["id"].(schema.StringAttribute)
	if !ok {
		// The generator always emits a string 'id' from the path parameter; if that ever changes, the data source
		// needs revisiting:
		panic("generated data source schema has no string 'id' attribute")
	}
	identifier.Required = false
	identifier.Optional = true
	identifier.Validators = lookupValidators()
	generated.Attributes["id"] = identifier
	generated.Attributes["name"] = schema.StringAttribute{
		Description: fmt.Sprintf("Name of the %s.", kind),
		Optional:    true,
		Validators:  lookupValidators(),
	}
	generated.Attributes["labels"] = schema.MapAttribute{
		Description: fmt.Sprintf(
			"Labels identifying the %s. The lookup returns the single object carrying all of them.", kind),
		Optional:    true,
		ElementType: types.StringType,
		Validators: []validator.Map{
			mapvalidator.ExactlyOneOf(
				path.MatchRoot("name"),
				path.MatchRoot("id"),
				path.MatchRoot("labels"),
			),
			mapvalidator.SizeAtLeast(1),
		},
	}
	return generated
}

// lookupValidators returns the validators shared by the `name` and `id` attributes.
func lookupValidators() []validator.String {
	return []validator.String{
		stringvalidator.ExactlyOneOf(
			path.MatchRoot("name"),
			path.MatchRoot("id"),
			path.MatchRoot("labels"),
		),
	}
}
