/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package quota

// Link says how a resource that OSAC creates for a parent points back to it, so the usage
// query can count a created resource under its parent. The query trusts a link only when
// the resource's creator is `system`: only the private API and OSAC's own code record
// that creator, so a tenant can't move a resource out of its parent's count by faking a
// link.
type Link struct {
	// Kind is the linked resource's kind, such as "Volume".
	Kind string

	// Parent is the parent's kind, such as "ComputeInstance". Empty for label links,
	// whose kind label carries the parent's kind.
	Parent string

	// Field is the path of the parent reference, relative to the resource, such as a
	// host's "status.cluster". Empty for name and label links.
	Field string

	// NamePrefix names the parent's created resources: "{parent}" is the parent's ID,
	// so "ci-{parent}-" names a VM's disk volume "ci-<vm id>-boot". Empty for field and
	// label links.
	NamePrefix string

	// IDLabel and KindLabel name the labels that hold the parent's ID and kind. Empty
	// for field and name links.
	IDLabel   string
	KindLabel string
}

// Links returns the created_for links of every kind, sorted by kind name.
func Links() ([]Link, error) {
	byName, err := kindsByName()
	if err != nil {
		return nil, err
	}
	var links []Link
	for _, name := range sortedKeys(byName) {
		kind := byName[name]
		if kind.CreatedFor != nil {
			links = append(links, Link{
				Kind:       kind.Name,
				Parent:     kind.CreatedFor.Parent,
				NamePrefix: kind.CreatedFor.NamePrefix,
				IDLabel:    kind.CreatedFor.IDLabel,
				KindLabel:  kind.CreatedFor.KindLabel,
			})
		}
		for _, field := range kind.Fields {
			if field.CreatedFor == nil {
				continue
			}
			links = append(links, Link{
				Kind:   kind.Name,
				Parent: field.CreatedFor.Parent,
				Field:  field.Path,
			})
		}
	}
	return links, nil
}
