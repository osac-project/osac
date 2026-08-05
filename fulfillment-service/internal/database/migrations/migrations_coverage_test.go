/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dustin/go-humanize/english"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Migrations test coverage", func() {
	It("Each migration has a corresponding test file", func() {
		// Migrations that predate the testing mechanism and don't have tests yet. New migrations should not be
		// added to this list.
		excludedMigrationFiles := map[string]bool{
			"0_public_schema":                                 true,
			"1_private_schema":                                true,
			"2_data":                                          true,
			"3_merge_public_and_private_data":                 true,
			"4_add_finalizers":                                true,
			"5_create_host_classes_tables":                    true,
			"6_merge_cluster_order_and_cluster":               true,
			"7_add_notifications_table":                       true,
			"8_create_virtual_machine_templates_tables":       true,
			"9_create_virtual_machines_tables":                true,
			"10_add_creators":                                 true,
			"11_add_tenants":                                  true,
			"12_create_hosts_tables":                          true,
			"13_add_name":                                     true,
			"14_rename_virtual_machines_to_compute_instances": true,
			"15_rename_vm_templates_to_ci_templates":          true,
			"16_add_labels":                                   true,
			"17_add_annotations":                              true,
			"19_create_network_classes_tables":                true,
			"20_create_virtual_networks_tables":               true,
			"21_create_subnets_tables":                        true,
			"22_create_security_groups_tables":                true,
			"23_drop_hosts_tables":                            true,
			"24_create_leases_tables":                         true,
			"25_add_version":                                  true,
			"26_create_archived_leases_table":                 true,
			"27_rename_host_classes_to_host_types":            true,
			"28_create_organizations_tables":                  true,
			"29_create_users_tables":                          true,
			"30_create_public_ip_pools_tables":                true,
			"31_create_public_ips_tables":                     true,
			"32_add_network_classes_is_default_index":         true,
			"33_create_roles_tables":                          true,
			"34_create_role_bindings_tables":                  true,
			"35_add_public_ips_compute_instance_unique_index": true,
			"36_drop_archived_public_ips_indexes":             true,
			"37_create_catalog_items_tables":                  true,
			"38_create_public_ip_attachments_tables":          true,
		}

		// Find all migration files:
		migrationFiles, err := filepath.Glob("*.up.sql")
		Expect(err).ToNot(HaveOccurred())
		Expect(migrationFiles).ToNot(BeEmpty())
		sort.Strings(migrationFiles)

		// Check that each migration has a test file:
		var badMigrationFiles []string
		for _, migrationFile := range migrationFiles {
			migrationName := strings.TrimSuffix(migrationFile, ".up.sql")
			if excludedMigrationFiles[migrationName] {
				continue
			}
			testFile := migrationName + "_test.go"
			_, err := os.Stat(testFile)
			if errors.Is(err, os.ErrNotExist) {
				badMigrationFiles = append(badMigrationFiles, migrationFile)
				continue
			}
			Expect(err).ToNot(HaveOccurred())
		}
		if len(badMigrationFiles) > 0 {
			badMigrationList := make([]string, len(badMigrationFiles))
			for i, migrationFile := range badMigrationFiles {
				badMigrationList[i] = fmt.Sprintf("'%s'", migrationFile)
			}
			if len(badMigrationList) == 1 {
				Fail(fmt.Sprintf(
					"Migration file %s doesn't have a corresponding test file",
					badMigrationList[0],
				))
			} else {
				Fail(fmt.Sprintf(
					"Migration files %s don't have corresponding test file",
					english.WordSeries(badMigrationList, "and"),
				))
			}
		}
	})
})
