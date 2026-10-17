/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package onboarding

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/fulfillment-service/internal/kafkatopics"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/strimzi"
)

// TopicsConfig configures the Kafka topics created for each tenant. A zero value, in particular an empty HubID,
// disables topic creation.
type TopicsConfig struct {
	// HubID is the identifier of the hub that hosts the Kafka cluster. The installer registers that hub as "hub".
	HubID string

	// Namespace is the namespace of the Strimzi Kafka cluster.
	Namespace string

	// ClusterName is the name of the Strimzi Kafka cluster.
	ClusterName string

	// RetentionMS is how long records are kept, in milliseconds. A negative value keeps them indefinitely.
	RetentionMS int64
}

// validate checks the topics configuration. It returns no error when topic creation is disabled.
func (c TopicsConfig) validate() error {
	if c.HubID == "" {
		return nil
	}
	if c.Namespace == "" {
		return fmt.Errorf("kafka topics namespace is mandatory when a hub is configured")
	}
	if c.ClusterName == "" {
		return fmt.Errorf("kafka topics cluster name is mandatory when a hub is configured")
	}
	if c.RetentionMS < -1 {
		return fmt.Errorf("kafka topics retention should be -1 or greater, but it is %d", c.RetentionMS)
	}
	return nil
}

// ensureTopics creates the tenant's Kafka topics on the hub that hosts the Kafka cluster, or corrects them when they
// already exist with a different configuration. Topics are never deleted: a tenant's topics may be removed only after
// its final invoice, which is outside this reconciler.
func (t *task) ensureTopics(ctx context.Context) error {
	if t.r.topics.HubID == "" {
		return nil
	}
	entry, err := t.r.hubCache.Get(ctx, t.r.topics.HubID)
	if err != nil {
		return fmt.Errorf("failed to get hub %s: %w", t.r.topics.HubID, err)
	}
	tenantName := t.tenant.GetMetadata().GetName()
	for _, topic := range kafkatopics.Names(tenantName) {
		result, err := t.ensureTopic(ctx, entry.Client, topic)
		if err != nil {
			return err
		}
		t.r.logger.DebugContext(ctx, fmt.Sprintf("%s tenant Kafka topic", result),
			slog.String("hub_id", t.r.topics.HubID),
			slog.String("namespace", t.r.topics.Namespace),
			slog.String("name", topic),
		)
	}
	return nil
}

// ensureTopic creates the KafkaTopic resource, or patches an existing one whose configuration drifted. A topic that
// already matches is left alone.
func (t *task) ensureTopic(ctx context.Context, client clnt.Client, topic string) (result controllerutil.OperationResult, err error) {
	object := &strimzi.KafkaTopic{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.r.topics.Namespace,
			Name:      topic,
		},
	}
	tenantName := t.tenant.GetMetadata().GetName()
	result, err = controllerutil.CreateOrPatch(ctx, client, object, func() error {
		if object.Labels == nil {
			object.Labels = make(map[string]string)
		}
		object.Labels[strimzi.ClusterLabel] = t.r.topics.ClusterName
		object.Labels[labels.TenantUuid] = tenantName
		object.Labels[labels.TenantID] = t.tenant.GetId()
		object.Spec.Partitions = 1
		if object.Spec.Config == nil {
			object.Spec.Config = make(map[string]string)
		}
		// Time is the only eviction rule: size-based eviction is unlimited so it cannot silently shorten the
		// retention period, and the policy is delete so the topics are never compacted.
		object.Spec.Config["retention.ms"] = strconv.FormatInt(t.r.topics.RetentionMS, 10)
		object.Spec.Config["retention.bytes"] = "-1"
		object.Spec.Config["cleanup.policy"] = "delete"
		return nil
	})
	if err != nil {
		err = fmt.Errorf("failed to create or patch Kafka topic '%s' on hub %s: %w", topic, t.r.topics.HubID, err)
	}
	return
}
