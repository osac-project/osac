//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package quotausage

import (
	"io"
	"log/slog"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// quietLogger builds the logger the counter requires, without tying the statements to a writer.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// statementOf generates the statement of one kind, as the counter would run it.
func statementOf(counter *Counter, kind string) string {
	annotated, ok := counter.kinds[kind]
	ExpectWithOffset(1, ok).To(BeTrue(), "the quota registry has no kind '%s'", kind)
	statement, err := counter.kindStatement(annotated)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "failed to generate the statement of '%s'", kind)
	return statement
}

// oneline collapses a statement's whitespace, so goldens compare by content, not layout.
func oneline(statement string) string {
	return strings.Join(strings.Fields(statement), " ")
}

// The golden statement of each kind, with no counting rules configured. Regenerate by printing
// kindStatement's output when a golden fails, and change the golden together with the generator.
const (
	bareMetalInstanceStatement = `
	select p.project as project, p.dimension as dimension, greatest(p.amount - coalesce(e.amount,
	0), 0)::bigint as amount from ( select r.id as id, r.tenant as tenant, r.project::text as
	project, u.dimension as dimension, sum(u.amount)::bigint as amount from bare_metal_instances r
	left join lateral ( select t.name as name, t.data as data from bare_metal_instance_types t
	where t.name = (r.data #>> '{spec,instance_type,name}') and case when (r.data #>>
	'{spec,instance_type,tenant}') is not null and (r.data #>> '{spec,instance_type,tenant}') <>
	'' then t.tenant = (r.data #>> '{spec,instance_type,tenant}') and t.project::text =
	coalesce((r.data #>> '{spec,instance_type,project}'), '') else t.tenant = r.tenant or t.tenant
	= 'shared' end order by (t.tenant = r.tenant) desc, t.project::text limit 1 ) t2 on true cross
	join lateral ( select 'network.external_ips' as dimension, 1 as amount where (r.data #>>
	'{spec,auto_external_ip_attachment}') = 'true' union all select 'quota.type.missing:' ||
	(r.data #>> '{spec,instance_type,name}') as dimension, 1 as amount where (r.data #>>
	'{spec,instance_type,name}') is not null and t2.name is null union all select
	'bare_metal.instances.' || t2.name as dimension, 1 as amount ) u where r.tenant = $1 and
	u.dimension is not null and u.dimension <> '' group by r.id, r.tenant, r.project, u.dimension
	) p left join lateral ( select ex.dimension as dimension, sum(ex.amount)::bigint as amount
	from ( select d.dimension as dimension, d.amount as amount from external_ips v1 cross join
	lateral ( select 'network.external_ips' as dimension, 1 as amount ) d where v1.tenant =
	p.tenant and v1.creator = 'system' and v1.labels @>
	jsonb_build_object('osac.openshift.io/auto-created-for', p.id,
	'osac.openshift.io/auto-created-kind', 'bare_metal_instance') ) ex group by ex.dimension ) e
	on e.dimension = p.dimension
	`
	clusterStatement = `
	select p.project as project, p.dimension as dimension, greatest(p.amount - coalesce(e.amount,
	0), 0)::bigint as amount from ( select r.id as id, r.tenant as tenant, r.project::text as
	project, u.dimension as dimension, sum(u.amount)::bigint as amount from clusters r cross join
	lateral ( select 'caas.control_planes' as dimension, 1 as amount union all select
	'network.external_ips' as dimension, 2 as amount where (r.data #>>
	'{spec,auto_external_ip_attachment}') = 'true' union all select 'quota.type.missing:' ||
	(i2.val #>> '{baremetal_instance_type,name}') as dimension, 1 as amount from
	jsonb_each(coalesce(r.data #> '{spec,node_sets}', '{}'::jsonb)) as i2(k, val) left join
	lateral ( select t.name as name, t.data as data from bare_metal_instance_types t where t.name
	= (i2.val #>> '{baremetal_instance_type,name}') and case when (i2.val #>>
	'{baremetal_instance_type,tenant}') is not null and (i2.val #>>
	'{baremetal_instance_type,tenant}') <> '' then t.tenant = (i2.val #>>
	'{baremetal_instance_type,tenant}') and t.project::text = coalesce((i2.val #>>
	'{baremetal_instance_type,project}'), '') else t.tenant = r.tenant or t.tenant = 'shared' end
	order by (t.tenant = r.tenant) desc, t.project::text limit 1 ) t3 on true where (i2.val #>>
	'{baremetal_instance_type,name}') is not null and t3.name is null union all select
	'bare_metal.instances.' || t3.name as dimension, coalesce(((i2.val #>> '{size}'))::bigint, 0)
	* 1 as amount from jsonb_each(coalesce(r.data #> '{spec,node_sets}', '{}'::jsonb)) as i2(k,
	val) left join lateral ( select t.name as name, t.data as data from bare_metal_instance_types
	t where t.name = (i2.val #>> '{baremetal_instance_type,name}') and case when (i2.val #>>
	'{baremetal_instance_type,tenant}') is not null and (i2.val #>>
	'{baremetal_instance_type,tenant}') <> '' then t.tenant = (i2.val #>>
	'{baremetal_instance_type,tenant}') and t.project::text = coalesce((i2.val #>>
	'{baremetal_instance_type,project}'), '') else t.tenant = r.tenant or t.tenant = 'shared' end
	order by (t.tenant = r.tenant) desc, t.project::text limit 1 ) t3 on true ) u where r.tenant =
	$1 and u.dimension is not null and u.dimension <> '' group by r.id, r.tenant, r.project,
	u.dimension ) p left join lateral ( select ex.dimension as dimension, sum(ex.amount)::bigint
	as amount from ( select d.dimension as dimension, d.amount as amount from external_ips v1
	cross join lateral ( select 'network.external_ips' as dimension, 1 as amount ) d where
	v1.tenant = p.tenant and v1.creator = 'system' and v1.labels @>
	jsonb_build_object('osac.openshift.io/auto-created-for', p.id,
	'osac.openshift.io/auto-created-kind', 'cluster') union all select d.dimension as dimension,
	d.amount as amount from bare_metal_instances v4 left join lateral ( select t.name as name,
	t.data as data from bare_metal_instance_types t where t.name = (v4.data #>>
	'{spec,instance_type,name}') and case when (v4.data #>> '{spec,instance_type,tenant}') is not
	null and (v4.data #>> '{spec,instance_type,tenant}') <> '' then t.tenant = (v4.data #>>
	'{spec,instance_type,tenant}') and t.project::text = coalesce((v4.data #>>
	'{spec,instance_type,project}'), '') else t.tenant = v4.tenant or t.tenant = 'shared' end
	order by (t.tenant = v4.tenant) desc, t.project::text limit 1 ) t5 on true cross join lateral
	( select 'bare_metal.instances.' || t5.name as dimension, 1 as amount ) d where v4.tenant =
	p.tenant and v4.creator = 'system' and v4.data #>> '{status,cluster,id}' = p.id ) ex group by
	ex.dimension ) e on e.dimension = p.dimension
	`
	computeInstanceStatement = `	select p.project as project, p.dimension as dimension, greatest(p.amount - coalesce(e.amount,
	0), 0)::bigint as amount from ( select r.id as id, r.tenant as tenant, r.project::text as
	project, u.dimension as dimension, sum(u.amount)::bigint as amount from compute_instances r
	left join lateral ( select t.name as name, t.data as data from instance_types t where t.name =
	(r.data #>> '{spec,instance_type,name}') and case when (r.data #>>
	'{spec,instance_type,tenant}') is not null and (r.data #>> '{spec,instance_type,tenant}') <>
	'' then t.tenant = (r.data #>> '{spec,instance_type,tenant}') and t.project::text =
	coalesce((r.data #>> '{spec,instance_type,project}'), '') else t.tenant = r.tenant or t.tenant
	= 'shared' end order by (t.tenant = r.tenant) desc, t.project::text limit 1 ) t5 on true left
	join lateral ( select t.name as name, t.data as data from instance_types t where t.name =
	(r.data #>> '{status,instance_type,name}') and case when (r.data #>>
	'{status,instance_type,tenant}') is not null and (r.data #>> '{status,instance_type,tenant}')
	<> '' then t.tenant = (r.data #>> '{status,instance_type,tenant}') and t.project::text =
	coalesce((r.data #>> '{status,instance_type,project}'), '') else t.tenant = r.tenant or
	t.tenant = 'shared' end order by (t.tenant = r.tenant) desc, t.project::text limit 1 ) t6 on
	true cross join lateral ( select 'storage.volumes' as dimension, 1 * 1 as amount from
	jsonb_array_elements(coalesce(r.data #> '{spec,additional_disks,items}', '[]'::jsonb)) as
	i1(val) union all select 'storage.' || (i1.val #>> '{storage_tier,name}') || '.gib' as
	dimension, 1 * coalesce(((i1.val #>> '{size_gib}'))::bigint, 0) as amount from
	jsonb_array_elements(coalesce(r.data #> '{spec,additional_disks,items}', '[]'::jsonb)) as
	i1(val) union all select 'network.external_ips' as dimension, 1 as amount where (r.data #>>
	'{spec,auto_external_ip_attachment}') = 'true' union all select 'storage.volumes' as
	dimension, 1 * 1 as amount from jsonb_array_elements(case when r.data #> '{spec,boot_disk}' is
	null then '[]'::jsonb else jsonb_build_array(r.data #> '{spec,boot_disk}') end) as i4(val)
	union all select 'storage.' || (i4.val #>> '{storage_tier,name}') || '.gib' as dimension, 1 *
	coalesce(((i4.val #>> '{size_gib}'))::bigint, 0) as amount from jsonb_array_elements(case when
	r.data #> '{spec,boot_disk}' is null then '[]'::jsonb else jsonb_build_array(r.data #>
	'{spec,boot_disk}') end) as i4(val) union all select 'quota.type.missing:' || (r.data #>>
	'{spec,instance_type,name}') as dimension, 1 as amount where (r.data #>>
	'{spec,instance_type,name}') is not null and t5.name is null union all select
	'quota.type.missing:' || (r.data #>> '{status,instance_type,name}') as dimension, 1 as amount
	where (r.data #>> '{status,instance_type,name}') is not null and t6.name is null union all
	select 'compute.gpu.' || (t5.data #>> '{spec,gpu,resource_name}') as dimension,
	greatest(coalesce(((t5.data #>> '{spec,gpu,count}'))::bigint, 0), coalesce(((t6.data #>>
	'{spec,gpu,count}'))::bigint, 0)) as amount union all select 'compute.memory_gib' as
	dimension, greatest(coalesce(((t5.data #>> '{spec,memory_gib}'))::bigint, 0),
	coalesce(((t6.data #>> '{spec,memory_gib}'))::bigint, 0)) as amount union all select
	'compute.vcpus' as dimension, greatest(coalesce(((t5.data #>> '{spec,vcpus}'))::bigint, 0),
	coalesce(((t6.data #>> '{spec,vcpus}'))::bigint, 0)) as amount ) u where r.tenant = $1 and
	u.dimension is not null and u.dimension <> '' group by r.id, r.tenant, r.project, u.dimension
	) p left join lateral ( select ex.dimension as dimension, sum(ex.amount)::bigint as amount
	from ( select d.dimension as dimension, d.amount as amount from volumes v2 cross join lateral
	( select 'storage.volumes' as dimension, 1 as amount union all select 'storage.' || (v2.data
	#>> '{spec,storage_tier}') || '.gib' as dimension, coalesce(((v2.data #>>
	'{spec,size_gib}'))::bigint, 0) as amount ) d where v2.tenant = p.tenant and v2.creator =
	'system' and v2.name like 'ci-' || p.id || '-' || '%' union all select d.dimension as
	dimension, d.amount as amount from external_ips v3 cross join lateral ( select
	'network.external_ips' as dimension, 1 as amount ) d where v3.tenant = p.tenant and v3.creator
	= 'system' and v3.labels @> jsonb_build_object('osac.openshift.io/auto-created-for', p.id,
	'osac.openshift.io/auto-created-kind', 'compute_instance') ) ex group by ex.dimension ) e on
	e.dimension = p.dimension
	`
	externalIPStatement = `
	select r.project::text as project, u.dimension as dimension, sum(u.amount)::bigint as amount
	from external_ips r cross join lateral ( select 'network.external_ips' as dimension, 1 as
	amount ) u where r.tenant = $1 and u.dimension is not null and u.dimension <> '' group by
	r.project, u.dimension
	`
	volumeStatement = `
	select r.project::text as project, u.dimension as dimension, sum(u.amount)::bigint as amount
	from volumes r cross join lateral ( select 'storage.volumes' as dimension, 1 as amount union
	all select 'storage.' || (r.data #>> '{spec,storage_tier}') || '.gib' as dimension,
	coalesce(((r.data #>> '{spec,size_gib}'))::bigint, 0) as amount ) u where r.tenant = $1 and
	u.dimension is not null and u.dimension <> '' group by r.project, u.dimension
	`
)

