/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	windowsUserDataCLISentinel = "OSAC-7712-CLI-SECRET-SENTINEL"
	windowsUserDataVMSentinel  = "OSAC-7713-VM-SECRET-SENTINEL"
	windowsUserDataMaxBytes    = 65_536
)

var _ = Describe("CLI Windows user-data Secret lifecycle", Label("cli", "secrets"), func() {
	var homeDir string

	BeforeEach(func() {
		var err error
		homeDir, err = tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(homeDir)).To(Succeed())
		})
	})

	DescribeTable("creates, describes, and deletes a Windows user-data Secret", func(ctx context.Context, xmlData []byte) {
		_, stderr, exitCode := tool.LoginCLI(ctx, homeDir, userUsername, usersPassword)
		Expect(exitCode).To(Equal(0), "login failed: %s", stderr)

		name := fmt.Sprintf("it-windows-user-data-%s", uuid.New()[24:32])
		xmlPath := writeWindowsUserDataFile(homeDir, "Unattend.xml", xmlData)

		created := false
		DeferCleanup(func(ctx context.Context) {
			if !created {
				return
			}
			_, cleanupStderr, code := tool.RunCLI(ctx, homeDir, "delete", "secrets", name)
			Expect(code).To(Equal(0), "failed to clean up Secret %q: %s", name, cleanupStderr)
		})

		createdOutput, createStderr, code := runCreateWindowsUserDataCLI(ctx, homeDir, name, "userdata="+xmlPath)
		Expect(code).To(Equal(0), "create failed: %s%s", createdOutput, createStderr)
		created = true

		Expect(createdOutput).To(ContainSubstring(name))
		Expect(createdOutput + createStderr).ToNot(ContainSubstring(windowsUserDataCLISentinel))

		_, idOutput, found := strings.Cut(createdOutput, "(ID: ")
		Expect(found).To(BeTrue(), "create output did not include the Secret ID: %s", createdOutput)
		secretID := strings.TrimRight(strings.TrimSpace(idOutput), ").")
		Expect(secretID).ToNot(BeEmpty())

		client := publicv1.NewSecretsClient(tool.ExternalView().UserConn())
		response, err := client.Get(ctx, publicv1.SecretsGetRequest_builder{
			Id: secretID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetType()).To(Equal(publicv1.SecretType_SECRET_TYPE_WINDOWS_USER_DATA))
		Expect(response.GetObject().GetData()).To(Equal(map[string][]byte{"userdata": xmlData}))

		describeOutput, describeStderr, code := tool.RunCLI(ctx, homeDir, "describe", "secret", name)
		Expect(code).To(Equal(0), "describe failed: %s%s", describeOutput, describeStderr)
		Expect(describeOutput).To(ContainSubstring("Windows User Data"))
		Expect(describeOutput + describeStderr).ToNot(ContainSubstring(windowsUserDataCLISentinel))

		_, deleteStderr, code := tool.RunCLI(ctx, homeDir, "delete", "secrets", name)
		Expect(code).To(Equal(0), "delete failed: %s", deleteStderr)
		deletionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		Eventually(func() codes.Code {
			_, err := client.Get(deletionCtx, publicv1.SecretsGetRequest_builder{Id: secretID}.Build())
			return status.Code(err)
		}).WithContext(deletionCtx).WithTimeout(30 * time.Second).Should(Equal(codes.NotFound))
		created = false
	},
		Entry("nested UTF-8 XML with a BOM and declaration whitespace",
			[]byte("\xef\xbb\xbf<?xml version='1.0' ?><unattend><settings>"+windowsUserDataCLISentinel+"</settings></unattend>")),
		Entry("exactly 64 KiB", []byte("<root>"+windowsUserDataCLISentinel+
			strings.Repeat("a", windowsUserDataMaxBytes-len("<root></root>")-len(windowsUserDataCLISentinel))+"</root>")),
	)

	DescribeTable("rejects invalid Windows user-data Secrets without exposing content",
		func(ctx context.Context, key string, data []byte, wantMessage string) {
			_, stderr, exitCode := tool.LoginCLI(ctx, homeDir, userUsername, usersPassword)
			Expect(exitCode).To(Equal(0), "login failed: %s", stderr)

			name := "it-windows-user-data-invalid-" + uuid.New()[24:32]
			xmlPath := writeWindowsUserDataFile(homeDir, "invalid.xml", data)
			client := publicv1.NewSecretsClient(tool.ExternalView().UserConn())
			filter := fmt.Sprintf("this.metadata.name == %q", name)
			// Clean up even if a regression incorrectly persists the rejected input.
			DeferCleanup(func(ctx context.Context) {
				list, err := client.List(ctx, publicv1.SecretsListRequest_builder{Filter: &filter}.Build())
				Expect(err).ToNot(HaveOccurred())
				for _, secret := range list.GetItems() {
					_, err = client.Delete(ctx, publicv1.SecretsDeleteRequest_builder{Id: secret.GetId()}.Build())
					Expect(err).ToNot(HaveOccurred())
				}
			})

			stdout, stderr, code := runCreateWindowsUserDataCLI(ctx, homeDir, name, key+"="+xmlPath)
			output := stdout + "\n" + stderr
			Expect(code).ToNot(Equal(0), "create unexpectedly succeeded: %s", output)
			Expect(output).To(ContainSubstring(wantMessage))
			Expect(output).ToNot(ContainSubstring(windowsUserDataCLISentinel))

			list, err := client.List(ctx, publicv1.SecretsListRequest_builder{Filter: &filter}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(list.GetItems()).To(BeEmpty(), "rejected Secret was persisted")
		},
		Entry("missing userdata key", "not-userdata", []byte("<root>"+windowsUserDataCLISentinel+"</root>"),
			`requires a non-empty data["userdata"]`),
		Entry("empty userdata", "userdata", []byte{}, `requires a non-empty data["userdata"]`),
		Entry("malformed XML", "userdata", []byte("<root>"+windowsUserDataCLISentinel), "not well-formed XML"),
		Entry("unsupported XML encoding", "userdata",
			[]byte(`<?xml version="1.0" encoding="UTF-16"?><root>`+windowsUserDataCLISentinel+`</root>`), "unsupported encoding"),
		Entry("raw invalid UTF-8", "userdata", []byte("<root>\xff"+windowsUserDataCLISentinel+"</root>"), "unsupported encoding"),
		Entry("DTD", "userdata", []byte("<!DOCTYPE root><root>"+windowsUserDataCLISentinel+"</root>"), "prohibited XML directive"),
		Entry("oversized XML", "userdata",
			[]byte("<root>"+strings.Repeat("a", windowsUserDataMaxBytes)+windowsUserDataCLISentinel+"</root>"),
			"exceeds the maximum of 65536 bytes"),
	)
})

