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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const computeInstanceSSHKeyNotFoundError = "SSH key Secret was not found or is not visible in the selected project."

func createComputeInstanceSSHKeySecretFixture(
	ctx context.Context,
	tenant, project, name string,
) *privatev1.Secret {
	GinkgoHelper()
	client := privatev1.NewSecretsClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant, Project: project}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"public_key": []byte(catalogItemFixtureSSHPublicKey)},
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	secret := response.GetObject()
	id := secret.GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return secret
}

func createComputeInstanceSSHKeyProjectFixture(ctx context.Context, tenant string, conn *grpc.ClientConn) string {
	GinkgoHelper()
	client := publicv1.NewProjectsClient(conn)
	name := catalogItemFixtureName()
	result, err := client.Create(ctx, publicv1.ProjectsCreateRequest_builder{
		Object: publicv1.Project_builder{
			Metadata: publicv1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := result.GetObject().GetId()
	cleanupClient := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := cleanupClient.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	Eventually(func(g Gomega) {
		response, err := client.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: id}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		if err != nil || response == nil || response.GetObject() == nil {
			return
		}
		g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(publicv1.ProjectState_PROJECT_STATE_ACTIVE),
			response.GetObject().GetStatus().GetMessage())
	}, 2*time.Minute, time.Second).Should(Succeed())
	return name
}

func grantComputeInstanceSSHKeyProjectAccess(ctx context.Context, tenant, project string) {
	GinkgoHelper()

	// Trigger just-in-time provisioning so the regular user can be referenced by ID.
	_, err := publicv1.NewSecretsClient(tool.ExternalView().UserConn()).List(ctx,
		publicv1.SecretsListRequest_builder{}.Build())
	Expect(err).NotTo(HaveOccurred())

	users := privatev1.NewUsersClient(tool.InternalView().AdminConn())
	filter := "this.metadata.name == '" + userUsername + "' && this.metadata.tenant == '" + tenant + "'"
	usersResponse, err := users.List(ctx, privatev1.UsersListRequest_builder{Filter: &filter}.Build())
	Expect(err).NotTo(HaveOccurred())
	Expect(usersResponse.GetItems()).To(HaveLen(1))
	userID := usersResponse.GetItems()[0].GetId()

	// Membership reconciliation requires the project groups and Keycloak user to exist.
	projects := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
	projectFilter := "this.metadata.name == '" + project + "' && this.metadata.tenant == '" + tenant + "'"
	Eventually(func(g Gomega) {
		projectsResponse, listErr := projects.List(ctx,
			privatev1.ProjectsListRequest_builder{Filter: &projectFilter}.Build())
		g.Expect(listErr).NotTo(HaveOccurred())
		g.Expect(projectsResponse.GetItems()).To(HaveLen(1))
		projectObject := projectsResponse.GetItems()[0]
		g.Expect(projectObject.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE),
			projectObject.GetStatus().GetMessage())
	}, 2*time.Minute, time.Second).Should(Succeed())

	Eventually(func(g Gomega) {
		userResponse, getErr := users.Get(ctx, privatev1.UsersGetRequest_builder{Id: userID}.Build())
		g.Expect(getErr).NotTo(HaveOccurred())
		g.Expect(userResponse.GetObject().GetStatus().GetKeycloakUserId()).NotTo(BeEmpty())
	}, 2*time.Minute, time.Second).Should(Succeed())

	client := privatev1.NewProjectMembershipsClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.ProjectMembershipsCreateRequest_builder{
		Object: privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    catalogItemFixtureName(),
				Tenant:  tenant,
				Project: project,
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Role: privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
				Users: []*privatev1.UserReference{
					privatev1.UserReference_builder{Id: userID}.Build(),
				},
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.ProjectMembershipsDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	Eventually(func(g Gomega) {
		membership, getErr := client.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{Id: id}.Build())
		g.Expect(getErr).NotTo(HaveOccurred())
		g.Expect(membership.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
		), membership.GetObject().GetStatus().GetMessage())
		g.Expect(membership.GetObject().GetStatus().GetUsers()).To(HaveLen(1))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func computeInstanceSSHKeyProjectUserConn(ctx context.Context, username string) *grpc.ClientConn {
	GinkgoHelper()
	tokenSource, err := tool.makeKeycloakTokenSource(ctx, username, usersPassword)
	Expect(err).NotTo(HaveOccurred())
	conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(conn.Close()).To(Succeed()) })
	return conn
}

