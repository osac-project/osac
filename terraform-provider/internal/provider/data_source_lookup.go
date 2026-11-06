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
	"context"
	"fmt"

	stringvalidator "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// withNameLookup adapts a data source schema generated from a GET by identifier operation so that the object can
// also be resolved by name: the `id` attribute becomes optional and a `name` attribute is added, with exactly one of
// the two required. The name is resolved to the identifier by the data source implementation, which the generator
// can't do because there is no read-by-name operation in the API.
func withNameLookup(generated schema.Schema, kind string) schema.Schema {
	identifier, ok := generated.Attributes["id"].(schema.StringAttribute)
	if !ok {
		// The generator always emits a string 'id' from the path parameter; if that ever changes, the data source
		// needs revisiting:
		panic("generated data source schema has no string 'id' attribute")
	}
	identifier.Required = false
	identifier.Optional = true
	identifier.Validators = nameLookupValidators()
	generated.Attributes["id"] = identifier
	generated.Attributes["name"] = schema.StringAttribute{
		Description: fmt.Sprintf("Name of the %s.", kind),
		Optional:    true,
		Validators:  nameLookupValidators(),
	}
	return generated
}

// nameLookupValidators returns the validators shared by the `name` and `id` attributes.
func nameLookupValidators() []validator.String {
	return []validator.String{
		stringvalidator.ExactlyOneOf(
			path.MatchRoot("name"),
			path.MatchRoot("id"),
		),
	}
}

// lookupIdentifier resolves the identifier of a data source lookup: the identifier itself when given, or the name,
// using the provided lister, which must return the identifiers of all the objects with that name.
func lookupIdentifier(ctx context.Context, kind, name, id string,
	lister func(ctx context.Context) (ids []string, err error)) (string, error) {
	if id != "" {
		return id, nil
	}
	ids, err := lister(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to find %s: %w", kind, err)
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("expected exactly one %s named '%s', but found %d", kind, name, len(ids))
	}
	return ids[0], nil
}

// markSensitive marks the given attributes of a generated data source schema as sensitive. The generator can't
// express sensitivity for map attributes, so the data source glue marks them.
func markSensitive(generated schema.Schema, names ...string) schema.Schema {
	for _, name := range names {
		if attribute, ok := generated.Attributes[name].(schema.MapAttribute); ok {
			attribute.Sensitive = true
			generated.Attributes[name] = attribute
		}
	}
	return generated
}
