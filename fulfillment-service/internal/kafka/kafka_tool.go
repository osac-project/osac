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
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/IBM/sarama"
	"github.com/magiconair/properties"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
	"github.com/spf13/pflag"
)

// Names of the Kafka configuration properties:
const (
	brokersProperty  = "brokers"
	userProperty     = "user"
	passwordProperty = "password"
)

var validProperties = map[string]struct{}{
	brokersProperty:  {},
	userProperty:     {},
	passwordProperty: {},
}

// Tool centralizes Kafka connection configuration and client construction that are needed during the startup of a
// process that uses Kafka.
type Tool interface {
	// Brokers returns the comma separated list of Kafka bootstrap servers.
	Brokers() string

	// User returns the SASL user name. It is empty when insecure mode is enabled and SASL is not configured.
	User() string

	// Password returns the SASL password. It is empty when insecure mode is enabled and SASL is not configured.
	Password() string

	// Client creates a Sarama client using the loaded configuration. The client manages connections
	// to the Kafka brokers and can be used to create producers and consumers with
	// sarama.NewSyncProducerFromClient, sarama.NewConsumerFromClient and
	// sarama.NewConsumerGroupFromClient. The caller must close the client.
	Client() (sarama.Client, error)
}

// ToolBuilder contains the data and logic needed to create a Kafka tool. Don't create instances of this type directly,
// use the NewTool function instead.
type ToolBuilder struct {
	logger         *slog.Logger
	properties     string
	propertiesFile string
	caPool         *trust.CertPool
	insecure       bool
}

type tool struct {
	logger     *slog.Logger
	properties map[string]string
	config     *sarama.Config
}

// NewTool creates a builder that can then be used to configure and create a Kafka tool.
func NewTool() *ToolBuilder {
	return &ToolBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *ToolBuilder) SetLogger(value *slog.Logger) *ToolBuilder {
	b.logger = value
	return b
}

// SetProperties sets the Kafka configuration properties from a comma separated list of key/value pairs. For example:
//
//	brokers=kafka.example.com:9093,user=my_user,password=my_password
//
// When set together with a properties file, these properties override values loaded from the file.
func (b *ToolBuilder) SetProperties(value string) *ToolBuilder {
	b.properties = value
	return b
}

// SetPropertiesFile sets the path to a file or directory containing Kafka configuration properties.
//
// When pointing to a file the properties are read using the Java properties format. That includes
// comments starting with '#' or '!', empty lines, and line continuation with a trailing '\'.
//
// When pointing to a directory the tool scans for files and treats each file name as a property name
// and the file contents as the property value. For example, given a directory '/etc/kafka' with the
// following files:
//
//	/etc/kafka/brokers  - Contains 'kafka.example.com:9093'.
//	/etc/kafka/user     - Contains 'my_user'.
//	/etc/kafka/password - Contains 'my_password'.
//
// The resulting properties will be:
//
//	brokers=kafka.example.com:9093
//	user=my_user
//	password=my_password
//
// When set together with properties set with SetProperties, the properties from the file or directory are loaded first
// and then overridden by the properties set with SetProperties.
func (b *ToolBuilder) SetPropertiesFile(value string) *ToolBuilder {
	b.propertiesFile = value
	return b
}

// SetCAPool sets the certificate pool used for TLS connections to Kafka. This is optional; when nil, TLS is not used.
func (b *ToolBuilder) SetCAPool(value *trust.CertPool) *ToolBuilder {
	b.caPool = value
	return b
}

// SetInsecure sets whether connections without TLS and SASL credentials are allowed. The default is false.
func (b *ToolBuilder) SetInsecure(value bool) *ToolBuilder {
	b.insecure = value
	return b
}

// SetFlags sets the command line flags that should be used to configure the tool. This is optional. Note that no
// files are read at this point; file reading is deferred to the Build method.
func (b *ToolBuilder) SetFlags(flags *pflag.FlagSet) *ToolBuilder {
	if flags == nil {
		return b
	}

	var (
		flag  string
		value string
		err   error
	)
	failure := func() {
		panic(fmt.Sprintf("failed to get flag '%s", flag))
	}

	flag = propertiesFlagName
	value, err = flags.GetString(flag)
	if err != nil {
		failure()
	} else if value != "" {
		b.SetProperties(value)
	}

	flag = propertiesFileFlagName
	value, err = flags.GetString(flag)
	if err != nil {
		failure()
	} else if value != "" {
		b.SetPropertiesFile(value)
	}

	return b
}

// Build uses the data stored in the builder to create a new Kafka tool.
func (b *ToolBuilder) Build() (result Tool, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	properties := map[string]string{}
	if b.propertiesFile != "" {
		properties, err = b.readPropertiesPath(b.propertiesFile)
		if err != nil {
			return
		}
	}
	if b.properties != "" {
		var overlay map[string]string
		overlay, err = parsePropertiesList(b.properties)
		if err != nil {
			return
		}
		for key, value := range overlay {
			properties[key] = value
		}
	}

	err = validateProperties(properties, b.insecure)
	if err != nil {
		return
	}

	var config *sarama.Config
	config, err = b.buildConfig(properties)
	if err != nil {
		return
	}

	result = &tool{
		logger:     b.logger,
		properties: properties,
		config:     config,
	}
	return
}

