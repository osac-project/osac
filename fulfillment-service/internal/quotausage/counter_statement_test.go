/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package quotausage

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

// quietLogger builds the logger the counter requires, without tying this test to Ginkgo's writer.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// statementOf generates the statement of one kind, as the counter would run it.
func statementOf(t *testing.T, counter *Counter, kind string) string {
	t.Helper()
	annotated, ok := counter.kinds[kind]
	if !ok {
		t.Fatalf("the quota registry has no kind '%s'", kind)
	}
	statement, err := counter.kindStatement(annotated)
	if err != nil {
		t.Fatalf("failed to generate the statement of '%s': %v", kind, err)
	}
	return statement
}

// oneline collapses a statement's whitespace, so goldens compare by content, not layout.
func oneline(statement string) string {
	return strings.Join(strings.Fields(statement), " ")
}

// The golden statement of each kind, with no counting rules configured. Regenerate by printing
// kindStatement's output when a golden fails, and change the golden together with the generator.
const (
	clusterStatement = `
select
	p.project as project,
	p.dimension as dimension,
	greatest(p.amount - coalesce(e.amount, 0), 0)::bigint as amount
from (
	select
		r.id as id,
		r.tenant as tenant,
		r.project::text as project,
		u.dimension as dimension,
		sum(u.amount)::bigint as amount
	from clusters r
	cross join lateral (
		select 'caas.control_planes' as dimension, 1 as amount
		union all
		select 'network.external_ips' as dimension, 2 as amount
			where (r.data #>> '{spec,auto_external_ip_attachment}') = 'true'
		union all
		select 'bare_metal.instances.' || t3.name as dimension,
			coalesce(((i2.val #>> '{size}'))::bigint, 0) * 1 as amount
		from jsonb_each(coalesce(r.data #> '{spec,node_sets}', '{}'::jsonb)) as i2(k, val)
		left join lateral (
			select t.name as name, t.data as data
			from bare_metal_instance_types t
			where t.name = (i2.val #>> '{baremetal_instance_type,name}')
				and (t.tenant = r.tenant or t.tenant = 'shared')
			order by (t.tenant = r.tenant) desc
			limit 1
		) t3 on true
	) u
	where r.tenant = $1
		and u.dimension is not null
		and u.dimension <> ''
	group by r.id, r.tenant, r.project, u.dimension
) p
left join lateral (
	select
		ex.dimension as dimension,
		sum(ex.amount)::bigint as amount
	from (
		select d.dimension as dimension, d.amount as amount
		from external_ips v1
		cross join lateral (
			select 'network.external_ips' as dimension, 1 as amount
		) d
		where v1.tenant = p.tenant
			and v1.creator = 'system'
			and v1.labels ->> 'osac.openshift.io/auto-created-for' = p.id
			and v1.labels ->> 'osac.openshift.io/auto-created-kind' = 'cluster'
		union all
		select d.dimension as dimension, d.amount as amount
		from bare_metal_instances v4
		left join lateral (
			select t.name as name, t.data as data
			from bare_metal_instance_types t
			where t.name = (v4.data #>> '{spec,instance_type,name}')
				and (t.tenant = v4.tenant or t.tenant = 'shared')
			order by (t.tenant = v4.tenant) desc
			limit 1
		) t5 on true
		cross join lateral (
			select 'bare_metal.instances.' || t5.name as dimension, 1 as amount
		) d
		where v4.tenant = p.tenant
			and v4.creator = 'system'
			and v4.data #>> '{status,cluster,id}' = p.id
	) ex
	group by ex.dimension
) e on e.dimension = p.dimension
`

	computeInstanceStatement = `
select
	p.project as project,
	p.dimension as dimension,
	greatest(p.amount - coalesce(e.amount, 0), 0)::bigint as amount
from (
	select
		r.id as id,
		r.tenant as tenant,
		r.project::text as project,
		u.dimension as dimension,
		sum(u.amount)::bigint as amount
	from compute_instances r
	left join lateral (
		select t.name as name, t.data as data
		from instance_types t
		where t.name = (r.data #>> '{spec,instance_type,name}')
			and (t.tenant = r.tenant or t.tenant = 'shared')
		order by (t.tenant = r.tenant) desc
		limit 1
	) t5 on true
	left join lateral (
		select t.name as name, t.data as data
		from instance_types t
		where t.name = (r.data #>> '{status,instance_type,name}')
			and (t.tenant = r.tenant or t.tenant = 'shared')
		order by (t.tenant = r.tenant) desc
		limit 1
	) t6 on true
	cross join lateral (
		select 'storage.volumes' as dimension, 1 * 1 as amount
		from jsonb_array_elements(coalesce(r.data #> '{spec,additional_disks,items}', '[]'::jsonb)) as i1(val)
		union all
		select 'storage.' || (i1.val #>> '{storage_tier,name}') || '.gib' as dimension,
			1 * coalesce(((i1.val #>> '{size_gib}'))::bigint, 0) as amount
		from jsonb_array_elements(coalesce(r.data #> '{spec,additional_disks,items}', '[]'::jsonb)) as i1(val)
		union all
		select 'network.external_ips' as dimension, 1 as amount
			where (r.data #>> '{spec,auto_external_ip_attachment}') = 'true'
		union all
		select 'storage.volumes' as dimension, 1 * 1 as amount
		from jsonb_array_elements(case when r.data #> '{spec,boot_disk}' is null
			then '[]'::jsonb else jsonb_build_array(r.data #> '{spec,boot_disk}') end) as i4(val)
		union all
		select 'storage.' || (i4.val #>> '{storage_tier,name}') || '.gib' as dimension,
			1 * coalesce(((i4.val #>> '{size_gib}'))::bigint, 0) as amount
		from jsonb_array_elements(case when r.data #> '{spec,boot_disk}' is null
			then '[]'::jsonb else jsonb_build_array(r.data #> '{spec,boot_disk}') end) as i4(val)
		union all
		select 'compute.gpu.' || (t5.data #>> '{spec,gpu,resource_name}') as dimension,
			greatest(coalesce(((t5.data #>> '{spec,gpu,count}'))::bigint, 0),
				coalesce(((t6.data #>> '{spec,gpu,count}'))::bigint, 0)) as amount
		union all
		select 'compute.memory_gib' as dimension,
			greatest(coalesce(((t5.data #>> '{spec,memory_gib}'))::bigint, 0),
				coalesce(((t6.data #>> '{spec,memory_gib}'))::bigint, 0)) as amount
		union all
		select 'compute.vcpus' as dimension,
			greatest(coalesce(((t5.data #>> '{spec,vcpus}'))::bigint, 0),
				coalesce(((t6.data #>> '{spec,vcpus}'))::bigint, 0)) as amount
	) u
	where r.tenant = $1
		and u.dimension is not null
		and u.dimension <> ''
	group by r.id, r.tenant, r.project, u.dimension
) p
left join lateral (
	select
		ex.dimension as dimension,
		sum(ex.amount)::bigint as amount
	from (
		select d.dimension as dimension, d.amount as amount
		from volumes v2
		cross join lateral (
			select 'storage.volumes' as dimension, 1 as amount
			union all
			select 'storage.' || (v2.data #>> '{spec,storage_tier}') || '.gib' as dimension,
				coalesce(((v2.data #>> '{spec,size_gib}'))::bigint, 0) as amount
		) d
		where v2.tenant = p.tenant
			and v2.creator = 'system'
			and starts_with(v2.name, 'ci-' || p.id || '-')
		union all
		select d.dimension as dimension, d.amount as amount
		from external_ips v3
		cross join lateral (
			select 'network.external_ips' as dimension, 1 as amount
		) d
		where v3.tenant = p.tenant
			and v3.creator = 'system'
			and v3.labels ->> 'osac.openshift.io/auto-created-for' = p.id
			and v3.labels ->> 'osac.openshift.io/auto-created-kind' = 'compute_instance'
	) ex
	group by ex.dimension
) e on e.dimension = p.dimension
`

	bareMetalInstanceStatement = `
select
	p.project as project,
	p.dimension as dimension,
	greatest(p.amount - coalesce(e.amount, 0), 0)::bigint as amount
from (
	select
		r.id as id,
		r.tenant as tenant,
		r.project::text as project,
		u.dimension as dimension,
		sum(u.amount)::bigint as amount
	from bare_metal_instances r
	left join lateral (
		select t.name as name, t.data as data
		from bare_metal_instance_types t
		where t.name = (r.data #>> '{spec,instance_type,name}')
			and (t.tenant = r.tenant or t.tenant = 'shared')
		order by (t.tenant = r.tenant) desc
		limit 1
	) t2 on true
	cross join lateral (
		select 'network.external_ips' as dimension, 1 as amount
			where (r.data #>> '{spec,auto_external_ip_attachment}') = 'true'
		union all
		select 'bare_metal.instances.' || t2.name as dimension, 1 as amount
	) u
	where r.tenant = $1
		and u.dimension is not null
		and u.dimension <> ''
	group by r.id, r.tenant, r.project, u.dimension
) p
left join lateral (
	select
		ex.dimension as dimension,
		sum(ex.amount)::bigint as amount
	from (
		select d.dimension as dimension, d.amount as amount
		from external_ips v1
		cross join lateral (
			select 'network.external_ips' as dimension, 1 as amount
		) d
		where v1.tenant = p.tenant
			and v1.creator = 'system'
			and v1.labels ->> 'osac.openshift.io/auto-created-for' = p.id
			and v1.labels ->> 'osac.openshift.io/auto-created-kind' = 'bare_metal_instance'
	) ex
	group by ex.dimension
) e on e.dimension = p.dimension
`

	volumeStatement = `
select
	r.project::text as project,
	u.dimension as dimension,
	sum(u.amount)::bigint as amount
from volumes r
cross join lateral (
	select 'storage.volumes' as dimension, 1 as amount
	union all
	select 'storage.' || (r.data #>> '{spec,storage_tier}') || '.gib' as dimension,
		coalesce(((r.data #>> '{spec,size_gib}'))::bigint, 0) as amount
) u
where r.tenant = $1
	and u.dimension is not null
	and u.dimension <> ''
group by r.project, u.dimension
`

	externalIPStatement = `
select
	r.project::text as project,
	u.dimension as dimension,
	sum(u.amount)::bigint as amount
from external_ips r
cross join lateral (
	select 'network.external_ips' as dimension, 1 as amount
) u
where r.tenant = $1
	and u.dimension is not null
	and u.dimension <> ''
group by r.project, u.dimension
`
)

