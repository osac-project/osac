/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"log"
	"net"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/testing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

const (
	serverPort            = "8080"
	defaultCIScenarioFile = "internal/testing/testdata/compute-instance-scenario.yaml"
)

func main() {
	ciScenarioFile := flag.String("ci-scenario", defaultCIScenarioFile, "Path to compute instance scenario YAML file")
	flag.Parse()

	// Load compute instance scenario
	ciScenario, err := testing.LoadComputeInstanceScenarioFromFile(*ciScenarioFile)
	if err != nil {
		log.Fatalf("Failed to load compute instance scenario from %s: %v", *ciScenarioFile, err)
	}
	log.Printf("Loaded compute instance scenario: %s - %s", ciScenario.Name, ciScenario.Description)

	listener, err := net.Listen("tcp", "127.0.0.1:"+serverPort)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()

	// Register compute instance servers
	publicv1.RegisterComputeInstancesServer(grpcServer, testing.NewMockComputeInstancesServer(ciScenario))
	publicv1.RegisterComputeInstanceTemplatesServer(grpcServer, testing.NewMockComputeInstanceTemplatesServer(ciScenario))

	// Health and reflection
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)

	fmt.Println("========================================")
	fmt.Println("Mock Fulfillment Service Started")
	fmt.Println("========================================")
	fmt.Printf("Listening on: %s\n", listener.Addr().String())
	fmt.Println("")
	fmt.Println("To test with the CLI:")
	fmt.Printf("  fulfillment-cli login http://127.0.0.1:%s\n", serverPort)
	fmt.Println("  fulfillment-cli get computeinstancetemplates")
	fmt.Println("  fulfillment-cli create computeinstance -t small-instance -n my-instance")
	fmt.Println("  fulfillment-cli get computeinstances")
	fmt.Println("========================================")

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
