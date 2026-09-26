/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
	"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package kafka

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/IBM/sarama"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
	"github.com/spf13/pflag"
)

var _ = Describe("Kafka tool", func() {
	var (
		tmpDir string
		caPool *trust.CertPool
	)

	writeFile := func(name, content string) string {
		path := filepath.Join(tmpDir, name)
		ExpectWithOffset(1, os.WriteFile(path, []byte(content), 0600)).To(Succeed())
		return path
	}

	writeCA := func() string {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		template := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "test-ca"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(time.Hour),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign,
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		return writeFile("ca.pem", string(pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: der,
		})))
	}

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, tmpDir)
		caPool, err = trust.NewCertPool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Configuration via builder methods", func() {
		It("Accepts properties set directly", func() {
			tool, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers=kafka.example.com:9093,user=my_user,password=my_password").
				SetCAPool(caPool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
			Expect(tool.User()).To(Equal("my_user"))
			Expect(tool.Password()).To(Equal("my_password"))
		})

		It("Trims whitespace around keys and values", func() {
			tool, err := NewTool().
				SetLogger(logger).
				SetProperties(" brokers = kafka.example.com:9093 , user = my_user , password = secret ").
				SetCAPool(caPool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
			Expect(tool.User()).To(Equal("my_user"))
			Expect(tool.Password()).To(Equal("secret"))
		})

		It("Reads properties from a file", func() {
			propertiesFile := writeFile("kafka.properties", ""+
				"brokers=kafka.example.com:9093\n"+
				"user=my_user\n"+
				"password=my_password\n",
			)

			tool, err := NewTool().
				SetLogger(logger).
				SetPropertiesFile(propertiesFile).
				SetCAPool(caPool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
			Expect(tool.User()).To(Equal("my_user"))
			Expect(tool.Password()).To(Equal("my_password"))
		})

		It("Ignores comments and empty lines in a properties file", func() {
			propertiesFile := writeFile("kafka.properties", ""+
				"# bootstrap servers\n"+
				"\n"+
				"brokers=kafka.example.com:9093\n"+
				"! ignored\n",
			)

			tool, err := NewTool().
				SetLogger(logger).
				SetPropertiesFile(propertiesFile).
				SetInsecure(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
			Expect(tool.User()).To(BeEmpty())
			Expect(tool.Password()).To(BeEmpty())
		})

		It("Joins continued lines in a properties file", func() {
			propertiesFile := writeFile("kafka.properties", ""+
				"brokers=kafka.example.com:\\\n"+
				"9093\n",
			)

			tool, err := NewTool().
				SetLogger(logger).
				SetPropertiesFile(propertiesFile).
				SetInsecure(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
		})

		It("Overrides file properties with the properties list", func() {
			propertiesFile := writeFile("kafka.properties", ""+
				"brokers=from-file:9093\n"+
				"user=file_user\n"+
				"password=file_password\n",
			)

			tool, err := NewTool().
				SetLogger(logger).
				SetPropertiesFile(propertiesFile).
				SetProperties("user=flag_user,password=flag_password").
				SetCAPool(caPool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("from-file:9093"))
			Expect(tool.User()).To(Equal("flag_user"))
			Expect(tool.Password()).To(Equal("flag_password"))
		})

		It("Returns an error when the properties file does not exist", func() {
			_, err := NewTool().
				SetLogger(logger).
				SetPropertiesFile("/nonexistent/kafka.properties").
				Build()
			Expect(err).To(MatchError(ContainSubstring("/nonexistent/kafka.properties")))
		})

		It("Returns an error when no brokers are provided", func() {
			_, err := NewTool().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("the 'brokers' property is mandatory"))
		})

		It("Returns an error by default when credentials are not provided", func() {
			_, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers=kafka.example.com:9093").
				Build()
			Expect(err).To(MatchError("the 'user' and 'password' properties are mandatory"))
		})

		It("Returns an error when the user is set without a password", func() {
			_, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers=kafka.example.com:9093,user=my_user").
				Build()
			Expect(err).To(MatchError("the 'password' property is mandatory when 'user' is set"))
		})

		It("Returns an error when the password is set without a user", func() {
			_, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers=kafka.example.com:9093,password=my_password").
				Build()
			Expect(err).To(MatchError("the 'user' property is mandatory when 'password' is set"))
		})

		It("Returns an error for unknown properties", func() {
			_, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers=kafka.example.com:9093,foo=bar").
				Build()
			Expect(err).To(MatchError(
				"unknown Kafka property 'foo', valid properties are 'brokers', 'user' and 'password'",
			))
		})

		It("Returns an error for a malformed properties list", func() {
			_, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers").
				Build()
			Expect(err).To(MatchError("failed to parse Kafka property 'brokers', expected key=value"))
		})

		It("Returns an error when the logger is missing", func() {
			_, err := NewTool().
				SetProperties("brokers=kafka.example.com:9093").
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
		})
	})

	Describe("Configuration from a directory", func() {
		It("Reads properties from files in a directory", func() {
			writeFile("brokers", "kafka.example.com:9093")
			writeFile("user", "my_user")
			writeFile("password", "my_password")

			tool, err := NewTool().
				SetLogger(logger).
				SetPropertiesFile(tmpDir).
				SetCAPool(caPool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
			Expect(tool.User()).To(Equal("my_user"))
			Expect(tool.Password()).To(Equal("my_password"))
		})

		It("Trims whitespace from directory file contents", func() {
			writeFile("brokers", "  kafka.example.com:9093\n")

			tool, err := NewTool().
				SetLogger(logger).
				SetPropertiesFile(tmpDir).
				SetInsecure(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
		})

		It("Ignores hidden files and subdirectories", func() {
			writeFile("brokers", "kafka.example.com:9093")
			writeFile(".ignored", "should-not-be-used")
			Expect(os.Mkdir(filepath.Join(tmpDir, "subdir"), 0700)).To(Succeed())

			tool, err := NewTool().
				SetLogger(logger).
				SetPropertiesFile(tmpDir).
				SetInsecure(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
		})

		It("Returns an error for unknown file names in a directory", func() {
			writeFile("brokers", "kafka.example.com:9093")
			writeFile("foo", "bar")

			_, err := NewTool().
				SetLogger(logger).
				SetPropertiesFile(tmpDir).
				Build()
			Expect(err).To(MatchError(
				"unknown Kafka property 'foo', valid properties are 'brokers', 'user' and 'password'",
			))
		})
	})

	Describe("Configuration via command line flags", func() {
		parseFlags := func(args ...string) *pflag.FlagSet {
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddFlags(flags)
			ExpectWithOffset(1, flags.Parse(args)).To(Succeed())
			return flags
		}

		It("Accepts properties via the --kafka-properties flag", func() {
			flags := parseFlags(
				"--kafka-properties",
				"brokers=kafka.example.com:9093,user=my_user,password=my_password",
			)

			tool, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				SetCAPool(caPool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
			Expect(tool.User()).To(Equal("my_user"))
			Expect(tool.Password()).To(Equal("my_password"))
		})

		It("Reads properties from a file via the --kafka-properties-file flag", func() {
			propertiesFile := writeFile("kafka.properties", "brokers=kafka.example.com:9093\n")

			flags := parseFlags("--kafka-properties-file", propertiesFile)

			tool, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				SetInsecure(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
		})

		It("Reads properties from a directory via the --kafka-properties-file flag", func() {
			writeFile("brokers", "kafka.example.com:9093")
			writeFile("user", "my_user")
			writeFile("password", "my_password")

			flags := parseFlags("--kafka-properties-file", tmpDir)

			tool, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				SetCAPool(caPool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("kafka.example.com:9093"))
			Expect(tool.User()).To(Equal("my_user"))
			Expect(tool.Password()).To(Equal("my_password"))
		})

		It("Overrides file properties with --kafka-properties", func() {
			writeFile("brokers", "from-file:9093")
			writeFile("user", "file_user")
			writeFile("password", "file_password")

			flags := parseFlags(
				"--kafka-properties-file", tmpDir,
				"--kafka-properties", "user=flag_user,password=flag_password",
			)

			tool, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				SetCAPool(caPool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Brokers()).To(Equal("from-file:9093"))
			Expect(tool.User()).To(Equal("flag_user"))
			Expect(tool.Password()).To(Equal("flag_password"))
		})

		It("Returns an error when no brokers are provided", func() {
			flags := parseFlags()

			_, err := NewTool().
				SetLogger(logger).
				SetFlags(flags).
				Build()
			Expect(err).To(MatchError("the 'brokers' property is mandatory"))
		})
	})

	Describe("Sarama configuration", func() {
		It("Returns an error when SCRAM is enabled without TLS", func() {
			_, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers=kafka.example.com:9093,user=my_user,password=my_password").
				Build()
			Expect(err).To(MatchError("TLS must be enabled when SCRAM is enabled"))
		})

		It("Enables SASL when the user and password are set", func() {
			built, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers=kafka.example.com:9093,user=my_user,password=my_password").
				SetCAPool(caPool).
				Build()
			Expect(err).ToNot(HaveOccurred())

			config := built.(*tool).config
			Expect(config.Net.SASL.Enable).To(BeTrue())
			Expect(config.Net.SASL.Mechanism).To(Equal(sarama.SASLMechanism(sarama.SASLTypeSCRAMSHA512)))
			Expect(config.Net.SASL.User).To(Equal("my_user"))
			Expect(config.Net.SASL.Password).To(Equal("my_password"))
			Expect(config.Net.TLS.Enable).To(BeTrue())
		})

		It("Allows TLS and SASL to remain disabled when insecure", func() {
			built, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers=kafka.example.com:9093").
				SetInsecure(true).
				Build()
			Expect(err).ToNot(HaveOccurred())

			config := built.(*tool).config
			Expect(config.Net.TLS.Enable).To(BeFalse())
			Expect(config.Net.SASL.Enable).To(BeFalse())
		})

		It("Enables TLS when a CA pool is set", func() {
			caFile := writeCA()
			caPool, err := trust.NewCertPool().
				SetLogger(logger).
				AddSystemFiles(true).
				AddFile(caFile).
				Build()
			Expect(err).ToNot(HaveOccurred())

			built, err := NewTool().
				SetLogger(logger).
				SetProperties("brokers=kafka.example.com:9093").
				SetCAPool(caPool).
				SetInsecure(true).
				Build()
			Expect(err).ToNot(HaveOccurred())

			config := built.(*tool).config
			Expect(config.Net.TLS.Enable).To(BeTrue())
			Expect(config.Net.TLS.Config).ToNot(BeNil())
			Expect(config.Net.TLS.Config.RootCAs).To(BeIdenticalTo(caPool.Pool()))
		})
	})
})
