/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package consumer_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-notifications/internal/consumer"
)

type fakeTopicClient struct {
	refreshed  bool
	topics     []string
	refreshErr error
	topicsErr  error
}

func (c *fakeTopicClient) RefreshMetadata(...string) error {
	if c.refreshErr != nil {
		return c.refreshErr
	}
	c.refreshed = true
	return nil
}

func (c *fakeTopicClient) Topics() ([]string, error) {
	if !c.refreshed {
		return nil, errors.New("stale metadata: topics read before any refresh")
	}
	if c.topicsErr != nil {
		return nil, c.topicsErr
	}
	return c.topics, nil
}

var _ = Describe("FilterTopics", func() {
	It("keeps ingest topics and drops DLQ topics", func() {
		Expect(consumer.FilterTopics([]string{
			"osac.notifications.acme",
			"osac.notifications.dlq.acme",
			"osac.events.acme",
			"osac.notifications.other",
		})).To(Equal([]string{
			"osac.notifications.acme",
			"osac.notifications.other",
		}))
	})
})

var _ = Describe("DiscoverIngestTopics", func() {
	It("refreshes metadata before listing topics", func() {
		client := &fakeTopicClient{
			topics: []string{
				"osac.notifications.acme",
				"osac.notifications.dlq.acme",
				"osac.events.acme",
			},
		}
		topics, err := consumer.DiscoverIngestTopics(client)
		Expect(err).ToNot(HaveOccurred())
		Expect(client.refreshed).To(BeTrue())
		Expect(topics).To(Equal([]string{"osac.notifications.acme"}))
	})
})