var _ = Describe("Windows user-data Secret workload admission", Label("compute-instances", "secrets"), func() {
	It("admits canonical Windows references and isolates rejected references", func(ctx context.Context) {
		conn := tool.ExternalView().UserConn()
		secrets := publicv1.NewSecretsClient(conn)
		instances := publicv1.NewComputeInstancesClient(conn)
		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		templateID := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		windowsImage := createWindowsUserDataDiskImageFixture(ctx,
			privatev1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS)
		linuxImage := createWindowsUserDataDiskImageFixture(ctx,
			privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX)

		windowsData := []byte("<unattend><settings>" + windowsUserDataVMSentinel + "</settings></unattend>")
		windowsSecret := createPublicUserDataSecretFixture(ctx, secrets,
			publicv1.SecretType_SECRET_TYPE_WINDOWS_USER_DATA, windowsData)
		Expect(windowsSecret.GetData()).To(BeEmpty(), "Secret Create must omit protected data")
		fetchedSecret, err := secrets.Get(ctx,
			publicv1.SecretsGetRequest_builder{Id: windowsSecret.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(fetchedSecret.GetObject().GetData()).To(Equal(
			map[string][]byte{"userdata": windowsData}))

		for _, reference := range []struct {
			label string
			value *publicv1.SecretLocalReference
		}{
			{
				label: "ID",
				value: publicv1.SecretLocalReference_builder{Id: windowsSecret.GetId()}.Build(),
			},
			{
				label: "name",
				value: publicv1.SecretLocalReference_builder{
					Name: windowsSecret.GetMetadata().GetName(),
				}.Build(),
			},
		} {
			By("creating and getting a Windows ComputeInstance by Secret " + reference.label)
			created, createErr := createComputeInstanceFixture(ctx, conn,
				windowsUserDataComputeSpec(templateID, windowsImage.GetId(), network, reference.value))
			Expect(createErr).ToNot(HaveOccurred())
			expectCanonicalUserDataSecret(created, windowsSecret.GetId(),
				windowsSecret.GetMetadata().GetName())
			expectSerializedMessageNotToContain(created, windowsUserDataVMSentinel)

			fetched, getErr := instances.Get(ctx,
				publicv1.ComputeInstancesGetRequest_builder{Id: created.GetId()}.Build())
			Expect(getErr).ToNot(HaveOccurred())
			expectCanonicalUserDataSecret(fetched.GetObject(), windowsSecret.GetId(),
				windowsSecret.GetMetadata().GetName())
			expectSerializedMessageNotToContain(fetched, windowsUserDataVMSentinel)
		}

		genericData := []byte("#cloud-config\n# " + windowsUserDataVMSentinel)
		genericSecret := createPublicUserDataSecretFixture(ctx, secrets,
			publicv1.SecretType_SECRET_TYPE_USER_DATA, genericData)
		linuxInstance, err := createComputeInstanceFixture(ctx, conn,
			windowsUserDataComputeSpec(templateID, linuxImage.GetId(), network,
				publicv1.SecretLocalReference_builder{Id: genericSecret.GetId()}.Build()))
		Expect(err).ToNot(HaveOccurred())
		expectCanonicalUserDataSecret(linuxInstance, genericSecret.GetId(),
			genericSecret.GetMetadata().GetName())
		expectSerializedMessageNotToContain(linuxInstance, windowsUserDataVMSentinel)

		otherProject := createCatalogItemProjectFixture(ctx, usersGroup)
		projectAdminConn := computeInstanceSSHKeyProjectAdminConn(ctx)
		projectAdminSecrets := publicv1.NewSecretsClient(projectAdminConn)
		projectAdminInstances := publicv1.NewComputeInstancesClient(projectAdminConn)
		otherProjectSecret := createPrivateUserDataSecretFixture(ctx, usersGroup,
			otherProject, privatev1.SecretType_SECRET_TYPE_WINDOWS_USER_DATA, windowsData)
		visible, err := projectAdminSecrets.Get(ctx,
			publicv1.SecretsGetRequest_builder{Id: otherProjectSecret.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred(), "the caller must be able to see both projects")
		Expect(visible.GetObject().GetData()).To(Equal(map[string][]byte{"userdata": windowsData}))

		otherTenant, _ := createCatalogItemTenantAdminFixture(ctx)
		otherTenantSecret := createPrivateUserDataSecretFixture(ctx, otherTenant, "",
			privatev1.SecretType_SECRET_TYPE_WINDOWS_USER_DATA, windowsData)
		sharedSecret := createPrivateUserDataSecretFixture(ctx, "shared", "",
			privatev1.SecretType_SECRET_TYPE_WINDOWS_USER_DATA, windowsData)

		for _, testCase := range []struct {
			label      string
			client     publicv1.ComputeInstancesClient
			cleanup    func(string)
			imageID    string
			secretID   string
			secretName string
			wantCode   codes.Code
			wantError  string
		}{
			{
				label: "generic USER_DATA on Windows", client: instances,
				cleanup:  func(id string) { deferComputeInstanceDeletion(id, conn) },
				imageID:  windowsImage.GetId(),
				secretID: genericSecret.GetId(), wantCode: codes.InvalidArgument,
				wantError: "SECRET_TYPE_WINDOWS_USER_DATA",
			},
			{
				label: "Windows user data on Linux", client: instances,
				cleanup:  func(id string) { deferComputeInstanceDeletion(id, conn) },
				imageID:  linuxImage.GetId(),
				secretID: windowsSecret.GetId(), wantCode: codes.InvalidArgument,
				wantError: "SECRET_TYPE_USER_DATA",
			},
			{
				label: "Secret from another tenant", client: instances,
				cleanup:  func(id string) { deferComputeInstanceDeletion(id, conn) },
				imageID:  windowsImage.GetId(),
				secretID: otherTenantSecret.GetId(),
			},
			{
				label: "shared Secret", client: instances,
				cleanup:  func(id string) { deferComputeInstanceDeletion(id, conn) },
				imageID:  windowsImage.GetId(),
				secretID: sharedSecret.GetId(), wantCode: codes.InvalidArgument,
				wantError: "there is no secret",
			},
			{
				label: "Secret by ID from another visible project", client: projectAdminInstances,
				cleanup:  func(id string) { deferComputeInstanceDeletion(id, projectAdminConn) },
				imageID:  windowsImage.GetId(),
				secretID: otherProjectSecret.GetId(), wantCode: codes.InvalidArgument,
				wantError: "there is no secret",
			},
			{
				label: "Secret by name from another visible project", client: projectAdminInstances,
				cleanup:    func(id string) { deferComputeInstanceDeletion(id, projectAdminConn) },
				imageID:    windowsImage.GetId(),
				secretName: otherProjectSecret.GetMetadata().GetName(), wantCode: codes.InvalidArgument,
				wantError: "there is no secret",
			},
		} {
			By("rejecting a " + testCase.label)
			name := "it-windows-user-data-vm-rejected-" + uuid.New()[24:32]
			secretRef := publicv1.SecretLocalReference_builder{Id: testCase.secretID}.Build()
			if testCase.secretName != "" {
				secretRef = publicv1.SecretLocalReference_builder{Name: testCase.secretName}.Build()
			}
			response, createErr := testCase.client.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: name}.Build(),
					Spec:     windowsUserDataComputeSpec(templateID, testCase.imageID, network, secretRef),
				}.Build(),
			}.Build())
			if createErr == nil {
				Expect(response.GetObject()).ToNot(BeNil())
				testCase.cleanup(response.GetObject().GetId())
			}
			Expect(createErr).To(HaveOccurred())
			if testCase.wantCode != codes.OK {
				Expect(status.Code(createErr)).To(Equal(testCase.wantCode))
			}
			if testCase.wantError != "" {
				Expect(status.Convert(createErr).Message()).To(ContainSubstring(testCase.wantError))
			}
			Expect(createErr.Error()).ToNot(ContainSubstring(windowsUserDataVMSentinel))
			expectNoComputeInstanceNamed(ctx, testCase.client, name)
		}
	})

	It("keeps BareMetalInstance user data restricted to generic USER_DATA", func(ctx context.Context) {
		conn := tool.ExternalView().UserConn()
		instances := publicv1.NewBareMetalInstancesClient(conn)
		_, err := instances.List(ctx, publicv1.BareMetalInstancesListRequest_builder{}.Build())
		if status.Code(err) == codes.Unavailable && status.Convert(err).Message() ==
			"the BMaaS service is not enabled on this server" {
			Skip("BMaaS is disabled on this Fulfillment server")
		}
		Expect(err).ToNot(HaveOccurred())

		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		image := createWindowsUserDataDiskImageFixture(ctx,
			privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX)
		template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
		instanceType := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)
		item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(),
			publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
					UserDataSource: publicv1.UserDataSourceFieldPolicy_builder{
						Editable: publicv1.EditableUserDataSourceField_builder{
							DefaultValue: publicv1.UserDataSource_builder{
								Inline: new("#cloud-config\n"),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
		secrets := publicv1.NewSecretsClient(conn)
		genericSecret := createPublicUserDataSecretFixture(ctx, secrets,
			publicv1.SecretType_SECRET_TYPE_USER_DATA,
			[]byte("#cloud-config\n# "+windowsUserDataVMSentinel))
		windowsSecret := createPublicUserDataSecretFixture(ctx, secrets,
			publicv1.SecretType_SECRET_TYPE_WINDOWS_USER_DATA,
			[]byte("<unattend>"+windowsUserDataVMSentinel+"</unattend>"))

		spec := func(secretID string) *publicv1.BareMetalInstanceSpec {
			return publicv1.BareMetalInstanceSpec_builder{
				CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{
					Id: item.GetId(),
				}.Build(),
				DiskImage: publicv1.DiskImageReference_builder{Id: image.GetId()}.Build(),
				UserDataSource: publicv1.UserDataSource_builder{
					Secret: publicv1.SecretLocalReference_builder{Id: secretID}.Build(),
				}.Build(),
				InstanceType: publicv1.BareMetalInstanceTypeReference_builder{
					Id: instanceType, Shared: true,
				}.Build(),
				NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{
					network.bareMetalInstanceAttachment(),
				},
			}.Build()
		}

		created, err := createBareMetalInstanceFixture(ctx, conn, spec(genericSecret.GetId()))
		Expect(err).ToNot(HaveOccurred())
		expectCanonicalBareMetalUserDataSecret(created, genericSecret.GetId(),
			genericSecret.GetMetadata().GetName())
		expectSerializedMessageNotToContain(created, windowsUserDataVMSentinel)

		name := "it-windows-user-data-bmi-rejected-" + uuid.New()[24:32]
		_, err = instances.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{Name: name}.Build(),
				Spec:     spec(windowsSecret.GetId()),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("SECRET_TYPE_USER_DATA"))
		Expect(err.Error()).ToNot(ContainSubstring(windowsUserDataVMSentinel))
		listed, listErr := instances.List(ctx, publicv1.BareMetalInstancesListRequest_builder{
			Filter: new(fmt.Sprintf("this.metadata.name == %q", name)),
		}.Build())
		Expect(listErr).ToNot(HaveOccurred())
		Expect(listed.GetItems()).To(BeEmpty())
	})
})

