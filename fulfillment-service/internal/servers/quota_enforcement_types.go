/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// quotaTypeLookup resolves the types a request references, from the database, in the request's
// transaction. It implements quota.TypeLookup, which has no error return: a database failure is
// remembered in failure, and the caller fails the request with an internal error instead of
// mistaking it for a missing type. A type of the resource's own tenant is preferred over one of
// the shared tenant, the way the usage counter's type join orders them.
type quotaTypeLookup struct {
	ctx      context.Context
	tenant   string
	enforcer *quotaEnforcer
	failure  error
}

// Type returns the message of the named type, or nil when no type of the kind carries the name.
func (l *quotaTypeLookup) Type(kind, name string) protoreflect.Message {
	message, table := l.enforcer.kindMessage[kind], l.enforcer.kindTables[kind]
	if message == "" || table == "" {
		return nil
	}
	tx, err := database.TxFromContext(l.ctx)
	if err != nil {
		l.failure = err
		return nil
	}
	var data []byte
	err = tx.QueryRow(l.ctx, fmt.Sprintf(
		`select data from %s
		where name = $1 and (tenant = $2 or tenant = 'shared')
		order by (tenant = $2) desc limit 1`, table), name, l.tenant,
	).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		l.failure = err
		return nil
	}
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(message)
	if err != nil {
		l.failure = err
		return nil
	}
	result := dynamicpb.NewMessage(descriptor.(protoreflect.MessageDescriptor))
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, result); err != nil {
		l.failure = err
		return nil
	}
	// The name lives in a column, not in the data document, so put it back before the price
	// reads it: a type's own dimension names itself, such as
	// bare_metal.instances.{metadata.name}.
	metadata := result.Descriptor().Fields().ByName("metadata")
	nameField := metadata.Message().Fields().ByName("name")
	result.Mutable(metadata).Message().Set(nameField, protoreflect.ValueOfString(name))
	return result
}
