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
	"fmt"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// findSingletonNetworkClass returns the sole active NetworkClass. A deployment
// has one active NetworkClass; callers must not recover by selecting a default
// or by choosing an arbitrary item when the invariant is violated.
func findSingletonNetworkClass(
	ctx context.Context,
	networkClassDAO *dao.GenericDAO[*privatev1.NetworkClass],
) (*privatev1.NetworkClass, error) {
	response, err := networkClassDAO.List().
		SetFilter("!has(this.metadata.deletion_timestamp)").
		SetLimit(2).
		Do(ctx)
	if err != nil {
		return nil, err
	}
	if response.GetTotal() > 1 || len(response.GetItems()) > 1 {
		return nil, fmt.Errorf("multiple active NetworkClasses are configured")
	}
	if len(response.GetItems()) == 0 {
		return nil, nil
	}
	return response.GetItems()[0], nil
}
