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

	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

func createComputeInstanceInState(
	ctx context.Context,
	computeInstanceDao *dao.GenericDAO[*privatev1.ComputeInstance],
	state privatev1.ComputeInstanceState,
) *privatev1.ComputeInstance {
	resp, err := computeInstanceDao.Create().SetObject(
		privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "shared",
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				Template: "general.small",
			}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State: state,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}