func computeInstanceSSHKeyProjectAdminConn(ctx context.Context) *grpc.ClientConn {
	GinkgoHelper()

	// Provision the dedicated project owner before assigning its tenant-admin role.
	bootstrapConn := computeInstanceSSHKeyProjectUserConn(ctx, sshKeyProjectOwnerUsername)
	_, err := publicv1.NewSecretsClient(bootstrapConn).List(ctx, publicv1.SecretsListRequest_builder{}.Build())
	Expect(err).NotTo(HaveOccurred())

	users := privatev1.NewUsersClient(tool.InternalView().AdminConn())
	filter := "this.metadata.name == '" + sshKeyProjectOwnerUsername + "' && this.metadata.tenant == '" + usersGroup + "'"
	response, err := users.List(ctx, privatev1.UsersListRequest_builder{Filter: &filter}.Build())
	Expect(err).NotTo(HaveOccurred())
	Expect(response.GetItems()).To(HaveLen(1))
	userID := response.GetItems()[0].GetId()
	var keycloakUserID string
	Eventually(func(g Gomega) {
		userResponse, getErr := users.Get(ctx, privatev1.UsersGetRequest_builder{Id: userID}.Build())
		g.Expect(getErr).NotTo(HaveOccurred())
		keycloakUserID = userResponse.GetObject().GetStatus().GetKeycloakUserId()
		g.Expect(keycloakUserID).NotTo(BeEmpty())
	}, 2*time.Minute, time.Second).Should(Succeed())
	grantTenantAdminRealmRole(ctx, keycloakUserID)

	// Mint a fresh token after the realm role is assigned.
	return computeInstanceSSHKeyProjectUserConn(ctx, sshKeyProjectOwnerUsername)
}

func runComputeInstanceSSHKeyCLI(ctx context.Context, home string, args ...string) (string, string, int) {
	GinkgoHelper()
	// The CLI harness uses an allowlisted environment, so pass through its local DNS shim.
	var extraEnv []string
	for _, name := range []string{"GODEBUG", "LD_PRELOAD"} {
		if value := os.Getenv(name); value != "" {
			extraEnv = append(extraEnv, name+"="+value)
		}
	}
	return tool.RunCLIWithEnv(ctx, home, extraEnv, args...)
}

func loginComputeInstanceSSHKeyCLI(ctx context.Context, home string) (string, string, int) {
	GinkgoHelper()
	return runComputeInstanceSSHKeyCLI(ctx, home, "login", "https://"+externalServiceAddr,
		"--flow=password", "--user="+userUsername, "--password="+usersPassword, "--insecure")
}

