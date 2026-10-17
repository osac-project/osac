/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package strimzi contains the subset of the Strimzi KafkaTopic resource that OSAC creates. The full upstream types
// are not published as a Go module, so only the fields OSAC sets are modeled here.
package strimzi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// Group is the API group of the Strimzi resources.
	Group = "kafka.strimzi.io"

	// Version is the API version of the Strimzi KafkaTopic resource.
	Version = "v1"

	// ClusterLabel selects the Strimzi Kafka cluster that a KafkaTopic belongs to.
	ClusterLabel = "strimzi.io/cluster"
)

// SchemeGroupVersion is the group and version of the Strimzi KafkaTopic resource.
var SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: Version}

// KafkaTopic is a Strimzi KafkaTopic. Only the fields OSAC sets are present; the rest of the resource is preserved
// untouched by the Kubernetes client.
type KafkaTopic struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec KafkaTopicSpec `json:"spec,omitempty"`
}

// KafkaTopicSpec is the spec of a Strimzi KafkaTopic. Replicas is left unset so the topic follows the replication
// factor configured in the Kafka cluster.
type KafkaTopicSpec struct {
	Partitions int32             `json:"partitions"`
	Config     map[string]string `json:"config,omitempty"`
}

// KafkaTopicList is a list of Strimzi KafkaTopics.
type KafkaTopicList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []KafkaTopic `json:"items"`
}

// DeepCopyObject implements runtime.Object.
func (in *KafkaTopic) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopy returns a deep copy of the KafkaTopic.
func (in *KafkaTopic) DeepCopy() *KafkaTopic {
	if in == nil {
		return nil
	}
	out := new(KafkaTopic)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the KafkaTopic into the given target.
func (in *KafkaTopic) DeepCopyInto(out *KafkaTopic) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec.Partitions = in.Spec.Partitions
	if in.Spec.Config != nil {
		config := make(map[string]string, len(in.Spec.Config))
		for key, value := range in.Spec.Config {
			config[key] = value
		}
		out.Spec.Config = config
	}
}

// DeepCopyObject implements runtime.Object.
func (in *KafkaTopicList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopy returns a deep copy of the KafkaTopicList.
func (in *KafkaTopicList) DeepCopy() *KafkaTopicList {
	if in == nil {
		return nil
	}
	out := new(KafkaTopicList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the KafkaTopicList into the given target.
func (in *KafkaTopicList) DeepCopyInto(out *KafkaTopicList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		items := make([]KafkaTopic, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&items[i])
		}
		out.Items = items
	}
}

// AddToScheme registers the Strimzi KafkaTopic types in the given scheme.
func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &KafkaTopic{}, &KafkaTopicList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
