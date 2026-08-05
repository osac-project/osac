/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package idp

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("ProjectGroupManager", func() {
	var (
		ctrl       *gomock.Controller
		mockClient *MockClientInterface
		ctx        = context.Background()
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockClient = NewMockClientInterface(ctrl)
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Describe("Builder validation", func() {
		It("should fail when logger is not set", func() {
			_, err := NewProjectGroupManager().
				SetClient(mockClient).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
		})

		It("should fail when client is not set", func() {
			_, err := NewProjectGroupManager().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("client is mandatory"))
		})
	})

	Describe("DeleteProjectGroups", func() {
		var manager *ProjectGroupManager

		BeforeEach(func() {
			var err error
			manager, err = NewProjectGroupManager().
				SetLogger(logger).
				SetClient(mockClient).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should delete parent project group only (cascade delete)", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project").
				Return("group-id-123", nil)

			mockClient.EXPECT().
				DeleteGroup(gomock.Any(), "test-org", "group-id-123").
				Return(nil)

			err := manager.DeleteProjectGroups(ctx, "test-org", "test-project")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil when parent group is not found", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project").
				Return("", errors.New("organization group not found: /test-project"))

			err := manager.DeleteProjectGroups(ctx, "test-org", "test-project")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should propagate non-not-found errors from GetGroupIDByPath", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project").
				Return("", errors.New("network error: connection timeout"))

			err := manager.DeleteProjectGroups(ctx, "test-org", "test-project")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get project group ID"))
			Expect(err.Error()).To(ContainSubstring("network error"))
		})

		It("should return error when deletion fails", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project").
				Return("group-id-123", nil)

			mockClient.EXPECT().
				DeleteGroup(gomock.Any(), "test-org", "group-id-123").
				Return(errors.New("keycloak error"))

			err := manager.DeleteProjectGroups(ctx, "test-org", "test-project")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to delete project group"))
		})

		It("should return error when tenant is empty", func() {
			err := manager.DeleteProjectGroups(ctx, "", "test-project")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tenant is required"))
		})

		It("should reject project name with dot-dot sequence", func() {
			err := manager.DeleteProjectGroups(ctx, "test-org", "../malicious")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project name cannot contain '..' sequence"))
		})
	})

	Describe("CreateProjectGroups", func() {
		var manager *ProjectGroupManager

		BeforeEach(func() {
			var err error
			manager, err = NewProjectGroupManager().
				SetLogger(logger).
				SetClient(mockClient).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should create hierarchical viewers and managers groups", func() {
			mockClient.EXPECT().
				CreateGroup(gomock.Any(), "test-org", "/test-project/system:viewers").
				Return("viewers-group-id", nil)

			mockClient.EXPECT().
				CreateGroup(gomock.Any(), "test-org", "/test-project/system:managers").
				Return("managers-group-id", nil)

			managersID, err := manager.CreateProjectGroups(ctx, "test-org", "test-project")
			Expect(err).ToNot(HaveOccurred())
			Expect(managersID).To(Equal("managers-group-id"))
		})

		It("should rollback viewers group when managers group creation fails", func() {
			mockClient.EXPECT().
				CreateGroup(gomock.Any(), "test-org", "/test-project/system:viewers").
				Return("viewers-group-id", nil)

			mockClient.EXPECT().
				CreateGroup(gomock.Any(), "test-org", "/test-project/system:managers").
				Return("", errors.New("keycloak error"))

			mockClient.EXPECT().
				DeleteGroup(gomock.Any(), "test-org", "viewers-group-id").
				Return(nil)

			managersID, err := manager.CreateProjectGroups(ctx, "test-org", "test-project")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create managers group"))
			Expect(managersID).To(BeEmpty())
		})

		It("should return error when viewers group creation fails", func() {
			mockClient.EXPECT().
				CreateGroup(gomock.Any(), "test-org", "/test-project/system:viewers").
				Return("", errors.New("keycloak error"))

			managersID, err := manager.CreateProjectGroups(ctx, "test-org", "test-project")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create viewers group"))
			Expect(managersID).To(BeEmpty())
		})

		It("should return error when tenant is empty", func() {
			managersID, err := manager.CreateProjectGroups(ctx, "", "test-project")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tenant is required"))
			Expect(managersID).To(BeEmpty())
		})

		It("should reject project path with dot-dot sequence", func() {
			managersID, err := manager.CreateProjectGroups(ctx, "test-org", "../malicious")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project path cannot contain '..' sequence"))
			Expect(managersID).To(BeEmpty())
		})

		It("should handle empty project path for tenant-level groups", func() {
			// When project path is empty, groups should be created at root level
			// without double slashes (was "//system:viewers", should be "/system:viewers")
			mockClient.EXPECT().
				CreateGroup(gomock.Any(), "test-org", "/system:viewers").
				Return("viewers-group-id", nil)

			mockClient.EXPECT().
				CreateGroup(gomock.Any(), "test-org", "/system:managers").
				Return("managers-group-id", nil)

			managersID, err := manager.CreateProjectGroups(ctx, "test-org", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(managersID).To(Equal("managers-group-id"))
		})

		It("should add leading slash to project path if missing", func() {
			mockClient.EXPECT().
				CreateGroup(gomock.Any(), "test-org", "/test-project/system:viewers").
				Return("viewers-group-id", nil)

			mockClient.EXPECT().
				CreateGroup(gomock.Any(), "test-org", "/test-project/system:managers").
				Return("managers-group-id", nil)

			managersID, err := manager.CreateProjectGroups(ctx, "test-org", "test-project")
			Expect(err).ToNot(HaveOccurred())
			Expect(managersID).To(Equal("managers-group-id"))
		})
	})

	Describe("AddUserToProjectGroup", func() {
		var manager *ProjectGroupManager

		BeforeEach(func() {
			var err error
			manager, err = NewProjectGroupManager().
				SetLogger(logger).
				SetClient(mockClient).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should add user to managers group", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project/system:managers").
				Return("managers-group-id", nil)

			mockClient.EXPECT().
				AddUserToGroup(gomock.Any(), "test-org", "user-123", "managers-group-id").
				Return(nil)

			err := manager.AddUserToProjectGroup(ctx, "test-org", "test-project", "user-123", GroupNameManagers)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should add user to viewers group", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project/system:viewers").
				Return("viewers-group-id", nil)

			mockClient.EXPECT().
				AddUserToGroup(gomock.Any(), "test-org", "user-456", "viewers-group-id").
				Return(nil)

			err := manager.AddUserToProjectGroup(ctx, "test-org", "test-project", "user-456", GroupNameViewers)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error when tenant is empty", func() {
			err := manager.AddUserToProjectGroup(ctx, "", "test-project", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tenant is required"))
		})

		It("should return error when project name is empty", func() {
			err := manager.AddUserToProjectGroup(ctx, "test-org", "", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project name is required"))
		})

		It("should return error when username is empty", func() {
			err := manager.AddUserToProjectGroup(ctx, "test-org", "test-project", "", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("username is required"))
		})

		It("should return error for invalid group type", func() {
			err := manager.AddUserToProjectGroup(ctx, "test-org", "test-project", "user-123", "invalid-group")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid group type"))
		})

		It("should return error when group lookup fails", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project/system:managers").
				Return("", errors.New("group not found"))

			err := manager.AddUserToProjectGroup(ctx, "test-org", "test-project", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get group ID"))
		})

		It("should return error when adding user to group fails", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project/system:managers").
				Return("managers-group-id", nil)

			mockClient.EXPECT().
				AddUserToGroup(gomock.Any(), "test-org", "user-123", "managers-group-id").
				Return(errors.New("keycloak error"))

			err := manager.AddUserToProjectGroup(ctx, "test-org", "test-project", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to add user to group"))
		})

		It("should reject project name with dot-dot sequence", func() {
			err := manager.AddUserToProjectGroup(ctx, "test-org", "../malicious", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project name cannot contain '..' sequence"))
		})
	})

	Describe("RemoveUserFromProjectGroup", func() {
		var manager *ProjectGroupManager

		BeforeEach(func() {
			var err error
			manager, err = NewProjectGroupManager().
				SetLogger(logger).
				SetClient(mockClient).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should remove user from managers group", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project/system:managers").
				Return("managers-group-id", nil)

			mockClient.EXPECT().
				RemoveUserFromGroup(gomock.Any(), "test-org", "user-123", "managers-group-id").
				Return(nil)

			err := manager.RemoveUserFromProjectGroup(ctx, "test-org", "test-project", "user-123", GroupNameManagers)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should remove user from viewers group", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project/system:viewers").
				Return("viewers-group-id", nil)

			mockClient.EXPECT().
				RemoveUserFromGroup(gomock.Any(), "test-org", "user-456", "viewers-group-id").
				Return(nil)

			err := manager.RemoveUserFromProjectGroup(ctx, "test-org", "test-project", "user-456", GroupNameViewers)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error when tenant is empty", func() {
			err := manager.RemoveUserFromProjectGroup(ctx, "", "test-project", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tenant is required"))
		})

		It("should return error when project name is empty", func() {
			err := manager.RemoveUserFromProjectGroup(ctx, "test-org", "", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project name is required"))
		})

		It("should return error when username is empty", func() {
			err := manager.RemoveUserFromProjectGroup(ctx, "test-org", "test-project", "", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("username is required"))
		})

		It("should return error for invalid group type", func() {
			err := manager.RemoveUserFromProjectGroup(ctx, "test-org", "test-project", "user-123", "invalid-group")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid group type"))
		})

		It("should return error when group lookup fails", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project/system:managers").
				Return("", errors.New("group not found"))

			err := manager.RemoveUserFromProjectGroup(ctx, "test-org", "test-project", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get group ID"))
		})

		It("should return error when removing user from group fails", func() {
			mockClient.EXPECT().
				GetGroupIDByPath(gomock.Any(), "test-org", "/test-project/system:managers").
				Return("managers-group-id", nil)

			mockClient.EXPECT().
				RemoveUserFromGroup(gomock.Any(), "test-org", "user-123", "managers-group-id").
				Return(errors.New("keycloak error"))

			err := manager.RemoveUserFromProjectGroup(ctx, "test-org", "test-project", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to remove user from group"))
		})

		It("should reject project name with dot-dot sequence", func() {
			err := manager.RemoveUserFromProjectGroup(ctx, "test-org", "../malicious", "user-123", GroupNameManagers)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("project name cannot contain '..' sequence"))
		})
	})
})
