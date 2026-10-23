/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2/types"
)

func TestIntegrationTimings(t *testing.T) {
	report := types.Report{
		SuiteSucceeded: false,
		RunTime:        10 * time.Second,
		SpecReports: types.SpecReports{{
			ContainerHierarchyTexts: []string{"Catalog"}, LeafNodeText: "rejects a foreign tenant",
			LeafNodeType: types.NodeTypeIt, State: types.SpecStateFailed, RunTime: 7 * time.Second, NumAttempts: 2,
			SpecEvents: types.SpecEvents{
				{SpecEventType: types.SpecEventNodeStart, NodeType: types.NodeTypeIt, Duration: 100 * time.Second},
				{SpecEventType: types.SpecEventByEnd, Duration: 100 * time.Second},
				{SpecEventType: types.SpecEventNodeEnd, NodeType: types.NodeTypeBeforeEach, Duration: time.Second},
				{SpecEventType: types.SpecEventNodeEnd, NodeType: types.NodeTypeIt, Duration: time.Second},
				{SpecEventType: types.SpecEventNodeEnd, NodeType: types.NodeTypeIt, Duration: 2 * time.Second},
				{SpecEventType: types.SpecEventNodeEnd, NodeType: types.NodeTypeAfterEach, Duration: time.Second},
				{SpecEventType: types.SpecEventNodeEnd, NodeType: types.NodeTypeCleanupAfterEach, Duration: time.Second},
			},
		}, {
			LeafNodeType: types.NodeTypeBeforeSuite, State: types.SpecStatePassed, RunTime: time.Second,
			SpecEvents: types.SpecEvents{{SpecEventType: types.SpecEventNodeEnd, NodeType: types.NodeTypeBeforeSuite, Duration: time.Second}},
		}, {
			LeafNodeType: types.NodeTypeIt, State: types.SpecStateSkipped,
		}, {
			LeafNodeType: types.NodeTypeIt, State: types.SpecStateInterrupted, RunTime: 2 * time.Second,
			SpecEvents: types.SpecEvents{{SpecEventType: types.SpecEventNodeStart, NodeType: types.NodeTypeIt}},
		}},
	}
	report.SuiteConfig.RandomSeed = 42
	report.SuiteConfig.DryRun = true
	got := integrationTimings(report, "service-only", "candidate")
	if got.Succeeded || !got.DryRun || got.Seconds != 10 || got.Seed != 42 || got.Commit != "candidate" || got.Phase != "service-only" {
		t.Fatalf("suite metadata lost: %+v", got)
	}
	row := got.Specs[0]
	if row.Name != "Catalog rejects a foreign tenant" || row.State != "failed" || row.Attempts != 2 ||
		row.Setup != 1 || row.Body != 3 || row.Cleanup != 2 || row.Unattributed != 1 {
		t.Fatalf("node durations overlap or retries/cleanup lost: %+v", row)
	}
	if got.Specs[1].Setup != 1 || got.Specs[2].State != "skipped" || got.Specs[3].Unattributed != 2 {
		t.Fatalf("suite setup, skipped or incomplete nodes lost: %+v", got.Specs)
	}
}

func TestIntegrationTimingsExcludeCapturedData(t *testing.T) {
	const sentinel = "credential-sentinel"
	report := types.Report{SpecReports: types.SpecReports{{
		LeafNodeType: types.NodeTypeIt, LeafNodeText: "static spec name", State: types.SpecStateFailed,
		CapturedGinkgoWriterOutput: sentinel, CapturedStdOutErr: sentinel,
		Failure:       types.Failure{Message: sentinel},
		ReportEntries: types.ReportEntries{{Name: sentinel}},
		SpecEvents:    types.SpecEvents{{SpecEventType: types.SpecEventNodeEnd, NodeType: types.NodeTypeIt, Message: sentinel}},
	}}}
	path := filepath.Join(t.TempDir(), "timings", "service-only.json")
	if err := writeIntegrationTimings(path, integrationTimings(report, "service-only", "")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), sentinel) {
		t.Fatal("timing artifact includes captured data")
	}
	var decoded integrationTimingReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Specs[0].Name != "static spec name" {
		t.Fatal("timing artifact lost spec identity")
	}
	// A second phase must not overwrite the first report.
	second := filepath.Join(filepath.Dir(path), "manager-readiness.json")
	if err := writeIntegrationTimings(second, integrationTimings(report, "manager-readiness", "")); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil || string(first) != string(data) {
		t.Fatal("phase report overwritten")
	}
}