func createPublicUserDataSecretFixture(
	ctx context.Context,
	client publicv1.SecretsClient,
	secretType publicv1.SecretType,
	data []byte,
) *publicv1.Secret {
	GinkgoHelper()
	response, err := client.Create(ctx, publicv1.SecretsCreateRequest_builder{
		Object: publicv1.Secret_builder{
			Metadata: publicv1.Metadata_builder{
				Name: "it-windows-user-data-api-" + uuid.New()[24:32],
			}.Build(),
			Type: secretType,
			Data: map[string][]byte{"userdata": data},
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	secret := response.GetObject()
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx,
			publicv1.SecretsDeleteRequest_builder{Id: secret.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
	})
	return secret
}

func createPrivateUserDataSecretFixture(
	ctx context.Context,
	tenant, project string,
	secretType privatev1.SecretType,
	data []byte,
) *privatev1.Secret {
	GinkgoHelper()
	client := privatev1.NewSecretsClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    "it-windows-user-data-scope-" + uuid.New()[24:32],
				Tenant:  tenant,
				Project: project,
			}.Build(),
			Type: secretType,
			Data: map[string][]byte{"userdata": data},
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	secret := response.GetObject()
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx,
			privatev1.SecretsDeleteRequest_builder{Id: secret.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
	})
	return secret
}