func (b *ToolBuilder) readPropertiesPath(path string) (properties map[string]string, err error) {
	var info os.FileInfo
	info, err = os.Stat(path)
	if err != nil {
		err = fmt.Errorf("failed to stat Kafka properties path '%s': %w", path, err)
		return
	}
	if info.IsDir() {
		return b.readPropertiesDirectory(path)
	}
	return b.readPropertiesFile(path)
}

func (b *ToolBuilder) readPropertiesFile(path string) (result map[string]string, err error) {
	loader := &properties.Loader{
		Encoding:         properties.UTF8,
		DisableExpansion: true,
	}
	loaded, err := loader.LoadFile(filepath.Clean(path))
	if err != nil {
		err = fmt.Errorf("failed to read Kafka properties file '%s': %w", path, err)
		return
	}
	result = loaded.Map()
	return
}

func (b *ToolBuilder) readPropertiesDirectory(dir string) (properties map[string]string, err error) {
	properties = map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		err = fmt.Errorf("failed to read Kafka properties directory '%s': %w", dir, err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		data, readErr := os.ReadFile(filepath.Clean(path))
		if readErr != nil {
			err = fmt.Errorf("failed to read property '%s' from file '%s': %w", name, path, readErr)
			return
		}
		properties[name] = strings.TrimSpace(string(data))
	}
	return
}

func (b *ToolBuilder) buildConfig(properties map[string]string) (*sarama.Config, error) {
	config := sarama.NewConfig()
	config.Version = sarama.V3_9_0_0
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Idempotent = true
	config.Producer.Return.Successes = true
	config.Net.MaxOpenRequests = 1
	if b.caPool != nil {
		b.configureTLS(config)
	}
	if properties[userProperty] != "" {
		err := b.configureSASL(config, properties[userProperty], properties[passwordProperty])
		if err != nil {
			return nil, err
		}
	}
	return config, nil
}

func (b *ToolBuilder) configureTLS(sc *sarama.Config) {
	sc.Net.TLS.Enable = true
	sc.Net.TLS.Config = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    b.caPool.Pool(),
	}
}

func (b *ToolBuilder) configureSASL(sc *sarama.Config, user, password string) error {
	if password == "" {
		return fmt.Errorf("the '%s' property is mandatory when '%s' is set", passwordProperty, userProperty)
	}
	if !sc.Net.TLS.Enable {
		return errors.New("TLS must be enabled when SCRAM is enabled")
	}
	sc.Net.SASL.Enable = true
	sc.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
	sc.Net.SASL.User = user
	sc.Net.SASL.Password = password
	sc.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
		return &scramClient{}
	}
	return nil
}

func (t *tool) Brokers() string {
	return t.properties[brokersProperty]
}

func (t *tool) User() string {
	return t.properties[userProperty]
}

func (t *tool) Password() string {
	return t.properties[passwordProperty]
}

func (t *tool) brokerAddrs() ([]string, error) {
	brokers := splitAndTrim(t.Brokers(), ",")
	if len(brokers) == 0 {
		return nil, fmt.Errorf("the '%s' property is mandatory", brokersProperty)
	}
	return brokers, nil
}

// Client creates a Sarama client using the loaded configuration. The client manages connections to the Kafka brokers
// and can be used to create producers and consumers. The caller must close the client.
func (t *tool) Client() (sarama.Client, error) {
	brokers, err := t.brokerAddrs()
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(brokers, t.config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka client: %w", err)
	}
	return client, nil
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := parts[:0]
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parsePropertiesList(value string) (properties map[string]string, err error) {
	properties = map[string]string{}
	for _, pair := range strings.Split(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, val, ok := splitProperty(pair, '=')
		if !ok {
			err = fmt.Errorf("failed to parse Kafka property '%s', expected key=value", pair)
			return
		}
		properties[key] = val
	}
	return
}

func splitProperty(pair string, sep byte) (key, value string, ok bool) {
	index := strings.IndexByte(pair, sep)
	if index < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(pair[:index])
	value = strings.TrimSpace(pair[index+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func validateProperties(properties map[string]string, insecure bool) error {
	for key := range properties {
		if _, ok := validProperties[key]; !ok {
			return fmt.Errorf(
				"unknown Kafka property '%s', valid properties are '%s', '%s' and '%s'",
				key, brokersProperty, userProperty, passwordProperty,
			)
		}
	}
	if strings.TrimSpace(properties[brokersProperty]) == "" {
		return fmt.Errorf("the '%s' property is mandatory", brokersProperty)
	}
	user := strings.TrimSpace(properties[userProperty])
	password := strings.TrimSpace(properties[passwordProperty])
	if !insecure && user == "" && password == "" {
		return fmt.Errorf("the '%s' and '%s' properties are mandatory", userProperty, passwordProperty)
	}
	if user != "" && password == "" {
		return fmt.Errorf("the '%s' property is mandatory when '%s' is set", passwordProperty, userProperty)
	}
	if password != "" && user == "" {
		return fmt.Errorf("the '%s' property is mandatory when '%s' is set", userProperty, passwordProperty)
	}
	return nil
}
