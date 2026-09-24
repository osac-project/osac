/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package sim

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	experimentalcredentials "google.golang.org/grpc/experimental/credentials"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var (
	conn              *grpc.ClientConn
	fulfillmentClient baremetalworker.FulfillmentClient
	catalogClient     privatev1.BareMetalInstanceCatalogItemsClient
)

func TestSim(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sim Integration Suite")
}

var _ = BeforeSuite(func() {
	envFile := findEnvFile()
	env := loadEnvFile(envFile)

	address := env["SIM_GRPC_ADDRESS"]
	Expect(address).NotTo(BeEmpty(), "SIM_GRPC_ADDRESS not set in sim.env")

	token := env["SIM_TOKEN"]
	Expect(token).NotTo(BeEmpty(), "SIM_TOKEN not set in sim.env")

	caFile := env["SIM_CA_FILE"]
	serverName := env["SIM_SERVER_NAME"]

	var dialOpts []grpc.DialOption

	if caFile != "" {
		caCert, err := os.ReadFile(caFile)
		Expect(err).NotTo(HaveOccurred(), "failed to read CA file")

		caPool := x509.NewCertPool()
		Expect(caPool.AppendCertsFromPEM(caCert)).To(BeTrue(), "failed to parse CA certificate")

		tlsConfig := &tls.Config{
			RootCAs:    caPool,
			ServerName: serverName,
		}
		// Use experimental credentials with ALPN disabled (same as the operator — see cmd/main.go).
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(
			experimentalcredentials.NewTLSWithALPNDisabled(tlsConfig),
		))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(
			credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}), //nolint:gosec
		))
	}

	dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(&staticTokenSource{token: token}))

	var err error
	conn, err = grpc.NewClient(address, dialOpts...)
	Expect(err).NotTo(HaveOccurred(), "failed to create gRPC connection")

	fulfillmentClient = baremetalworker.NewFulfillmentClientFromConn(conn)
	catalogClient = privatev1.NewBareMetalInstanceCatalogItemsClient(conn)

	ensureCatalogItem(catalogClient)
})

var _ = AfterSuite(func() {
	if conn != nil {
		_ = conn.Close()
	}
})

func findEnvFile() string {
	candidates := []string{
		"hack/sim.env",
		"../../../hack/sim.env",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	Fail("sim.env not found — run 'make sim-up' first")
	return ""
}

func loadEnvFile(path string) map[string]string {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to read %s", path))

	env := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env
}

type staticTokenSource struct {
	token string
}

func (s *staticTokenSource) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + s.token,
	}, nil
}

func (s *staticTokenSource) RequireTransportSecurity() bool {
	return false
}

func ensureCatalogItem(client privatev1.BareMetalInstanceCatalogItemsClient) {
	ctx := context.Background()
	_, err := client.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
		Object: privatev1.BareMetalInstanceCatalogItem_builder{
			Metadata: privatev1.Metadata_builder{
				Name: "sim-test-catalog-item",
			}.Build(),
			Published: true,
		}.Build(),
	}.Build())
	if err == nil || status.Code(err) == codes.AlreadyExists {
		return
	}
	Fail(fmt.Sprintf("failed to create test catalog item: %v", err))
}
