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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

// Use an allowlist instead of serializing Ginkgo's full report: captured output,
// failures and arbitrary report entries may contain credentials or request data.
type integrationTimingReport struct {
	Phase     string                  `json:"phase"`
	Commit    string                  `json:"commit,omitempty"`
	Seed      int64                   `json:"seed"`
	Succeeded bool                    `json:"succeeded"`
	DryRun    bool                    `json:"dry_run"`
	Started   time.Time               `json:"started"`
	Seconds   float64                 `json:"seconds"`
	Specs     []integrationSpecTiming `json:"specs"`
}

type integrationSpecTiming struct {
	Name         string  `json:"name"`
	NodeType     string  `json:"node_type"`
	State        string  `json:"state"`
	Attempts     int     `json:"attempts"`
	Seconds      float64 `json:"seconds"`
	Setup        float64 `json:"setup_seconds"`
	Body         float64 `json:"body_seconds"`
	Cleanup      float64 `json:"cleanup_seconds"`
	Unattributed float64 `json:"unattributed_seconds"`
}

var _ = ginkgo.ReportAfterSuite("integration timings", func(report types.Report) {
	path := os.Getenv("IT_TIMING_REPORT")
	if path == "" {
		return
	}
	timing := integrationTimings(report, os.Getenv("IT_TIMING_PHASE"), os.Getenv("IT_TEST_COMMIT"))
	if err := writeIntegrationTimings(path, timing); err != nil {
		ginkgo.Fail(fmt.Sprintf("write integration timings: %v", err))
	}
	fmt.Fprintf(ginkgo.GinkgoWriter, "Integration timing: phase=%s suite=%s report=%s\n", timing.Phase, report.RunTime, path)
})

func integrationTimings(report types.Report, phase, commit string) integrationTimingReport {
	result := integrationTimingReport{
		Phase: phase, Commit: commit, Seed: report.SuiteConfig.RandomSeed, DryRun: report.SuiteConfig.DryRun,
		Succeeded: report.SuiteSucceeded, Started: report.StartTime, Seconds: report.RunTime.Seconds(),
		Specs: make([]integrationSpecTiming, 0, len(report.SpecReports)),
	}
	for _, spec := range report.SpecReports {
		row := integrationSpecTiming{
			Name: spec.FullText(), NodeType: spec.LeafNodeType.String(), State: spec.State.String(),
			Attempts: spec.NumAttempts, Seconds: spec.RunTime.Seconds(),
		}
		if row.Name == "" {
			row.Name = row.NodeType
		}
		for _, event := range spec.SpecEvents {
			// By steps and node-start events overlap node-end durations.
			if event.SpecEventType != types.SpecEventNodeEnd {
				continue
			}
			seconds := event.Duration.Seconds()
			switch {
			case event.NodeType.Is(types.NodeTypeBeforeEach | types.NodeTypeJustBeforeEach | types.NodeTypeBeforeAll |
				types.NodeTypeBeforeSuite | types.NodeTypeSynchronizedBeforeSuite):
				row.Setup += seconds
			case event.NodeType.Is(types.NodeTypeIt):
				row.Body += seconds
			case event.NodeType.Is(types.NodeTypeAfterEach | types.NodeTypeJustAfterEach | types.NodeTypeAfterAll |
				types.NodeTypeAfterSuite | types.NodeTypeSynchronizedAfterSuite | types.NodeTypeCleanupAfterEach |
				types.NodeTypeCleanupAfterAll | types.NodeTypeCleanupAfterSuite):
				row.Cleanup += seconds
			}
		}
		// Includes reporting overhead and any node without a completed timing event.
		row.Unattributed = max(0, row.Seconds-row.Setup-row.Body-row.Cleanup)
		result.Specs = append(result.Specs, row)
	}
	return result
}

func writeIntegrationTimings(path string, report integrationTimingReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".timings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
