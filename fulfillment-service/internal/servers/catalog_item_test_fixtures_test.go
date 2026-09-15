/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func seedComputeCatalogItemTemplate(ctx context.Context, tenant, project, id string) error {
	templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	if err != nil {
		return err
	}
	_, err = templatesDao.Create().SetObject(privatev1.ComputeInstanceTemplate_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Name:    "my-ci-template",
			Tenant:  tenant,
			Project: project,
		}.Build(),
		Title: "Catalog item test template",
	}.Build()).Do(ctx)
	return err
}

func seedClusterCatalogItemTemplate(ctx context.Context, tenant, project, id string) error {
	templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	if err != nil {
		return err
	}
	_, err = templatesDao.Create().SetObject(privatev1.ClusterTemplate_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Name:    "my-cluster-template",
			Tenant:  tenant,
			Project: project,
		}.Build(),
		Title: "Catalog item test template",
	}.Build()).Do(ctx)
	return err
}

func seedBareMetalCatalogItemTemplate(ctx context.Context, tenant, project, id string) error {
	templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	if err != nil {
		return err
	}
	_, err = templatesDao.Create().SetObject(privatev1.BareMetalInstanceTemplate_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Name:    "my-bare-metal-template",
			Tenant:  tenant,
			Project: project,
		}.Build(),
		Title: "Catalog item test template",
	}.Build()).Do(ctx)
	return err
}
