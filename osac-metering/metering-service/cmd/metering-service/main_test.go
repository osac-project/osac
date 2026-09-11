package main

import (
	"testing"

	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
)

func TestConfigValidateRequiresDeploymentID(t *testing.T) {
	cfg := &config{
		fulfillmentAddr: "fulfillment:8001",
		kafka: kafkapub.ConnectionConfig{
			Brokers:      "kafka:9093",
			SASLUser:     "metering",
			SASLPassFile: "/tmp/password",
		},
		dbURLFile: "/etc/metering/db",
	}

	if err := cfg.validate(); err == nil {
		t.Fatal("expected missing deployment identity to fail validation")
	}

	cfg.deploymentID = "installation-a"
	if err := cfg.validate(); err != nil {
		t.Fatalf("valid deployment identity rejected: %v", err)
	}
}
