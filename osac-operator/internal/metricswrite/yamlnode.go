/*
Copyright 2026.

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

package metricswrite

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// decode parses the cluster-monitoring-config payload into a node tree so a
// merge rewrites only the keys OSAC owns and leaves every other key, comment
// and ordering intact. An absent or null document decodes to nil.
func decode(current []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(current)) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(current, &doc); err != nil {
		return nil, fmt.Errorf("parse cluster-monitoring-config: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("cluster-monitoring-config is not a YAML document")
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return nil, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("cluster-monitoring-config is not a mapping")
	}
	return &doc, nil
}

func documentOf(root *yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
}

func key(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func mappingOf(pairs ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: pairs}
}

func sequenceOf(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

func mappingValue(mapping *yaml.Node, name string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func scalarValue(mapping *yaml.Node, name string) string {
	value := mappingValue(mapping, name)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}

// ensureMapping returns the mapping under name, replacing a null or missing
// value. A scalar left by another writer is replaced too: every value OSAC
// writes under these keys is a mapping, so a scalar cannot be preserved.
func ensureMapping(mapping *yaml.Node, name string) *yaml.Node {
	if value := mappingValue(mapping, name); value != nil && value.Kind == yaml.MappingNode {
		return value
	}
	replacement := mappingOf()
	setNode(mapping, name, replacement)
	return replacement
}

func ensureSequence(mapping *yaml.Node, name string) *yaml.Node {
	if value := mappingValue(mapping, name); value != nil && value.Kind == yaml.SequenceNode {
		return value
	}
	replacement := sequenceOf()
	setNode(mapping, name, replacement)
	return replacement
}

func setScalar(mapping *yaml.Node, name, value string) {
	setNode(mapping, name, scalar(value))
}

func setNode(mapping *yaml.Node, name string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, key(name), value)
}

func deleteKey(mapping *yaml.Node, name string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
	}
	return false
}
