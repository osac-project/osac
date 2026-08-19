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

	"github.com/google/uuid"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
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
				Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
			}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State: state,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

// createDiskImageWithLifecycle seeds a DiskImage with the given lifecycle directly through
// the DAO as a shared/global image, so a tenancy-filtered DAO resolves it. Id and Name are
// set to the same value so a string default resolves whether treated as an id or a name.
// Note: unit tests call servers directly, so the gRPC reference-validation interceptor is
// not in the chain — existence is exercised by the handler's own lookup.
func createDiskImageWithLifecycle(
	name string,
	lifecycle privatev1.DiskImageLifecycle,
	deprecation *privatev1.DiskImageDeprecation,
) {
	diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	_, err = diskImagesDao.Create().SetObject(
		privatev1.DiskImage_builder{
			Id: name,
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				Lifecycle:   lifecycle,
				Deprecation: deprecation,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}
