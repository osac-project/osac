/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
)

var _ = Describe("RegisterReferenceLookups", func() {
	var validator *references.ReferenceValidator

	BeforeEach(func() {
		validator = newTestReferenceValidator()
	})

	It("registers SecretLocalReference for private and public APIs", func() {
		for _, name := range []protoreflect.FullName{
			"osac.private.v1.SecretLocalReference",
			"osac.public.v1.SecretLocalReference",
		} {
			Expect(validator.HasLookup(name)).To(BeTrue(), "missing lookup for %s", name)
		}
	})

	It("registers every reference type used in Create or Update requests", func() {
		var missing []string
		for _, name := range createOrUpdateReferenceTypes() {
			if !validator.HasLookup(name) {
				missing = append(missing, string(name))
			}
		}
		slices.Sort(missing)
		Expect(missing).To(BeEmpty(),
			"no lookup registered for Create/Update reference types:\n  %s",
			strings.Join(missing, "\n  "))
	})

	It("does not reject identity provider client_secret_secret as unregistered", func() {
		request := privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Spec: privatev1.IdentityProviderSpec_builder{
					Oidc: privatev1.OidcConfig_builder{
						ClientSecretSecret: privatev1.SecretLocalReference_builder{
							Id: "secret-id",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build()

		_, err := validator.UnaryServer(
			context.Background(),
			request,
			&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.IdentityProviders/Create"},
			func(context.Context, any) (any, error) { return "ok", nil },
		)
		if err != nil {
			st, _ := grpcstatus.FromError(err)
			Expect(st.Message()).ToNot(ContainSubstring("no lookup registered"),
				"SecretLocalReference is not registered with the interceptor: %v", err)
		}
	})
})

func newTestReferenceValidator() *references.ReferenceValidator {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tenancy, err := auth.NewGuestTenancyLogic().SetLogger(logger).Build()
	Expect(err).ToNot(HaveOccurred())
	validator, err := references.NewReferenceValidator().SetLogger(logger).Build()
	Expect(err).ToNot(HaveOccurred())
	err = registerReferenceLookups(validator, logger, tenancy, prometheus.NewRegistry())
	Expect(err).ToNot(HaveOccurred())
	return validator
}

func createOrUpdateReferenceTypes() []protoreflect.FullName {
	seen := map[protoreflect.FullName]struct{}{}
	refs := map[protoreflect.FullName]struct{}{}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		pkg := string(fd.Package())
		if pkg != "osac.private.v1" && pkg != "osac.public.v1" {
			return true
		}
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			methods := services.Get(i).Methods()
			for j := 0; j < methods.Len(); j++ {
				method := methods.Get(j)
				name := string(method.Name())
				if name != "Create" && name != "Update" {
					continue
				}
				collectReferenceTypes(method.Input(), seen, refs)
			}
		}
		return true
	})
	result := make([]protoreflect.FullName, 0, len(refs))
	for name := range refs {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}

func collectReferenceTypes(
	md protoreflect.MessageDescriptor,
	seen map[protoreflect.FullName]struct{},
	refs map[protoreflect.FullName]struct{},
) {
	if md == nil {
		return
	}
	name := md.FullName()
	if _, ok := seen[name]; ok {
		return
	}
	seen[name] = struct{}{}

	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			continue
		}
		msg := fd.Message()
		if strings.HasSuffix(string(msg.FullName()), "Reference") {
			refs[msg.FullName()] = struct{}{}
			continue
		}
		collectReferenceTypes(msg, seen, refs)
	}
}
