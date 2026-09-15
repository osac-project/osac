/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("shared adapter topics", func() {
	It("uses the existing metering topic set", func() {
		Expect(AllTopics).To(Equal([]string{
			TopicLifecycle,
			TopicHeartbeat,
			TopicCorrections,
			TopicInference,
		}))
	})

	It("locks the shared Kafka topic retention contract", func() {
		_, testFile, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue())
		templatePath := filepath.Join(filepath.Dir(testFile), "..", "charts", "osac-metering", "templates", "kafka-topics.yaml")
		templateBytes, err := os.ReadFile(templatePath)
		Expect(err).NotTo(HaveOccurred())
		template := string(templateBytes)

		Expect(strings.ToLower(template)).NotTo(ContainSubstring("bmaas"))
		Expect(template).To(ContainSubstring("mul .retentionDays 86400000"))

		dictPattern := regexp.MustCompile(`\(dict "name" "([^"]+)"\s+"partitions"\s+\d+\s+"retentionDays"\s+(\d+)\)`)
		matches := dictPattern.FindAllStringSubmatch(template, -1)
		Expect(matches).To(HaveLen(5))

		retentionDays := make(map[string]string, len(matches))
		for _, match := range matches {
			retentionDays[match[1]] = match[2]
		}
		Expect(retentionDays).To(Equal(map[string]string{
			"osac.metering.lifecycle":   "30",
			"osac.metering.heartbeat":   "30",
			"osac.metering.inference":   "30",
			"osac.metering.corrections": "30",
			"osac.metering.dlq":         "90",
		}))
	})
})
