/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package references

import (
	"context"
	"fmt"
	"strconv"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
)

// errRefNotFound satisfies the IsNotFound() interface expected by the reference validator.
type errRefNotFound struct {
	identifier string
}

func (e *errRefNotFound) Error() string {
	return fmt.Sprintf("resource %q not found", e.identifier)
}

func (e *errRefNotFound) IsNotFound() bool {
	return true
}

// NewDAOLookupFunc creates a ReferenceLookupFunc backed by a GenericDAO. It queries the DAO
// using a CEL filter that matches by id or metadata.name and returns the resolved reference metadata.
func NewDAOLookupFunc[O dao.Object](d *dao.GenericDAO[O]) ReferenceLookupFunc {
	return func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
		var filter string
		switch {
		case id != "" && name != "":
			filter = fmt.Sprintf(
				"this.id == %s && this.metadata.name == %s",
				strconv.Quote(id), strconv.Quote(name),
			)
		case id != "":
			filter = fmt.Sprintf("this.id == %s", strconv.Quote(id))
		case name != "":
			filter = fmt.Sprintf("this.metadata.name == %s", strconv.Quote(name))
		default:
			return nil, &errRefNotFound{identifier: "(empty)"}
		}

		response, err := d.List().
			SetFilter(filter).
			SetLimit(1).
			Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("lookup failed: %w", err)
		}

		items := response.GetItems()
		if len(items) == 0 {
			identifier := name
			if identifier == "" {
				identifier = id
			}
			return nil, &errRefNotFound{identifier: identifier}
		}

		item := items[0]
		resolved := &ResolvedRef{
			ID: item.GetId(),
		}

		msg := item.ProtoReflect()
		metadataFD := msg.Descriptor().Fields().ByName("metadata")
		if metadataFD != nil {
			metadataMsg := msg.Get(metadataFD).Message()

			nameFD := metadataMsg.Descriptor().Fields().ByName("name")
			if nameFD != nil {
				resolved.Name = metadataMsg.Get(nameFD).String()
			}

			tenantFD := metadataMsg.Descriptor().Fields().ByName("tenant")
			if tenantFD != nil {
				resolved.Tenant = metadataMsg.Get(tenantFD).String()
			}

			projectFD := metadataMsg.Descriptor().Fields().ByName("project")
			if projectFD != nil {
				resolved.Project = metadataMsg.Get(projectFD).String()
			}
		}

		return resolved, nil
	}
}

// RegisterDAOLookup is a convenience that instantiates a DAO lookup and registers it on the
// validator in one call, using the protobuf full name of the reference message type.
func RegisterDAOLookup[O dao.Object](
	v *ReferenceValidator,
	fullName protoreflect.FullName,
	d *dao.GenericDAO[O],
) {
	v.Register(fullName, NewDAOLookupFunc(d))
}