var _ = Describe("Counter statements", func() {
	var counter *Counter

	BeforeEach(func() {
		var err error
		counter, err = NewCounter().SetLogger(quietLogger()).Build()
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("golden-compares the statement of each kind's price",
		func(kind, golden string) {
			Expect(oneline(statementOf(counter, kind))).To(Equal(oneline(golden)))
		},
		Entry("the bare-metal instance", "BareMetalInstance", bareMetalInstanceStatement),
		Entry("the cluster", "Cluster", clusterStatement),
		Entry("the compute instance", "ComputeInstance", computeInstanceStatement),
		Entry("the external IP", "ExternalIP", externalIPStatement),
		Entry("the volume", "Volume", volumeStatement),
	)

	Describe("the parts the design depends on", func() {
		It("keeps the trust gate and the three link shapes of the exists side", func() {
			cluster := oneline(statementOf(counter, "Cluster"))
			Expect(cluster).To(ContainSubstring("and v4.creator = 'system'"))
			computeInstance := oneline(statementOf(counter, "ComputeInstance"))
			Expect(computeInstance).To(ContainSubstring("and v2.name like 'ci-' || p.id || '-' || '%'"))
			Expect(cluster).To(ContainSubstring(
				"and v1.labels @> jsonb_build_object(" +
					"'osac.openshift.io/auto-created-for', p.id, " +
					"'osac.openshift.io/auto-created-kind', 'cluster')"))
			Expect(cluster).To(ContainSubstring("and v4.data #>> '{status,cluster,id}' = p.id"))
		})

		It("nets the ask against what exists", func() {
			cluster := oneline(statementOf(counter, "Cluster"))
			Expect(cluster).To(ContainSubstring(
				"greatest(p.amount - coalesce(e.amount, 0), 0)::bigint as amount"))
		})

		It("resolves a type by the reference's scope, falling back to the row's tenant", func() {
			computeInstance := oneline(statementOf(counter, "ComputeInstance"))
			// A reference that carries its type's tenant selects that row exactly, by
			// tenant and project, so a same-name type of another scope can't replace it:
			Expect(computeInstance).To(ContainSubstring(
				"when (r.data #>> '{spec,instance_type,tenant}') is not null" +
					" and (r.data #>> '{spec,instance_type,tenant}') <> ''" +
					" then t.tenant = (r.data #>> '{spec,instance_type,tenant}')" +
					" and t.project::text = coalesce((r.data #>> '{spec,instance_type,project}'), '')"))
			// A name-only reference falls back to the row's tenant, or the shared tenant,
			// and the pick is deterministic when two projects hold the same name:
			Expect(computeInstance).To(ContainSubstring(
				"else t.tenant = r.tenant or t.tenant = 'shared' end" +
					" order by (t.tenant = r.tenant) desc, t.project::text limit 1"))
		})

		It("fails loudly when an asked type resolves to no row", func() {
			computeInstance := oneline(statementOf(counter, "ComputeInstance"))
			Expect(computeInstance).To(ContainSubstring(
				"select 'quota.type.missing:' || (r.data #>> '{spec,instance_type,name}') as dimension," +
					" 1 as amount where (r.data #>> '{spec,instance_type,name}') is not null" +
					" and t5.name is null"))
			cluster := oneline(statementOf(counter, "Cluster"))
			Expect(cluster).To(ContainSubstring(
				"select 'quota.type.missing:' || (i2.val #>> '{baremetal_instance_type,name}') as dimension," +
					" 1 as amount from jsonb_each"))
		})

		It("stops counting the mapped states in the parent's rows and the exists side", func() {
			withRules, err := NewCounter().
				SetLogger(quietLogger()).
				SetCountingRules(map[string][]string{
					"Cluster":           {"CLUSTER_STATE_FAILED"},
					"BareMetalInstance": {"BARE_METAL_INSTANCE_STATE_FAILED"},
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())
			clusterWithRules := oneline(statementOf(withRules, "Cluster"))
			Expect(clusterWithRules).To(ContainSubstring(
				"and coalesce(r.data #>> '{status,state}', '') not in ('CLUSTER_STATE_FAILED')"))
			Expect(clusterWithRules).To(ContainSubstring(
				"and coalesce(v4.data #>> '{status,state}', '')" +
					" not in ('BARE_METAL_INSTANCE_STATE_FAILED')"))
		})
	})
})