// TestKindStatements golden-compares the statement the counter generates for each kind's price.
func TestKindStatements(t *testing.T) {
	counter, err := NewCounter().SetLogger(quietLogger()).Build()
	if err != nil {
		t.Fatal(err)
	}
	goldens := map[string]string{
		"BareMetalInstance": bareMetalInstanceStatement,
		"Cluster":           clusterStatement,
		"ComputeInstance":   computeInstanceStatement,
		"ExternalIP":        externalIPStatement,
		"Volume":            volumeStatement,
	}
	for kind, golden := range goldens {
		t.Run(kind, func(t *testing.T) {
			got, want := oneline(statementOf(t, counter, kind)), oneline(golden)
			if got != want {
				t.Errorf("the statement of %s changed:\ngot:  %s\nwant: %s", kind, got, want)
			}
		})
	}
}

// TestStatementStructure pins the parts of the statements the design depends on: the trust gate
// and the three links of the exists side, the netting of the ask against what exists, and the
// type join's preference for the resource's own tenant. The counting rules parts come from a
// counter built with rules injected, so the state filter shows in the parent's own rows and in
// the exists side that nets against them.
func TestStatementStructure(t *testing.T) {
	counter, err := NewCounter().SetLogger(quietLogger()).Build()
	if err != nil {
		t.Fatal(err)
	}
	withRules, err := NewCounter().
		SetLogger(quietLogger()).
		SetCountingRules(map[string][]string{
			"Cluster":           {"CLUSTER_STATE_FAILED"},
			"BareMetalInstance": {"BARE_METAL_INSTANCE_STATE_FAILED"},
		}).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	cluster := oneline(statementOf(t, counter, "Cluster"))
	computeInstance := oneline(statementOf(t, counter, "ComputeInstance"))
	clusterWithRules := oneline(statementOf(t, withRules, "Cluster"))
	parts := []struct {
		name      string
		statement string
		part      string
	}{
		{"the system-creator trust gate", cluster, "and v4.creator = 'system'"},
		{"the ci- name prefix link", computeInstance, "and starts_with(v2.name, 'ci-' || p.id || '-')"},
		{"the id and kind label pair", cluster,
			"and v1.labels ->> 'osac.openshift.io/auto-created-for' = p.id" +
				" and v1.labels ->> 'osac.openshift.io/auto-created-kind' = 'cluster'"},
		{"the status.cluster id path", cluster, "and v4.data #>> '{status,cluster,id}' = p.id"},
		{"the netting of the ask against what exists", cluster,
			"greatest(p.amount - coalesce(e.amount, 0), 0)::bigint as amount"},
		{"the tenant-over-shared type preference", cluster,
			"and (t.tenant = r.tenant or t.tenant = 'shared') order by (t.tenant = r.tenant) desc limit 1"},
		{"the parent's own rows stop counting in the mapped states", clusterWithRules,
			"and coalesce(r.data #>> '{status,state}', '') not in ('CLUSTER_STATE_FAILED')"},
		{"the exists side stops counting in the created kind's states", clusterWithRules,
			"and coalesce(v4.data #>> '{status,state}', '') not in ('BARE_METAL_INSTANCE_STATE_FAILED')"},
	}
	for _, part := range parts {
		t.Run(part.name, func(t *testing.T) {
			if !strings.Contains(part.statement, part.part) {
				t.Errorf("the statement lacks %q:\n%s", part.part, part.statement)
			}
		})
	}
}
