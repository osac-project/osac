package it

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func getOrCreateKeycloakTenantAdminRole(ctx context.Context) map[string]any {
	GinkgoHelper()
	roleStatus, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/roles/tenant-admin", nil)
	Expect(err).ToNot(HaveOccurred())
	if roleStatus == http.StatusNotFound {
		code, _, createErr := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/roles", map[string]any{
			"name": "tenant-admin",
		})
		Expect(createErr).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusCreated))
		roleStatus, body, err = tool.KeycloakAdminRequest(ctx, http.MethodGet, "/roles/tenant-admin", nil)
		Expect(err).ToNot(HaveOccurred())
	}
	Expect(roleStatus).To(Equal(http.StatusOK))
	var role map[string]any
	Expect(json.Unmarshal(body, &role)).To(Succeed())
	return role
}

func assignKeycloakRealmRole(ctx context.Context, userID string, role map[string]any) {
	GinkgoHelper()
	code, _, err := tool.KeycloakAdminRequest(
		ctx, http.MethodPost, fmt.Sprintf("/users/%s/role-mappings/realm", userID), []map[string]any{role},
	)
	Expect(err).ToNot(HaveOccurred())
	Expect(code).To(Equal(http.StatusNoContent))
}
