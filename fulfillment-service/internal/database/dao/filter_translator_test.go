/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

var _ = Describe("Filter translator", func() {
	Describe("Shared query contract", func() {
		var translator *FilterTranslator

		BeforeEach(func() {
			var err error
			translator, err = NewFilterTranslator().SetLogger(logger).
				SetDescriptor((*testsv1.Object)(nil).ProtoReflect().Descriptor()).Build()
			Expect(err).ToNot(HaveOccurred())
		})

		DescribeTable("Rejects statically non-Boolean filters",
			func(ctx context.Context, filter string) {
				actual, err := translator.Translate(ctx, filter)
				Expect(err).To(BeAssignableToTypeOf(&ErrValidation{}))
				Expect(err.Error()).To(ContainSubstring("filter must return a Boolean"))
				Expect(actual).To(BeEmpty())
			},
			Entry("string that PostgreSQL could parse as true", `"true"`),
			Entry("string field", `this.id`),
			Entry("arithmetic", `this.my_int32 + 1`),
			Entry("array length", `this.my_string_list.size()`),
			Entry("timestamp", `now`),
			Entry("map entry", `this.metadata.labels['team']`),
			Entry("message", `this.status`),
			Entry("null", `null`),
		)
	})

	Describe("Construction", func() {
		It("Fails without a descriptor", func() {
			_, err := NewFilterTranslator().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
		})

		It("Succeeds with a descriptor", func() {
			_, err := NewFilterTranslator().
				SetLogger(logger).
				SetDescriptor((*testsv1.Object)(nil).ProtoReflect().Descriptor()).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Literal timestamp constructor", func() {
		var translator *FilterTranslator

		BeforeEach(func() {
			var err error
			translator, err = NewFilterTranslator().SetLogger(logger).
				SetDescriptor((*testsv1.Object)(nil).ProtoReflect().Descriptor()).Build()
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("translates validated instants through the existing string literal machinery",
			func(ctx context.Context, literal, instant string) {
				actual, err := translator.Translate(ctx, `this.status.status_timestamp >= timestamp(`+literal+`)`)
				Expect(err).NotTo(HaveOccurred())
				Expect(actual).To(Equal(`cast(data->'status'->>'status_timestamp' as timestamp with time zone) >= cast('` + instant + `' as timestamp with time zone)`))
			},
			Entry("UTC", `"2026-01-01T00:00:00Z"`, "2026-01-01T00:00:00Z"),
			Entry("positive offset", `"2026-01-01T03:30:00+03:30"`, "2026-01-01T00:00:00Z"),
			Entry("negative offset", `'2025-12-31T19:00:00-05:00'`, "2026-01-01T00:00:00Z"),
			Entry("fractions", `"2026-01-01T00:00:00.123456789Z"`, "2026-01-01T00:00:00.123456789Z"),
			Entry("epoch", `"1970-01-01T00:00:00Z"`, "1970-01-01T00:00:00Z"),
			Entry("subsecond epoch", `"1970-01-01T00:00:00.000001Z"`, "1970-01-01T00:00:00.000001Z"),
			Entry("minimum protobuf instant", `"0001-01-01T00:00:00Z"`, "0001-01-01T00:00:00Z"),
			Entry("maximum protobuf instant", `"9999-12-31T23:59:59.999999999Z"`, "9999-12-31T23:59:59.999999999Z"),
		)

		DescribeTable("rejects invalid and unsupported forms before producing SQL",
			func(ctx context.Context, constructor string) {
				actual, err := translator.Translate(ctx, `this.status.status_timestamp == `+constructor)
				Expect(err).To(HaveOccurred())
				Expect(actual).To(BeEmpty())
			},
			Entry("no arguments", `timestamp()`),
			Entry("two arguments", `timestamp("2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")`),
			Entry("integer epoch overload is not supported", `timestamp(0)`),
			Entry("boolean", `timestamp(true)`),
			Entry("null", `timestamp(null)`),
			Entry("double", `timestamp(1.5)`),
			Entry("bytes", `timestamp(b"2026-01-01T00:00:00Z")`),
			Entry("list", `timestamp(["2026-01-01T00:00:00Z"])`),
			Entry("field", `timestamp(this.my_string)`),
			Entry("concatenation is not evaluated", `timestamp("2026-01-01T" + "00:00:00Z")`),
			Entry("timestamp identity is not evaluated", `timestamp(now)`),
			Entry("empty", `timestamp("")`),
			Entry("date only", `timestamp("2026-01-01")`),
			Entry("no timezone", `timestamp("2026-01-01T00:00:00")`),
			Entry("single-digit hour", `timestamp("2026-01-01T0:00:00Z")`),
			Entry("comma fraction", `timestamp("2026-01-01T00:00:00,123Z")`),
			Entry("out-of-range offset hour", `timestamp("2026-01-01T00:00:00+24:00")`),
			Entry("out-of-range offset minute", `timestamp("2026-01-01T00:00:00-00:60")`),
			Entry("invalid calendar date", `timestamp("2026-02-30T00:00:00Z")`),
			Entry("before protobuf range", `timestamp("0000-01-01T00:00:00Z")`),
			Entry("offset outside protobuf range", `timestamp("0001-01-01T00:00:00+01:00")`),
			Entry("SQL-like text", `timestamp("2026-01-01T00:00:00Z'); drop table objects; --")`),
			Entry("CEL-like text", `timestamp("2026-01-01T00:00:00Z\") || true //")`),
		)

		It("compares nested status instants in real PostgreSQL, preserving optional status absence", func(ctx context.Context) {
			db, err := server.NewInstance().Build()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(db.Close)
			pool, err := db.Pool(ctx)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(pool.Close)
			// Nested JSON timestamps use RFC3339 literals and preserve absent parent/leaf semantics.
			for _, tc := range []struct {
				filter string
				ids    []string
			}{
				{`this.status.status_timestamp == timestamp("2026-01-01T03:30:00.123456+03:30")`, []string{"match"}},
				{`this.status.status_timestamp < timestamp("2026-01-01T00:00:00Z")`, []string{"before", "epoch", "fractional-epoch"}},
				{`this.status.status_timestamp > timestamp("2026-01-01T00:00:00.123456Z")`, []string{"after"}},
				{`this.status.status_timestamp == timestamp("1970-01-01T00:00:00Z")`, []string{"epoch"}},
				{`this.status.status_timestamp != timestamp("2026-01-01T00:00:00.123456Z")`, []string{"after", "before", "epoch", "fractional-epoch"}},
				{`has(this.status)`, []string{"after", "before", "empty-status", "epoch", "fractional-epoch", "match"}},
				{`!has(this.status)`, []string{"missing-status"}},
				{`has(this.status.status_timestamp)`, []string{"after", "before", "epoch", "fractional-epoch", "match"}},
				{`!has(this.status.status_timestamp)`, []string{"empty-status", "missing-status"}},
			} {
				translated, err := translator.Translate(ctx, tc.filter)
				Expect(err).NotTo(HaveOccurred(), tc.filter)
				rows, err := pool.Query(ctx, `select id from (
					select id, at::timestamptz as creation_timestamp,
					case id when 'empty-status' then '{"status":{}}'::jsonb
					when 'missing-status' then '{}'::jsonb
					else jsonb_build_object('status', jsonb_build_object('status_timestamp', at)) end as data
					from (values ('empty-status', $1::text), ('missing-status', $1), ('epoch', $1), ('fractional-epoch', $2),
					('before', $3), ('match', $4), ('after', $5)) as fixture(id, at)
				) as objects where `+translated+` order by id`,
					"1970-01-01T00:00:00Z", "1970-01-01T00:00:00.000001Z", "2025-01-01T00:00:00Z",
					"2026-01-01T00:00:00.123456Z", "2027-01-01T00:00:00Z")
				Expect(err).NotTo(HaveOccurred(), tc.filter)
				ids := []string{}
				for rows.Next() {
					var id string
					Expect(rows.Scan(&id)).To(Succeed())
					ids = append(ids, id)
				}
				Expect(rows.Err()).NotTo(HaveOccurred())
				rows.Close()
				Expect(ids).To(Equal(tc.ids), fmt.Sprintf("PostgreSQL filter: %s", tc.filter))
			}
		})
	})

	Describe("Reserved metadata filtering", func() {
		var publicTranslator, privateTranslator *FilterTranslator

		BeforeEach(func() {
			var err error
			publicTranslator, err = NewFilterTranslator().
				SetLogger(logger).
				SetDescriptor((*publicv1.Cluster)(nil).ProtoReflect().Descriptor()).
				Build()
			Expect(err).ToNot(HaveOccurred())

			privateTranslator, err = NewFilterTranslator().
				SetLogger(logger).
				SetDescriptor((*privatev1.Cluster)(nil).ProtoReflect().Descriptor()).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		DescribeTable("Rejects explicit references to reserved metadata keys",
			func(ctx context.Context, filter string) {
				_, err := publicTranslator.Translate(ctx, filter)
				Expect(err).To(MatchError(ContainSubstring("reserved metadata key")))
			},
			Entry("reserved label index", `this.metadata.labels['admin.osac.openshift.io/managed'] == 'system'`),
			Entry("reserved annotation index", `this.metadata.annotations['admin.osac.openshift.io/managed'] == 'system'`),
			Entry("reserved label membership", `'admin.osac.openshift.io/managed' in this.metadata.labels`),
			Entry("reserved subdomain membership", `'controller.admin.osac.openshift.io/managed' in this.metadata.annotations`),
			Entry("mixed nested predicate", `this.id == 'known' || this.metadata.labels['admin.osac.openshift.io/managed'] == 'system'`),
		)

		It("Keeps private metadata filtering and ordinary public keys available", func(ctx context.Context) {
			filter := `this.metadata.labels['admin.osac.openshift.io/managed'] == 'system'`
			translated, err := privateTranslator.Translate(ctx, filter)
			Expect(err).ToNot(HaveOccurred())
			Expect(translated).To(Equal(`labels @> '{"admin.osac.openshift.io/managed":"system"}'`))

			for _, key := range []string{
				`adminXosac.openshift.io/managed`,
				`admin.osac.openshift.io`,
				`osac.openshift.io/tenant`,
				`osac.openshift.io/owner-reference`,
			} {
				translated, err = publicTranslator.Translate(ctx, "this.metadata.labels['"+key+"'] == 'value'")
				Expect(err).ToNot(HaveOccurred(), "key %q should remain filterable", key)
				Expect(translated).To(ContainSubstring(`labels @>`))
			}
		})

	})

	Describe("Object translation", func() {
		var translator *FilterTranslator

		BeforeEach(func() {
			var err error

			translator, err = NewFilterTranslator().
				SetLogger(logger).
				SetDescriptor((*testsv1.Object)(nil).ProtoReflect().Descriptor()).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects a filter referencing a field absent from the configured descriptor", func(ctx context.Context) {
			restricted, err := NewFilterTranslator().
				SetLogger(logger).
				SetDescriptor((*testsv1.Status)(nil).ProtoReflect().Descriptor()).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = restricted.Translate(ctx, "this.my_bool")
			Expect(err).To(HaveOccurred())
		})

		DescribeTable(
			"Object translation",
			func(ctx context.Context, filter, expected string) {
				actual, err := translator.Translate(ctx, filter)
				Expect(err).ToNot(HaveOccurred())
				Expect(actual).To(Equal(expected))
			},
			Entry(
				"Escape string with single quotes",
				`this.id == 'my \'value\''`,
				`id = e'my \'value\''`,
			),
			Entry(
				"Field equals value",
				"this.id == 'my value'",
				"id = 'my value'",
			),
			Entry(
				"Field not equals value",
				"this.id != 'my value'",
				"id != 'my value'",
			),
			Entry(
				"Integer greater than literal",
				"this.my_int32 > 42",
				"cast(data->>'my_int32' as integer) > 42",
			),
			Entry(
				"String in list",
				"this.my_string in ['a', 'b', 'c']",
				"data->>'my_string' in ('a', 'b', 'c')",
			),
			Entry(
				"Calculated value in list",
				"(this.my_int32 + 1) in [123, 456]",
				"cast(data->>'my_int32' as integer) + 1 in (123, 456)",
			),
			Entry(
				"String contains",
				`this.my_string.contains("my value")`,
				`data->>'my_string' like '%my value%'`,
			),
			Entry(
				"Nested string",
				`this.spec.spec_string == 'my_value'`,
				`data->'spec'->>'spec_string' = 'my_value'`,
			),
			Entry(
				"Name equals value",
				`this.metadata.name == 'my_name'`,
				`name = 'my_name'`,
			),
			Entry(
				"Name not equals value",
				`this.metadata.name != 'my_name'`,
				`name != 'my_name'`,
			),
			Entry(
				"Creation timestamp is null",
				`this.metadata.creation_timestamp == null`,
				`creation_timestamp is null`,
			),
			Entry(
				"Creation timestamp is not null",
				`this.metadata.creation_timestamp != null`,
				`creation_timestamp is not null`,
			),
			Entry(
				"Deletion timestamp is null",
				`this.metadata.deletion_timestamp == null`,
				`nullif(deletion_timestamp, '1970-01-01 00:00:00Z') is null`,
			),
			Entry(
				"Deletion timestamp is not null",
				`this.metadata.deletion_timestamp != null`,
				`nullif(deletion_timestamp, '1970-01-01 00:00:00Z') is not null`,
			),
			Entry(
				"Timestamp in the past",
				`this.my_timestamp < now`,
				`cast(data->>'my_timestamp' as timestamp with time zone) < now()`,
			),
			Entry(
				"Timestamp in the future",
				`this.my_timestamp > now`,
				`cast(data->>'my_timestamp' as timestamp with time zone) > now()`,
			),
			Entry(
				"Reverse null and timestamp before null check",
				`null == this.my_timestamp`,
				`cast(data->>'my_timestamp' as timestamp with time zone) is null`,
			),
			Entry(
				"Reverse null and timestamp before not null check",
				`null != this.my_timestamp`,
				`cast(data->>'my_timestamp' as timestamp with time zone) is not null`,
			),
			Entry(
				"Check presence of identifier",
				`has(this.id)`,
				`true`,
			),
			Entry(
				"Check presence of name",
				`has(this.metadata.name)`,
				`true`,
			),
			Entry(
				"Check presence of creation timestamp",
				`has(this.metadata.creation_timestamp)`,
				`true`,
			),
			Entry(
				"Check presence of deletion timestamp",
				`has(this.metadata.deletion_timestamp)`,
				`deletion_timestamp != '1970-01-01 00:00:00Z'`,
			),
			Entry(
				"Boolean true check",
				`this.my_bool`,
				`coalesce(cast(data->>'my_bool' as bool), false)`,
			),
			Entry(
				"Boolean false check",
				`!this.my_bool`,
				`not coalesce(cast(data->>'my_bool' as bool), false)`,
			),
			Entry(
				"Boolean equality true",
				`this.my_bool == true`,
				`coalesce(cast(data->>'my_bool' as bool), false) = true`,
			),
			Entry(
				"Boolean equality false",
				`this.my_bool == false`,
				`coalesce(cast(data->>'my_bool' as bool), false) = false`,
			),
			Entry(
				"Check presence of boolean field",
				`has(this.my_bool)`,
				`data ? 'my_bool'`,
			),
			Entry(
				"Check presence of int32 field",
				`has(this.my_int32)`,
				`data ? 'my_int32'`,
			),
			Entry(
				"Check presence of int64 field",
				`has(this.my_int64)`,
				`data ? 'my_int64'`,
			),
			Entry(
				"Check presence of string field",
				`has(this.my_string)`,
				`data ? 'my_string'`,
			),
			Entry(
				"Check presence of float field",
				`has(this.my_float)`,
				`data ? 'my_float'`,
			),
			Entry(
				"Check presence of double field",
				`has(this.my_double)`,
				`data ? 'my_double'`,
			),
			Entry(
				"Check presence of timestamp field",
				`has(this.my_timestamp)`,
				`data ? 'my_timestamp'`,
			),
			Entry(
				"Check presence of message field",
				`has(this.spec)`,
				`data ? 'spec'`,
			),
			Entry(
				"Check presence of nested boolean field",
				`has(this.spec.spec_bool)`,
				`coalesce(data->'spec' ? 'spec_bool', false)`,
			),
			Entry(
				"Check presence of deeply nested field",
				`has(this.spec.spec_msg.my_string)`,
				`coalesce(data->'spec'->'spec_msg' ? 'my_string', false)`,
			),
			Entry(
				"String starts with",
				`this.my_string.startsWith("my")`,
				`data->>'my_string' like 'my%'`,
			),
			Entry(
				"String ends with",
				`this.my_string.endsWith("my")`,
				`data->>'my_string' like '%my'`,
			),
			Entry(
				"Escape percent in like pattern",
				`this.my_string.startsWith("my%")`,
				`data->>'my_string' like 'my\%%'`,
			),
			Entry(
				"Escape underscore in like pattern",
				`this.my_string.startsWith("my_")`,
				`data->>'my_string' like 'my\_%'`,
			),
			Entry(
				"Check if object is deleted",
				`!has(this.metadata.deletion_timestamp)`,
				`not deletion_timestamp != '1970-01-01 00:00:00Z'`,
			),
			Entry(
				"Filter by creator",
				`this.metadata.creator == 'my_user'`,
				`creator = 'my_user'`,
			),
			Entry(
				"Filter by tenant",
				`this.metadata.tenant == 'my_tenant'`,
				`tenant = 'my_tenant'`,
			),
			Entry(
				"Filter by project",
				`this.metadata.project == 'my_project'`,
				`project = 'my_project'`,
			),
			Entry(
				"Translates 'in' with empty list into false",
				"this.id in []",
				"false",
			),
			Entry(
				"Label equals value",
				`this.metadata.labels['mylabel'] == 'myvalue'`,
				`labels @> '{"mylabel":"myvalue"}'`,
			),
			Entry(
				"Label not equals value",
				`this.metadata.labels['mylabel'] != 'myvalue'`,
				`not (labels @> '{"mylabel":"myvalue"}')`,
			),
			Entry(
				"Filter by label key",
				`'mylabel' in this.metadata.labels`,
				`labels ? 'mylabel'`,
			),
			Entry(
				"String in repeated string field",
				`'myvalue' in this.my_string_list`,
				`data->'my_string_list' @> jsonb_build_array('myvalue')`,
			),
			Entry(
				"Integer in repeated int32 field",
				`42 in this.my_int32_list`,
				`data->'my_int32_list' @> jsonb_build_array(42)`,
			),
			Entry(
				"Integer in repeated int64 field",
				`99 in this.my_int64_list`,
				`data->'my_int64_list' @> jsonb_build_array(99)`,
			),
			Entry(
				"Boolean in repeated bool field",
				`true in this.my_bool_list`,
				`data->'my_bool_list' @> jsonb_build_array(true)`,
			),
			Entry(
				"Field reference in repeated string field",
				`this.my_string in this.my_string_list`,
				`data->'my_string_list' @> jsonb_build_array(data->>'my_string')`,
			),
			Entry(
				"Size of repeated string field",
				`this.my_string_list.size() == 0`,
				`coalesce(jsonb_array_length(data->'my_string_list'), 0) = 0`,
			),
			Entry(
				"Double-quoted string with single quote from %q",
				`this.id == "it's"`,
				`id = e'it\'s'`,
			),
			Entry(
				"Double-quoted string with escaped double quote from %q",
				`this.id == "say \"hello\""`,
				`id = 'say "hello"'`,
			),
			Entry(
				"Double-quoted string with backslash from %q",
				`this.id == "path\\to\\thing"`,
				`id = e'path\\to\\thing'`,
			),
			Entry(
				"LIKE backslash and wildcards retain both escape layers",
				`this.my_string.startsWith("path\\%_tail")`,
				`data->>'my_string' like e'path\\\\\\%\\_tail%'`,
			),
			Entry(
				"LIKE wildcards stay literal when a quote requires SQL escapes",
				`this.my_string.contains("it's%_tail")`,
				`data->>'my_string' like e'%it\'s\\%\\_tail%'`,
			),
			Entry(
				"LIKE non-ASCII characters are escaped",
				`this.my_string.contains("café")`,
				`data->>'my_string' like e'%caf\u00e9%'`,
			),
			Entry(
				"Double-quoted string with CEL injection attempt from %q",
				`this.id == "'] || true || this.id in ['"`,
				`id = e'\'] || true || this.id in [\''`,
			),
			Entry(
				"Check presence of tenant",
				`has(this.metadata.tenant)`,
				`tenant != ''`,
			),
			Entry(
				"Check presence of creator",
				`has(this.metadata.creator)`,
				`creator != ''`,
			),
			Entry(
				"Enum field equals integer literal",
				`this.spec.spec_enum == 1`,
				`data->'spec'->>'spec_enum' = 'MY_ENUM_VALUE_A'`,
			),
			Entry(
				"Enum field not equals integer literal",
				`this.spec.spec_enum != 2`,
				`data->'spec'->>'spec_enum' != 'MY_ENUM_VALUE_B'`,
			),
			Entry(
				"Enum field equals zero (unspecified)",
				`this.spec.spec_enum == 0`,
				`data->'spec'->>'spec_enum' = 'MY_ENUM_UNSPECIFIED'`,
			),
			Entry(
				"Reversed enum comparison",
				`1 == this.spec.spec_enum`,
				`data->'spec'->>'spec_enum' = 'MY_ENUM_VALUE_A'`,
			),
			Entry(
				"Enum field in list",
				`this.spec.spec_enum in [0, 1, 2]`,
				`data->'spec'->>'spec_enum' in ('MY_ENUM_UNSPECIFIED', 'MY_ENUM_VALUE_A', 'MY_ENUM_VALUE_B')`,
			),
			Entry(
				"Enum field in single-element list",
				`this.spec.spec_enum in [1]`,
				`data->'spec'->>'spec_enum' in ('MY_ENUM_VALUE_A')`,
			),
			Entry(
				"Compare project to string",
				`this.metadata.project == 'my_project'`,
				`project = 'my_project'`,
			),
			Entry(
				"Project starts with string",
				`this.metadata.project.startsWith('my_project.')`,
				`cast(project as text) like 'my\_project.%'`,
			),
			Entry(
				"Project ends with string",
				`this.metadata.project.endsWith('.my_project')`,
				`cast(project as text) like '%.my\_project'`,
			),
			Entry(
				"Project contains string",
				`this.metadata.project.contains('my')`,
				`cast(project as text) like '%my%'`,
			),
			Entry(
				"Bracket-index on a map field nested under spec",
				`this.spec.spec_map["key"] == null`,
				`data->'spec'->'spec_map'->>'key' is null`,
			),
			Entry(
				"Key presence in a map field nested under spec",
				`'key' in this.spec.spec_map`,
				`data->'spec'->'spec_map' ? 'key'`,
			),
		)

		DescribeTable(
			"Enum translation errors",
			func(ctx context.Context, filter string) {
				_, err := translator.Translate(ctx, filter)
				Expect(err).To(HaveOccurred())
			},
			Entry(
				"Unknown enum value",
				`this.spec.spec_enum == 99`,
			),
			Entry(
				"Non-literal numeric expression equals enum",
				`this.spec.spec_enum == (1 + 1)`,
			),
			Entry(
				"Non-literal numeric expression not-equals enum",
				`this.spec.spec_enum != (1 + 1)`,
			),
			Entry(
				"Unknown enum value in list",
				`this.spec.spec_enum in [1, 99]`,
			),
		)

		// These two cases are currently rejected by CEL's own type checker at Compile() time — the 'in' operator
		// has no overload for a plain string/message operand — so they don't exercise translateInField's
		// default branch directly. They're kept as regression tests of the externally observable contract
		// (translate-time error, never broken SQL for an unsupported 'in' target), which also covers that
		// branch if CEL's checking behavior ever changes.
		DescribeTable(
			"'in' operator: unsupported target kind errors",
			func(ctx context.Context, filter string) {
				_, err := translator.Translate(ctx, filter)
				Expect(err).To(HaveOccurred())
			},
			Entry(
				"'in' operator against a plain string field",
				`'x' in this.spec.spec_string`,
			),
			Entry(
				"'in' operator against a nested message field",
				`'x' in this.spec.spec_msg`,
			),
		)

		It("Does not leak the JSON operand path in the unsupported-field-kind error", func(ctx context.Context) {
			_, err := translator.Translate(ctx, `this.spec.spec_bytes == this.spec.spec_bytes`)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(
				"select of JSON field 'spec_bytes' of type 'osac.tests.v1.Spec' of kind 'bytes' isn't supported",
			))
		})
	})

	// Projects need special translation because the type of the 'name' column is 'ltree', and that can't be
	// compared directly to strings using the 'like' operator. Both the public and private Project descriptors
	// must be recognized, since a public-fronted ProjectsServer now configures the translator with the public
	// descriptor while PrivateProjectsServer keeps using the private one.
	Describe("Project translation", func() {
		for _, desc := range []protoreflect.MessageDescriptor{
			(*publicv1.Project)(nil).ProtoReflect().Descriptor(),
			(*privatev1.Project)(nil).ProtoReflect().Descriptor(),
		} {
			Describe(string(desc.FullName()), func() {
				var translator *FilterTranslator

				BeforeEach(func() {
					var err error

					translator, err = NewFilterTranslator().
						SetLogger(logger).
						SetDescriptor(desc).
						Build()
					Expect(err).ToNot(HaveOccurred())
				})

				DescribeTable(
					"Project translation",
					func(ctx context.Context, filter, expected string) {
						actual, err := translator.Translate(ctx, filter)
						Expect(err).ToNot(HaveOccurred())
						Expect(actual).To(Equal(expected))
					},
					Entry(
						"Compare name to string",
						`this.metadata.name == 'my_project'`,
						`name = 'my_project'`,
					),
					Entry(
						"Name starts with string",
						`this.metadata.name.startsWith('my_project.')`,
						`cast(name as text) like 'my\_project.%'`,
					),
					Entry(
						"Name ends with string",
						`this.metadata.name.endsWith('.my_project')`,
						`cast(name as text) like '%.my\_project'`,
					),
					Entry(
						"Name contains string",
						`this.metadata.name.contains('my')`,
						`cast(name as text) like '%my%'`,
					),
				)
			})
		}
	})
})
