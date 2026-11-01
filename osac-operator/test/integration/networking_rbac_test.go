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

package integration

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The deployed operator's networking permissions, asked of the real
// authorizer with SubjectAccessReviews for its ServiceAccount (inventory item
// I5: NetworkAttachment, NetworkClass, the BareMetalInstance watch of the
// NetworkAttachment controller, and the registration ConfigMaps).

// pluralOf returns the resource name of an OSAC kind.
func pluralOf(kind string) string {
	return strings.SplitN(resourceOf(kind), ".", 2)[0]
}

// subjectAccessReview asks the API server whether user (a ServiceAccount)
// may do verb on group/resource[/subresource] in namespace.
func subjectAccessReview(user, namespace, group, resource, subresource, verb string) (bool, string) {
	groups := []any{"system:authenticated"}
	if strings.HasPrefix(user, "system:serviceaccount:") {
		parts := strings.Split(user, ":")
		groups = append(groups, "system:serviceaccounts", "system:serviceaccounts:"+parts[2])
	}
	attributes := map[string]any{"namespace": namespace, "group": group, "resource": resource, "verb": verb}
	if subresource != "" {
		attributes["subresource"] = subresource
	}
	review := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "authorization.k8s.io/v1",
		"kind":       "SubjectAccessReview",
		"spec":       map[string]any{"user": user, "groups": groups, "resourceAttributes": attributes},
	}}
	output, err := kubectl(toJSON(review), "create", "-f", "-", "-o", "json")
	Expect(err).NotTo(HaveOccurred(), output)
	result := &unstructured.Unstructured{}
	Expect(result.UnmarshalJSON([]byte(output))).To(Succeed())
	allowed, _, _ := unstructured.NestedBool(result.Object, "status", "allowed")
	reason, _, _ := unstructured.NestedString(result.Object, "status", "reason")
	return allowed, reason
}

var _ = Describe("Deployed operator networking RBAC (SubjectAccessReview)", Label("networking", "rbac"), func() {
	type permission struct {
		group, resource, subresource string
		verbs                        []string
	}
	osac := func(resource, subresource string, verbs ...string) permission {
		return permission{group: "osac.openshift.io", resource: resource, subresource: subresource, verbs: verbs}
	}

	It("grants the operator ServiceAccount what the networking controllers need and nothing it must not have", func() {
		identity := operatorIdentity()
		_, _ = fmt.Fprintf(GinkgoWriter, "operator identity: %s\n", identity)

		managed := []string{"virtualnetworks", "subnets", "securitygroups", "externalippools",
			"externalips", "externalipattachments", "natgateways"}
		granted := make([]permission, 0, 12+3*len(managed))
		granted = append(granted,
			osac("networkattachments", "", "get", "list", "watch", "create", "update", "patch", "delete"),
			osac("networkattachments", "status", "get", "update", "patch"),
			osac("networkattachments", "finalizers", "update"),
			osac("networkclasses", "", "get", "list", "watch", "update", "patch"),
			osac("networkclasses", "status", "get", "update", "patch"),
			osac("networkclasses", "finalizers", "update"),
			// The NetworkAttachment controller watches BareMetalInstances (BMaaS targets).
			osac("baremetalinstances", "", "get", "list", "watch"),
			osac("computeinstances", "", "get", "list", "watch"),
			osac("fabricdomains", "", "get", "list", "watch", "update", "patch"),
			osac("fabricdomains", "status", "get", "update", "patch"),
			osac("fabricdomains", "finalizers", "update"),
			// The NetworkClass controller reads the registrations in its namespace.
			permission{group: "", resource: "configmaps", verbs: []string{"get", "list", "watch"}},
		)
		for _, resource := range managed {
			granted = append(granted,
				osac(resource, "", "get", "list", "watch", "update", "patch"),
				osac(resource, "status", "get", "update", "patch"),
				osac(resource, "finalizers", "update"))
		}
		for _, p := range granted {
			for _, verb := range p.verbs {
				allowed, reason := subjectAccessReview(identity, operatorNamespace, p.group, p.resource, p.subresource, verb)
				Expect(allowed).To(BeTrue(), "%s must be allowed to %s %s/%s in %s: %s",
					identity, verb, p.resource, p.subresource, operatorNamespace, reason)
			}
		}

		By("checking that the operator cannot create or delete NetworkClasses (the fulfillment reconciler owns them)")
		for _, verb := range []string{"create", "delete"} {
			allowed, _ := subjectAccessReview(identity, operatorNamespace, "osac.openshift.io", "networkclasses", "", verb)
			Expect(allowed).To(BeFalse(), "%s must not be allowed to %s networkclasses", identity, verb)
		}
	})
})
