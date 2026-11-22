/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package realmauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const realmArtifactDrift = "shipped realm artifacts must have identical JSON content"

// Compare every value before substitution. Object key order and whitespace are
// insignificant; array order, numeric representations, IDs and placeholders are not.
func equivalentRealmArtifacts(left, right string) error {
	first, err := readRealmArtifact(left)
	if err != nil {
		return err
	}
	second, err := readRealmArtifact(right)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(first, second) {
		return errors.New(realmArtifactDrift)
	}
	return nil
}

func readRealmArtifact(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := realmJSONValue(decoder)
	if err == nil {
		_, err = decoder.Token()
		if errors.Is(err, io.EOF) {
			return value, nil
		}
	}
	// Do not format the document, its keys or decoder errors into test output.
	return nil, errors.New("realm artifact must contain one valid JSON document without duplicate keys")
}

// encoding/json normally accepts duplicate object keys, which could hide drift.
// Decode tokens so duplicates are rejected at every object depth.
func realmJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := map[string]any{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("object key must be a string")
			}
			if _, exists := object[name]; exists {
				return nil, errors.New("duplicate object key")
			}
			value, err := realmJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err := decoder.Token() // The decoder validates the closing delimiter.
		return object, err
	case json.Delim('['):
		array := []any{}
		for decoder.More() {
			value, err := realmJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := decoder.Token()
		return array, err
	default:
		return token, nil
	}
}

var _ = Describe("Raw shipped realm equivalence", func() {
	const chart = "../../../osac-installer/charts/osac-infra/files/realm.json"
	const prerequisites = "../../../osac-installer/prerequisites/keycloak/service/files/realm.json"

	It("compares the complete artifacts before secret substitution", func() {
		Expect(equivalentRealmArtifacts(chart, prerequisites)).To(Succeed())
	})

	It("detects deliberately mutated unrelated-client drift in a temporary copy", func() {
		realm, err := readRealmArtifact(chart)
		Expect(err).ToNot(HaveOccurred())
		object, ok := realm.(map[string]any)
		Expect(ok).To(BeTrue())
		clients, ok := object["clients"].([]any)
		Expect(ok).To(BeTrue())
		mutated := false
		for _, value := range clients {
			client, ok := value.(map[string]any)
			Expect(ok).To(BeTrue())
			if client["clientId"] == "osac-ui" {
				enabled, ok := client["enabled"].(bool)
				Expect(ok).To(BeTrue())
				client["enabled"] = !enabled
				mutated = true
			}
		}
		Expect(mutated).To(BeTrue(), "the browser client must exist for this deliberate mutation control")
		data, err := json.MarshalIndent(realm, "", "  ")
		Expect(err).ToNot(HaveOccurred())
		path := filepath.Join(GinkgoT().TempDir(), "mutated-realm.json")
		Expect(os.WriteFile(path, data, 0600)).To(Succeed())
		Expect(equivalentRealmArtifacts(chart, path)).To(MatchError(realmArtifactDrift))
	})

	DescribeTable("compares strict JSON without normalizing values", func(left, right string, equivalent bool) {
		dir := GinkgoT().TempDir()
		first, second := filepath.Join(dir, "first.json"), filepath.Join(dir, "second.json")
		Expect(os.WriteFile(first, []byte(left), 0600)).To(Succeed())
		Expect(os.WriteFile(second, []byte(right), 0600)).To(Succeed())
		err := equivalentRealmArtifacts(first, second)
		if equivalent {
			Expect(err).ToNot(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
		}
	},
		Entry("ignores only formatting and object key order",
			`{"a":1,"b":{"x":true,"y":null}}`, "{\n  \"b\": {\"y\": null, \"x\": true}, \"a\": 1\n}", true),
		Entry("retains array order", `{"roles":["a","b"]}`, `{"roles":["b","a"]}`, false),
		Entry("retains scalar types", `{"enabled":true}`, `{"enabled":1}`, false),
		Entry("retains numeric precision", `{"id":9007199254740992}`, `{"id":9007199254740993}`, false),
		Entry("retains numeric representations", `{"value":1}`, `{"value":1.0}`, false),
		Entry("retains IDs and placeholders", `{"id":"a","secret":"__ONE__"}`, `{"id":"a","secret":"__TWO__"}`, false),
		Entry("rejects duplicate keys even in matching documents",
			`{"client":{"enabled":false,"enabled":true}}`, `{"client":{"enabled":false,"enabled":true}}`, false),
		Entry("accepts the same key in separate objects", `[{"a":1},{"a":2}]`, `[{"a":1},{"a":2}]`, true),
		Entry("rejects trailing documents", `{} {}`, `{} {}`, false),
		Entry("rejects malformed JSON", `{"a":[}`, `{"a":[}`, false),
	)
})
