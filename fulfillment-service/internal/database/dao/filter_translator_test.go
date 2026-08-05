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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	testsv1 "github.com/osac-project/fulfillment-service/internal/api/osac/tests/v1"
)

var _ = Describe("Filter translator", func() {
	Describe("Object translation", func() {
		var translator *FilterTranslator[*testsv1.Object]

		BeforeEach(func() {
			var err error

			translator, err = NewFilterTranslator[*testsv1.Object]().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
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
				`data->'spec' ? 'spec_bool'`,
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
				`id = 'path\\to\\thing'`,
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
		)
	})

	// Projects need special translation because the type of the 'name' column is 'ltree', and that can't be
	// compared directly to strings using the 'like' operator.
	Describe("Project translation", func() {
		var translator *FilterTranslator[*privatev1.Project]

		BeforeEach(func() {
			var err error

			translator, err = NewFilterTranslator[*privatev1.Project]().
				SetLogger(logger).
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
})
