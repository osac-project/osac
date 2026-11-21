/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = DescribeMigration("Seed named role permissions", func() {
	It("populates the role catalog while preserving existing identities and unrelated data", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into roles (id, name, tenant, data)
			values ('custom-role', 'custom-role', 'shared', '{"spec":{"title":"Custom role"}}');
			create temporary table roles_before as table roles;
		`)
		Expect(err).ToNot(HaveOccurred())

		Expect(tool.Migrate(ctx, 152)).To(Succeed())

		var changed int
		Expect(conn.QueryRow(ctx, `
			select count(*) from roles_before previous
			left join roles current on current.id = previous.id
			where row(current.name, current.tenant, current.data #- '{spec,permissions}')
				is distinct from row(previous.name, previous.tenant, previous.data #- '{spec,permissions}')
		`).Scan(&changed)).To(Succeed())
		Expect(changed).To(BeZero())

		var customData string
		Expect(conn.QueryRow(ctx, `select data::text from roles where id = 'custom-role'`).
			Scan(&customData)).To(Succeed())
		Expect(customData).To(MatchJSON(`{"spec":{"title":"Custom role"}}`))

		rows, err := conn.Query(ctx, `select name, tenant, jsonb_array_length(data #> '{spec,permissions}')
			from roles where id <> 'custom-role'`)
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()
		placements := map[string]string{}
		for rows.Next() {
			var name, tenant string
			var permissions int
			Expect(rows.Scan(&name, &tenant, &permissions)).To(Succeed())
			Expect(permissions).To(BeNumerically(">", 0), "role %q", name)
			Expect(placements).ToNot(HaveKey(name))
			placements[name] = tenant
		}
		Expect(rows.Err()).ToNot(HaveOccurred())
		Expect(placements).To(Equal(map[string]string{
			"cloud-provider-admin":  "shared",
			"cloud-provider-reader": "shared",
			"tenant-admin":          "shared",
			"tenant-reader":         "shared",
			"tenant-user":           "shared",
			"tenant-idp-manager":    "shared",
			"tenant-automation":     "shared",
			"osac-csi-driver":       "system",
		}))
	})

	It("stores protobuf permission rules accepted by the running catalog", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
		Expect(err).ToNot(HaveOccurred())

		rows, err := conn.Query(ctx, `select name, data from roles`)
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()
		for rows.Next() {
			var name string
			var data []byte
			Expect(rows.Scan(&name, &data)).To(Succeed())
			role := &privatev1.Role{}
			Expect(protojson.Unmarshal(data, role)).To(Succeed(), "role %q", name)
			for _, rule := range role.GetSpec().GetPermissions() {
				Expect(permissionCatalog.ValidateRule(rule.GetService(), rule.GetMethods())).
					To(Succeed(), "role %q, service %q", name, rule.GetService())
			}
		}
		Expect(rows.Err()).ToNot(HaveOccurred())
	})

	It("lets tenant admins manage workloads and roles while limiting definition and platform access", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		var data []byte
		Expect(conn.QueryRow(ctx, `select data from roles where name = 'tenant-admin' and tenant = 'shared'`).
			Scan(&data)).To(Succeed())
		role := &privatev1.Role{}
		Expect(protojson.Unmarshal(data, role)).To(Succeed())
		permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
		Expect(err).ToNot(HaveOccurred())
		var methods []string
		for _, rule := range role.GetSpec().GetPermissions() {
			expanded, err := permissionCatalog.Expand(rule.GetService(), rule.GetMethods())
			Expect(err).ToNot(HaveOccurred())
			methods = append(methods, expanded...)
		}
		Expect(methods).To(ContainElements(
			"osac.public.v1.Clusters/Create",
			"osac.public.v1.Clusters/Delete",
			"osac.public.v1.Roles/Create",
			"osac.public.v1.Roles/Update",
			"osac.public.v1.Roles/Delete",
			"osac.public.v1.RoleBindings/Create",
			"osac.public.v1.RoleBindings/Update",
			"osac.public.v1.Tenants/List",
		))
		for _, service := range []string{"BareMetalInstanceTemplates", "ClusterTemplates", "ComputeInstanceTemplates"} {
			Expect(methods).To(ContainElements("osac.public.v1."+service+"/Get", "osac.public.v1."+service+"/List"))
			for _, method := range []string{"Create", "Update", "Delete"} {
				Expect(methods).ToNot(ContainElement("osac.public.v1." + service + "/" + method))
			}
		}
		for _, method := range []string{
			"osac.public.v1.Tenants/Create",
			"osac.public.v1.Tenants/Update",
			"osac.public.v1.Tenants/Delete",
			"osac.public.v1.FabricDomains/Create",
			"osac.public.v1.FabricDomains/Delete",
		} {
			Expect(methods).ToNot(ContainElement(method))
		}
		for _, method := range methods {
			Expect(method).To(HavePrefix("osac.public.v1."))
		}
	})
})