func createWindowsUserDataDiskImageFixture(
	ctx context.Context,
	family privatev1.GuestOSFamily,
) *privatev1.DiskImage {
	GinkgoHelper()
	client := privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
		Object: privatev1.DiskImage_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "it-windows-user-data-image-" + uuid.New()[24:32],
				Tenant: "shared",
			}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:     "quay.io/containerdisks/test:latest",
				GuestOsFamily: family,
				Architecture: []privatev1.Architecture{
					privatev1.Architecture_ARCHITECTURE_AMD64,
				},
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	image := response.GetObject()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx,
			privatev1.DiskImagesDeleteRequest_builder{Id: image.GetId()}.Build())
		return err
	}, nil)
	return image
}

func windowsUserDataComputeSpec(
	templateID, imageID string,
	network catalogItemNetworkFixture,
	secret *publicv1.SecretLocalReference,
) *publicv1.ComputeInstanceSpec {
	return publicv1.ComputeInstanceSpec_builder{
		Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
		DiskImage: publicv1.DiskImageReference_builder{Id: imageID}.Build(),
		UserDataSource: publicv1.UserDataSource_builder{
			Secret: secret,
		}.Build(),
		NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
			network.computeInstanceAttachment(),
		},
	}.Build()
}

func expectCanonicalUserDataSecret(object *publicv1.ComputeInstance, id, name string) {
	GinkgoHelper()
	ref := object.GetSpec().GetUserDataSource().GetSecret()
	Expect(ref).ToNot(BeNil())
	Expect(ref.GetId()).To(Equal(id))
	Expect(ref.GetName()).To(Equal(name))
}

