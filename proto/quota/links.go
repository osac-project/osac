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

// Link says how a resource that OSAC creates for a parent points back to it: the parent's kind
// and the way to find its identifier. It carries only what the enforcement check needs, because
// the usage query resolves the same links through the kind's annotations (see Kind.LinkTo).
type Link struct {
	// Kind is the linked resource's kind, such as "Volume".
	Kind string

	// Parent is the parent's kind, such as "ComputeInstance". Empty for label links,
	// whose kind label carries the parent's kind.
	Parent string

	// Field is the path of the parent reference, relative to the resource, such as a
	// host's "status.cluster". The link resolves by the reference's id subfield, such
	// as "status.cluster.id": the writers set the id and leave the name empty. Empty
	// for name and label links.
	Field string

	// NamePrefix names the parent's created resources: "{parent}" is the parent's ID,
	// so "ci-{parent}-" names a VM's disk volume "ci-<vm id>-boot". Empty for field and
	// label links.
	NamePrefix string
}

// Links returns one link per kind that OSAC creates resources for a parent, sorted by kind
// name: the kind's own link when it declares one, else the link of its first annotated field.
// A kind declares one link today, so a kind whose own link is a label link hides no field link.
func Links() ([]Link, error) {
	byName, err := kindsByName()
	if err != nil {
		return nil, err
	}
	var links []Link
	for _, name := range SortedKeys(byName) {
		kind := byName[name]
		if kind.CreatedFor != nil {
			links = append(links, Link{
				Kind:       kind.Name,
				Parent:     kind.CreatedFor.Parent,
				NamePrefix: kind.CreatedFor.NamePrefix,
			})
			continue
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
			break
		}
	}
	return links, nil
}
