package grpcserver

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// invokeCatalogRPC exercises generated requests through the production registration and reference interceptor.
// JSON keeps the same behavioral scenario readable across public/private versions of the three typed APIs.
func invokeCatalogRPC(layer, service, method, body string) (proto.Message, error) {
	prefix := "osac." + layer + ".v1."
	requestType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(prefix + service + method + "Request"))
	if err != nil {
		return nil, err
	}
	responseType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(prefix + service + method + "Response"))
	if err != nil {
		return nil, err
	}
	request, response := requestType.New().Interface(), responseType.New().Interface()
	if err := protojson.Unmarshal([]byte(body), request); err != nil {
		return nil, err
	}
	err = conn.Invoke(ctx, "/"+prefix+service+"/"+method, request, response)
	return response, err
}

var _ = Describe("Catalog reference RPC integration", func() {
	DescribeTable("resolves authoring references after merging and preserves ordinary lookups", func(kind, layer string) {
		name := "catalog-" + uuid.NewString()
		template, err := invokeCatalogRPC("private", kind+"Templates", "Create", fmt.Sprintf(`{"object":{"metadata":{"name":%q,"tenant":"shared"},"title":"Template"}}`, name))
		Expect(err).ToNot(HaveOccurred())
		templateObject := template.ProtoReflect().Get(template.ProtoReflect().Descriptor().Fields().ByName("object")).Message()
		templateID := templateObject.Get(templateObject.Descriptor().Fields().ByName("id")).String()
		_, err = invokeCatalogRPC("private", "Tenants", "Create", fmt.Sprintf(`{"object":{"metadata":{"name":%q}}}`, name))
		Expect(err).ToNot(HaveOccurred())
		_, err = invokeCatalogRPC("private", kind+"Templates", "Create", fmt.Sprintf(`{"object":{"metadata":{"name":%q,"tenant":%q},"title":"Tenant Template"}}`, name, name))
		Expect(err).ToNot(HaveOccurred())
		response, err := invokeCatalogRPC(layer, kind+"CatalogItems", "Create", fmt.Sprintf(`{"object":{"metadata":{"name":%q,"tenant":"shared"},"title":"Offering","published":true,"template":{"name":%q,"shared":true}}}`, name, name))
		Expect(err).ToNot(HaveOccurred())
		object := response.ProtoReflect().Get(response.ProtoReflect().Descriptor().Fields().ByName("object")).Message()
		id := object.Get(object.Descriptor().Fields().ByName("id")).String()
		ref := object.Get(object.Descriptor().Fields().ByName("template")).Message()
		Expect(ref.Get(ref.Descriptor().Fields().ByName("id")).String()).To(Equal(templateID))
		// A reference outside the mask must never reach lookup, while the stored reference remains unchanged.
		_, err = invokeCatalogRPC(layer, kind+"CatalogItems", "Update", fmt.Sprintf(`{"object":{"id":%q,"published":false,"template":{"id":"missing"}},"updateMask":"published"}`, id))
		Expect(err).ToNot(HaveOccurred())
		_, err = invokeCatalogRPC(layer, kind+"CatalogItems", "Update", fmt.Sprintf(`{"object":{"id":%q,"template":{"id":%q,"name":"wrong-name"}},"updateMask":"template"}`, id, templateID))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("id and name"))
		// A real ordinary tenant makes publication the first provisioning rejection for every resource type.
		_, err = invokeCatalogRPC(layer, kind+"s", "Create", fmt.Sprintf(`{"object":{"metadata":{"name":"resource","tenant":%q},"spec":{"catalogItem":{"id":%q}}}}`, name, id))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not published"))
		// Same-name tenant and shared catalogs must resolve using the requested selector.
		_, err = invokeCatalogRPC(layer, kind+"CatalogItems", "Create", fmt.Sprintf(`{"object":{"metadata":{"name":%q,"tenant":%q},"title":"Tenant offering","template":{"name":%q}}}`, name, name, name))
		Expect(err).ToNot(HaveOccurred())
		_, err = invokeCatalogRPC(layer, kind+"CatalogItems", "Update", fmt.Sprintf(`{"object":{"id":%q,"published":true},"updateMask":"published"}`, id))
		Expect(err).ToNot(HaveOccurred())
		for _, shared := range []bool{false, true} {
			if shared {
				By("resolving the published shared catalog by name")
			} else {
				By("rejecting the unpublished tenant catalog by name")
			}
			_, err = invokeCatalogRPC(layer, kind+"s", "Create", fmt.Sprintf(`{"object":{"metadata":{"name":"resource","tenant":%q},"spec":{"catalogItem":{"name":%q,"shared":%t}}}}`, name, name, shared))
			Expect(err).To(HaveOccurred())
			if shared {
				Expect(err.Error()).ToNot(ContainSubstring("not published"))
			} else {
				Expect(err.Error()).To(ContainSubstring("not published"))
			}
		}
		// Nonexcluded references still use the registered lookup, rather than being skipped request-wide.
		resourceService := kind + "s"
		field := "diskImage"
		if kind == "Cluster" {
			field = "version"
		}
		_, err = invokeCatalogRPC(layer, resourceService, "Create", fmt.Sprintf(`{"object":{"metadata":{"name":"resource"},"spec":{"template":{"id":%q},%q:{"id":"missing-dependency"}}}}`, templateID, field))
		Expect(err).To(HaveOccurred())
		Expect(strings.ToLower(err.Error())).To(ContainSubstring("not found"))
		_, err = invokeCatalogRPC(layer, kind+"CatalogItems", "Delete", fmt.Sprintf(`{"id":%q}`, id))
		Expect(err).ToNot(HaveOccurred())
		_, err = invokeCatalogRPC("private", kind+"Templates", "Delete", fmt.Sprintf(`{"id":%q}`, templateID))
		Expect(err).ToNot(HaveOccurred())
	},
		Entry("public compute instance", "ComputeInstance", "public"),
		Entry("private compute instance", "ComputeInstance", "private"),
		Entry("public cluster", "Cluster", "public"),
		Entry("private cluster", "Cluster", "private"),
		Entry("public bare metal instance", "BareMetalInstance", "public"),
		Entry("private bare metal instance", "BareMetalInstance", "private"),
	)
})