func expectCanonicalBareMetalUserDataSecret(object *publicv1.BareMetalInstance, id, name string) {
	GinkgoHelper()
	ref := object.GetSpec().GetUserDataSource().GetSecret()
	Expect(ref).ToNot(BeNil())
	Expect(ref.GetId()).To(Equal(id))
	Expect(ref.GetName()).To(Equal(name))
}

func expectSerializedMessageNotToContain(message proto.Message, value string) {
	GinkgoHelper()
	serialized, err := protojson.Marshal(message)
	Expect(err).ToNot(HaveOccurred())
	Expect(string(serialized)).ToNot(ContainSubstring(value))
}

func expectNoComputeInstanceNamed(
	ctx context.Context,
	client publicv1.ComputeInstancesClient,
	name string,
) {
	GinkgoHelper()
	listed, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{
		Filter: new(fmt.Sprintf("this.metadata.name == %q", name)),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	Expect(listed.GetItems()).To(BeEmpty())
}

func writeWindowsUserDataFile(homeDir, name string, data []byte) string {
	path := filepath.Join(homeDir, name)
	ExpectWithOffset(1, os.WriteFile(path, data, 0600)).To(Succeed())
	return path
}

func runCreateWindowsUserDataCLI(ctx context.Context, homeDir, name, fromFile string) (string, string, int) {
	return tool.RunCLI(ctx, homeDir,
		"create", "secret",
		"--name", name,
		"--type", "windows-user-data",
		"--from-file", fromFile,
	)
}