var _ = Describe("ComputeInstance SSH key project scope", Label("compute-instances", "ssh-keys"), func() {
	It("resolves SSH key names and IDs in the selected tenant project", func(ctx context.Context) {
		userConn := computeInstanceSSHKeyProjectUserConn(ctx, userUsername)
		projectAdminConn := computeInstanceSSHKeyProjectAdminConn(ctx)
		apps := createComputeInstanceSSHKeyProjectFixture(ctx, usersGroup, projectAdminConn)
		ops := createComputeInstanceSSHKeyProjectFixture(ctx, usersGroup, projectAdminConn)
		grantComputeInstanceSSHKeyProjectAccess(ctx, usersGroup, apps)
		grantComputeInstanceSSHKeyProjectAccess(ctx, usersGroup, ops)
		name := catalogItemFixtureName()
		appsSecret := createComputeInstanceSSHKeySecretFixture(ctx, usersGroup, apps, name)
		opsSecret := createComputeInstanceSSHKeySecretFixture(ctx, usersGroup, ops, name)
		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		client := publicv1.NewComputeInstancesClient(userConn)

		for _, testCase := range []struct {
			label     string
			secret    *privatev1.Secret
			reference *publicv1.SecretReference
		}{
			{
				label:     "name",
				secret:    appsSecret,
				reference: publicv1.SecretReference_builder{Name: name, Project: apps}.Build(),
			},
			{
				label:     "ID",
				secret:    opsSecret,
				reference: publicv1.SecretReference_builder{Id: opsSecret.GetId(), Project: ops}.Build(),
			},
		} {
			By("creating a ComputeInstance with an SSH key selected by " + testCase.label)
			createdResponse, err := client.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
						SshKey:             testCase.reference,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			created := createdResponse.GetObject()
			deferComputeInstanceDeletion(created.GetId(), userConn)
			stored, err := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: created.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			ref := stored.GetObject().GetSpec().GetSshKey()
			Expect(ref.GetId()).To(Equal(testCase.secret.GetId()))
			Expect(ref.GetName()).To(Equal(name))
			Expect(ref.GetProject()).To(Equal(testCase.secret.GetMetadata().GetProject()))
			Expect(ref.GetShared()).To(BeFalse())
		}
	})

	It("resolves omitted and empty project values in the tenant's unnamed project", func(ctx context.Context) {
		apps := createCatalogItemProjectFixture(ctx, usersGroup)
		name := catalogItemFixtureName()
		rootSecret := createComputeInstanceSSHKeySecretFixture(ctx, usersGroup, "", name)
		_ = createComputeInstanceSSHKeySecretFixture(ctx, usersGroup, apps, name)
		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())

		created, err := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), publicv1.ComputeInstanceSpec_builder{
			Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
			NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
			SshKey:             publicv1.SecretReference_builder{Name: name}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		stored, err := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: created.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.GetObject().GetSpec().GetSshKey().GetId()).To(Equal(rootSecret.GetId()))
		Expect(stored.GetObject().GetSpec().GetSshKey().GetProject()).To(BeEmpty())

		body, err := json.Marshal(map[string]any{
			"metadata": map[string]any{"name": catalogItemFixtureName()},
			"spec": map[string]any{
				"template": map[string]any{"id": template},
				"ssh_key":  map[string]any{"name": name, "project": ""},
				"network_attachments": []any{map[string]any{
					"subnet":          map[string]any{"id": network.subnetID},
					"security_groups": []any{map[string]any{"id": network.securityGroupID}},
				}},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"/api/fulfillment/v1/compute_instances", bytes.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Content-Type", "application/json")
		response, err := tool.ExternalView().UserClient().Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer response.Body.Close()
		content, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK), string(content))
		var result map[string]any
		Expect(json.Unmarshal(content, &result)).To(Succeed())
		instanceID, ok := result["id"].(string)
		Expect(ok).To(BeTrue())
		deferComputeInstanceDeletion(instanceID, tool.ExternalView().UserConn())
		stored, err = client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: instanceID}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.GetObject().GetSpec().GetSshKey().GetId()).To(Equal(rootSecret.GetId()))
		Expect(stored.GetObject().GetSpec().GetSshKey().GetProject()).To(BeEmpty())
	})

	It("returns the same non-disclosing error when the SSH key Secret is hidden, out of scope, missing, or in a project the user cannot access", func(ctx context.Context) {
		userConn := computeInstanceSSHKeyProjectUserConn(ctx, userUsername)
		projectAdminConn := computeInstanceSSHKeyProjectAdminConn(ctx)
		apps := createComputeInstanceSSHKeyProjectFixture(ctx, usersGroup, projectAdminConn)
		grantComputeInstanceSSHKeyProjectAccess(ctx, usersGroup, apps)
		ops := createComputeInstanceSSHKeyProjectFixture(ctx, usersGroup, projectAdminConn)
		keyName := catalogItemFixtureName()
		outOfProject := createComputeInstanceSSHKeySecretFixture(ctx, usersGroup, ops, keyName)
		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		client := publicv1.NewComputeInstancesClient(userConn)

		By("rejecting a Secret in a project the user cannot access")
		instanceName := catalogItemFixtureName()
		_, err := client.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{Name: instanceName}.Build(),
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
					NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
					SshKey:             publicv1.SecretReference_builder{Id: outOfProject.GetId(), Project: ops}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
		Expect(status.Convert(err).Message()).To(Equal(computeInstanceSSHKeyNotFoundError))
		listed, listErr := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{
			Filter: new("this.metadata.name == '" + instanceName + "'"),
		}.Build())
		Expect(listErr).NotTo(HaveOccurred())
		Expect(listed.GetItems()).To(BeEmpty())

		// Grant access so the remaining out-of-scope case uses a visible Secret.
		grantComputeInstanceSSHKeyProjectAccess(ctx, usersGroup, ops)
		otherTenant, _ := createCatalogItemTenantAdminFixture(ctx)
		hidden := createComputeInstanceSSHKeySecretFixture(ctx, otherTenant, "", catalogItemFixtureName())

		for _, testCase := range []struct {
			label string
			ref   *publicv1.SecretReference
		}{
			{label: "hidden ID", ref: publicv1.SecretReference_builder{Id: hidden.GetId()}.Build()},
			{label: "hidden name", ref: publicv1.SecretReference_builder{Name: hidden.GetMetadata().GetName()}.Build()},
			{label: "out-of-project ID", ref: publicv1.SecretReference_builder{Id: outOfProject.GetId(), Project: apps}.Build()},
			{label: "out-of-project name", ref: publicv1.SecretReference_builder{Name: keyName, Project: apps}.Build()},
			{label: "missing ID", ref: publicv1.SecretReference_builder{Id: "missing-ssh-key"}.Build()},
			{label: "missing name", ref: publicv1.SecretReference_builder{Name: "missing-ssh-key"}.Build()},
		} {
			By("rejecting " + testCase.label)
			instanceName := catalogItemFixtureName()
			_, err := client.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: instanceName}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
						SshKey:             testCase.ref,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			Expect(status.Convert(err).Message()).To(Equal(computeInstanceSSHKeyNotFoundError))
			listed, listErr := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{
				Filter: new("this.metadata.name == '" + instanceName + "'"),
			}.Build())
			Expect(listErr).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())
		}
	})

	It("accepts bare-name and project-aware SSH key values through the CLI", func(ctx context.Context) {
		home, err := tool.NewCLIHomeDir()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(home)).To(Succeed()) })
		projectAdminConn := computeInstanceSSHKeyProjectAdminConn(ctx)
		apps := createComputeInstanceSSHKeyProjectFixture(ctx, usersGroup, projectAdminConn)
		grantComputeInstanceSSHKeyProjectAccess(ctx, usersGroup, apps)
		_, stderr, code := loginComputeInstanceSSHKeyCLI(ctx, home)
		Expect(code).To(Equal(0), stderr)
		name := catalogItemFixtureName()
		rootSecret := createComputeInstanceSSHKeySecretFixture(ctx, usersGroup, "", name)
		appsSecret := createComputeInstanceSSHKeySecretFixture(ctx, usersGroup, apps, name)
		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())

		for _, testCase := range []struct {
			value  string
			secret *privatev1.Secret
		}{
			{value: name, secret: rootSecret},
			{value: "name=" + name + ",project=" + apps, secret: appsSecret},
		} {
			instanceName := catalogItemFixtureName()
			_, stderr, code = runComputeInstanceSSHKeyCLI(ctx, home, "create", "computeinstance",
				"--template", template, "--name", instanceName,
				"--ssh-key", testCase.value,
				"--network-attachment", "subnet="+network.subnetID+",security-groups="+network.securityGroupID)
			Expect(code).To(Equal(0), stderr)
			listed, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{
				Filter: new("this.metadata.name == '" + instanceName + "'"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(HaveLen(1))
			instance := listed.GetItems()[0]
			deferComputeInstanceDeletion(instance.GetId(), tool.ExternalView().UserConn())
			Expect(instance.GetSpec().GetSshKey().GetId()).To(Equal(testCase.secret.GetId()))
			Expect(instance.GetSpec().GetSshKey().GetProject()).To(Equal(testCase.secret.GetMetadata().GetProject()))
			Expect(instance.GetSpec().GetSshKey().GetShared()).To(BeFalse())
		}
	})

	It("applies a named-project CatalogItem SSH default through the CLI", func(ctx context.Context) {
		home, err := tool.NewCLIHomeDir()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(home)).To(Succeed()) })
		projectAdminConn := computeInstanceSSHKeyProjectAdminConn(ctx)
		project := createComputeInstanceSSHKeyProjectFixture(ctx, usersGroup, projectAdminConn)
		grantComputeInstanceSSHKeyProjectAccess(ctx, usersGroup, project)
		_, stderr, code := loginComputeInstanceSSHKeyCLI(ctx, home)
		Expect(code).To(Equal(0), stderr)
		secret := createComputeInstanceSSHKeySecretFixture(ctx, usersGroup, project, catalogItemFixtureName())
		template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
			Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
			Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
			Published: true,
			Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
				SshKey: publicv1.FullSecretReferenceFieldPolicy_builder{
					Editable: publicv1.EditableFullSecretReferenceField_builder{
						DefaultValue: publicv1.SecretReference_builder{Id: secret.GetId(), Project: project}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		instanceName := catalogItemFixtureName()
		_, stderr, code = runComputeInstanceSSHKeyCLI(ctx, home, "create", "computeinstance", "--catalog-item", item.GetId(),
			"--name", instanceName,
			"--network-attachment", "subnet="+network.subnetID+",security-groups="+network.securityGroupID)
		Expect(code).To(Equal(0), stderr)
		listed, err := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn()).List(ctx,
			publicv1.ComputeInstancesListRequest_builder{Filter: new("this.metadata.name == '" + instanceName + "'")}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.GetItems()).To(HaveLen(1))
		instance := listed.GetItems()[0]
		deferComputeInstanceDeletion(instance.GetId(), tool.ExternalView().UserConn())
		Expect(instance.GetSpec().GetSshKey().GetId()).To(Equal(secret.GetId()))
		Expect(instance.GetSpec().GetSshKey().GetName()).To(Equal(secret.GetMetadata().GetName()))
		Expect(instance.GetSpec().GetSshKey().GetProject()).To(Equal(project))
		Expect(instance.GetSpec().GetSshKey().GetShared()).To(BeFalse())
	})
})
