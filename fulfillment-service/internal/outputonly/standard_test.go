/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package outputonly

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// The import registers the public proto files in the global registry:
	_ "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The OUTPUT_ONLY contract is enforced centrally by the interceptor, driven by the proto
// annotations. These checks keep the annotations themselves uniform, so the contract cannot
// rot as resources are added:
//
//   - every resource's status field carries exactly one OUTPUT_ONLY annotation, and
//   - no field inside a status message carries one, so the annotation always lives on the
//     status field itself.
//
// Importing the public package registers its files in the global registry.

// isResourceStatusField reports whether the field is a resource's status field: resources
// follow the object shape with both a spec and a status. Other message-typed fields named
// status, like a oneof variant in a streaming response, are not resource statuses.
func isResourceStatusField(message protoreflect.MessageDescriptor) (protoreflect.FieldDescriptor, bool) {
	specField := message.Fields().ByName("spec")
	statusField := message.Fields().ByName("status")
	if specField == nil || statusField == nil ||
		specField.Kind() != protoreflect.MessageKind || statusField.Kind() != protoreflect.MessageKind {
		return nil, false
	}
	return statusField, true
}

// publicProtoFiles returns the descriptors of the public API proto files.
func publicProtoFiles() []protoreflect.FileDescriptor {
	var files []protoreflect.FileDescriptor
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if strings.HasPrefix(fd.Path(), "osac/public/v1/") {
			files = append(files, fd)
		}
		return true
	})
	return files
}

var _ = Describe("OUTPUT_ONLY annotation standard", func() {
	It("annotates the status field of every public resource", func() {
		publicFiles := publicProtoFiles()
		Expect(publicFiles).ToNot(BeEmpty())
		var checked int
		for _, fd := range publicFiles {
			messages := fd.Messages()
			for i := 0; i < messages.Len(); i++ {
				message := messages.Get(i)
				statusField, ok := isResourceStatusField(message)
				if !ok {
					continue
				}
				checked++
				Expect(hasOutputOnlyBehavior(statusField)).To(BeTrue(),
					"%s.status must be annotated OUTPUT_ONLY", message.FullName())
			}
		}
		Expect(checked).To(BeNumerically(">", 10))
	})

	It("keeps OUTPUT_ONLY annotations out of status messages", func() {
		for _, fd := range publicProtoFiles() {
			messages := fd.Messages()
			for i := 0; i < messages.Len(); i++ {
				message := messages.Get(i)
				statusField, ok := isResourceStatusField(message)
				if !ok {
					continue
				}
				statusMessage := statusField.Message()
				fields := statusMessage.Fields()
				for j := 0; j < fields.Len(); j++ {
					Expect(hasOutputOnlyBehavior(fields.Get(j))).To(BeFalse(),
						"%s.%s: the annotation belongs on the status field, not on its fields",
						statusMessage.FullName(), fields.Get(j).Name())
				}
			}
		}
	})
})
