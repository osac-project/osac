/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// validatePublishedAddOnOperatorGraph validates the complete dependency graph
// rooted at a published operator. Direct dependencies remain in the operator's
// serialized resource; no separate graph projection is maintained.
func (s *PrivateAddOnOperatorsServer) validatePublishedAddOnOperatorGraph(
	ctx context.Context, root *privatev1.AddOnOperator,
) (err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	defer tx.ReportError(&err)

	resolver := &addOnOperatorReferenceResolver{
		resource: s.publishedOperatorResolver,
		cache:    make(map[addOnOperatorReferenceCacheKey]*privatev1.AddOnOperator),
	}
	graph := newAddOnOperatorGraph(resolver.resolve)
	_, _, err = graph.resolveAndExpand(ctx, root.GetMetadata(), []*privatev1.AddOnOperatorReference{
		privatev1.AddOnOperatorReference_builder{Id: root.GetId()}.Build(),
	})
	return err
}

// lockAddOnOperatorDependencyGraph serializes graph mutations so validation
// observes a stable set of operator resources during the check.
func lockAddOnOperatorDependencyGraph(ctx context.Context) (err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	defer tx.ReportError(&err)

	return database.LockAddOnOperatorDependencyGraph(ctx, tx)
}
