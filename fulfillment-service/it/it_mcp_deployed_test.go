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
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
)

var _ = Describe("deployed MCP server", func() {
	It("serves MCP tools through TLS with a user-scoped OAuth token", func() {
		endpoint := strings.TrimSuffix(os.Getenv("IT_MCP_DEPLOYED_URL"), "/")
		if endpoint == "" {
			Skip("IT_MCP_DEPLOYED_URL is not set")
		}
		Expect(endpoint).To(HavePrefix("https://"))
		resourceURL := endpoint + "/"
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		baseTransport := &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: tool.caPool.Pool(), MinVersion: tls.VersionTLS12,
		}}
		defer baseTransport.CloseIdleConnections()
		baseClient := &http.Client{Transport: baseTransport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}

		Expect(probeMCPMetadata(ctx, baseClient, endpoint)).To(Succeed())

		userToken, err := tool.SimulateMCPLogin(ctx, resourceURL, userUsername, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		mcpClient := &http.Client{Transport: bearerRoundTripper{base: baseTransport, token: userToken, origin: endpoint}}
		client := mcp.NewClient(&mcp.Implementation{Name: "it-mcp-deployed-client", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: mcpClient}, nil)
		Expect(err).ToNot(HaveOccurred())
		defer func() { Expect(session.Close()).To(Succeed()) }()
		tools, err := session.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(tools.Tools).To(HaveLen(4))
		_, err = callMCPTool[mcpserver.ListResourcesOutput](ctx, session, "list_resources", mcpserver.ListResourcesInput{
			ResourceType: mcpserver.ResourceTypeComputeInstance,
		})
		Expect(err).ToNot(HaveOccurred())

		// The regular Fulfillment API token has a different audience and must not
		// be accepted by the MCP listener.
		apiToken, err := tool.UserTokenSource().Token(ctx)
		Expect(err).ToNot(HaveOccurred())
		wrongAudienceReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
		Expect(err).ToNot(HaveOccurred())
		wrongAudienceReq.Header.Set("Authorization", "Bearer "+apiToken.Access)
		wrongAudienceResp, err := baseClient.Do(wrongAudienceReq)
		Expect(err).ToNot(HaveOccurred())
		defer wrongAudienceResp.Body.Close()
		Expect(wrongAudienceResp.StatusCode).To(Equal(http.StatusUnauthorized))
	})
})

type bearerRoundTripper struct {
	base   http.RoundTripper
	token  string
	origin string
}

func (t bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme+"://"+request.URL.Host != t.origin {
		return nil, fmt.Errorf("refusing to send MCP bearer token to another origin")
	}
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(request)
}
