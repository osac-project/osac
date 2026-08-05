/*
Copyright (c) 2025 Red Hat Inc.

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

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

// catalogItemReferenceChecker checks whether the caller has any resources that reference a catalog item.
type catalogItemReferenceChecker interface {
	hasReference(ctx context.Context, catalogItemID string) (bool, error)
}

// daoReferenceChecker implements catalogItemReferenceChecker using a GenericDAO.
type daoReferenceChecker[Resource dao.Object] struct {
	resourceDao *dao.GenericDAO[Resource]
}

func (c *daoReferenceChecker[Resource]) hasReference(ctx context.Context, catalogItemID string) (bool, error) {
	filter := fmt.Sprintf("this.spec.catalog_item == %q", catalogItemID)
	response, err := c.resourceDao.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		return false, grpcstatus.Errorf(grpccodes.Internal, "failed to check resource references")
	}
	return response.GetTotal() > 0, nil
}
