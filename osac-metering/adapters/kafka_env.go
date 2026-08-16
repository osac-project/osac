/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import "os"

// KafkaConfigFromEnv reads Kafka connection configuration from environment
// variables. TLS is enabled by default; set KAFKA_TLS_ENABLED=false to
// disable it.
//
// Environment variables:
//
//   - KAFKA_TLS_ENABLED       — "false" to disable TLS (default: enabled)
//   - KAFKA_TLS_CA_CERT       — path to CA certificate file (default: system CAs)
//   - KAFKA_SASL_USERNAME     — SASL/SCRAM username (default: disabled)
//   - KAFKA_SASL_PASSWORD_FILE — path to file containing SASL password
func KafkaConfigFromEnv() KafkaConfig {
	return KafkaConfig{
		TLSEnabled:   os.Getenv("KAFKA_TLS_ENABLED") != "false",
		TLSCACert:    os.Getenv("KAFKA_TLS_CA_CERT"),
		SASLUser:     os.Getenv("KAFKA_SASL_USERNAME"),
		SASLPassFile: os.Getenv("KAFKA_SASL_PASSWORD_FILE"),
	}
}
