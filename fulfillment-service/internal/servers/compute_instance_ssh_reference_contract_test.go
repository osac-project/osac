/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func seedSSHReferenceTestProject(name string) error {
	projects, err := dao.NewGenericDAO[*privatev1.Project]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
	if err != nil {
		return err
	}
	_, err = projects.Create().SetObject(privatev1.Project_builder{
		Id:       name,
		Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
	}.Build()).Do(ctx)
	return err
}

func computeInstanceWithSSHKey(tenant, reference string) (*privatev1.ComputeInstance, error) {
	body := fmt.Sprintf(`{"metadata":{"tenant":%q},"spec":{"sshKey":%s}}`, tenant, reference)
	instance := &privatev1.ComputeInstance{}
	if err := protojson.Unmarshal([]byte(body), instance); err != nil {
		return nil, err
	}
	return instance, nil
}

var _ = Describe("ComputeInstance SSH reference scope", func() {
	It("resolves names and IDs in the exact tenant project", func() {
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).NotTo(HaveOccurred())

		for _, project := range []string{"apps", "ops"} {
			Expect(seedSSHReferenceTestProject(project)).To(Succeed())
		}

		for _, secret := range []*privatev1.Secret{
			privatev1.Secret_builder{
				Id:       "scoped-ssh-apps-key",
				Metadata: privatev1.Metadata_builder{Name: "scoped-login", Tenant: testTenant, Project: "apps"}.Build(),
				Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
				Data:     map[string][]byte{"public_key": []byte(testSSHPublicKey)},
			}.Build(),
			privatev1.Secret_builder{
				Id:       "scoped-ssh-ops-key",
				Metadata: privatev1.Metadata_builder{Name: "scoped-login", Tenant: testTenant, Project: "ops"}.Build(),
				Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
				Data:     map[string][]byte{"public_key": []byte(testSSHPublicKey)},
			}.Build(),
		} {
			_, err = server.secretsDao.Create().SetObject(secret).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
		}

		for _, tc := range []struct {
			reference string
			id        string
			project   string
		}{
			{reference: `{"name":"scoped-login","project":"apps"}`, id: "scoped-ssh-apps-key", project: "apps"},
			{reference: `{"id":"scoped-ssh-ops-key","project":"ops"}`, id: "scoped-ssh-ops-key", project: "ops"},
		} {
			instance, err := computeInstanceWithSSHKey(testTenant, tc.reference)
			Expect(err).NotTo(HaveOccurred())
			Expect(server.validateSshPublicKey(ctx, instance, nil)).To(Succeed())
			reference := instance.GetSpec().GetSshKey()
			Expect(reference.GetId()).To(Equal(tc.id))
			Expect(reference.GetName()).To(Equal("scoped-login"))
			projectField := reference.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name("project"))
			Expect(projectField).NotTo(BeNil())
			Expect(reference.ProtoReflect().Get(projectField).String()).To(Equal(tc.project))
			sharedField := reference.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name("shared"))
			Expect(sharedField).NotTo(BeNil())
			Expect(reference.ProtoReflect().Get(sharedField).Bool()).To(BeFalse())
		}
	})

	It("returns one non-disclosing error for missing and out-of-scope Secrets", func() {
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).NotTo(HaveOccurred())

		for _, project := range []string{"apps", "ops"} {
			Expect(seedSSHReferenceTestProject(project)).To(Succeed())
		}

		_, err = server.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id:       "scope-error-key",
			Metadata: privatev1.Metadata_builder{Name: "scope-error-key", Tenant: testTenant, Project: "apps"}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"public_key": []byte(testSSHPublicKey)},
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())

		requests := []string{
			`{"id":"scope-error-key","project":"ops"}`,
			`{"name":"scope-error-key","project":"ops"}`,
			`{"id":"missing-scope-error-key","project":"apps"}`,
			`{"name":"missing-scope-error-key","project":"apps"}`,
		}
		for _, reference := range requests {
			instance, err := computeInstanceWithSSHKey(testTenant, reference)
			Expect(err).NotTo(HaveOccurred())
			err = server.validateSshPublicKey(ctx, instance, nil)
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			Expect(status.Convert(err).Message()).To(Equal(sshKeyReferenceNotFoundMessage))
		}
	})

	It("rejects shared references before accessing the Secret DAO", func() {
		instance, err := computeInstanceWithSSHKey(testTenant, `{"name":"hidden-key","shared":true}`)
		Expect(err).NotTo(HaveOccurred())

		err = (&PrivateComputeInstancesServer{}).validateSshPublicKey(ctx, instance, nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(Equal("SSH key reference must be tenant-scoped."))
	})

	It("keeps the full SSH reference immutable", func() {
		current, err := computeInstanceWithSSHKey(testTenant, `{"id":"key-id","name":"key-name","project":"apps"}`)
		Expect(err).NotTo(HaveOccurred())
		candidate, err := computeInstanceWithSSHKey(testTenant, `{"id":"key-id","name":"key-name","project":"ops"}`)
		Expect(err).NotTo(HaveOccurred())

		err = validateComputeInstanceImmutability(current, candidate, &fieldmaskpb.FieldMask{Paths: []string{"spec.ssh_key"}})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("ssh_key is immutable"))
	})

	It("decodes stored ID/name-only references with default project and shared values", func() {
		instance, err := computeInstanceWithSSHKey(testTenant, `{"id":"legacy-id","name":"legacy-name"}`)
		Expect(err).NotTo(HaveOccurred())
		reference := instance.GetSpec().GetSshKey().ProtoReflect()
		projectField := reference.Descriptor().Fields().ByName(protoreflect.Name("project"))
		sharedField := reference.Descriptor().Fields().ByName(protoreflect.Name("shared"))
		Expect(projectField).NotTo(BeNil())
		Expect(sharedField).NotTo(BeNil())
		Expect(reference.Get(projectField).String()).To(BeEmpty())
		Expect(reference.Get(sharedField).Bool()).To(BeFalse())

		item, err := computeInstanceCatalogItemWithSSHPolicy(testTenant,
			`{"editable":{"defaultValue":{"id":"legacy-id","name":"legacy-name"}}}`)
		Expect(err).NotTo(HaveOccurred())
		legacyDefault := item.GetFields().GetSshKey().GetEditable().GetDefaultValue().ProtoReflect()
		projectField = legacyDefault.Descriptor().Fields().ByName(protoreflect.Name("project"))
		sharedField = legacyDefault.Descriptor().Fields().ByName(protoreflect.Name("shared"))
		Expect(projectField).NotTo(BeNil())
		Expect(sharedField).NotTo(BeNil())
		Expect(legacyDefault.Get(projectField).String()).To(BeEmpty())
		Expect(legacyDefault.Get(sharedField).Bool()).To(BeFalse())
	})
})
