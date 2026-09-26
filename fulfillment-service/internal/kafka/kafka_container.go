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
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/cenkalti/backoff/v4"

	"github.com/osac-project/osac/fulfillment-service/internal/logging"
)

// ContainerBuilder contains the data and logic needed to create a Kafka broker container. Don't create instances of
// this type directly, use the NewContainer function instead.
type ContainerBuilder struct {
	logger *slog.Logger
	tool   string
}

// Container knows how to start a Kafka broker inside a container. It is intended for use in unit tests where an
// ephemeral Kafka instance is needed. Log and metadata directories stay inside the container filesystem and are
// discarded when the container is removed. It should not be used to create brokers for production environments.
type Container struct {
	logger    *slog.Logger
	tool      string
	id        string
	host      string
	port      string
	outWriter *logging.Writer
	errWriter *logging.Writer
	runCmd    *exec.Cmd
}

// NewContainer creates a builder that can then be used to configure and create a Kafka broker container. The resulting
// container starts a Kafka broker using podman or docker and is intended exclusively for unit tests. Do not use it to
// create brokers for production environments.
func NewContainer() *ContainerBuilder {
	return &ContainerBuilder{}
}

// SetLogger sets the logger that the container will use to write messages to the log. This is mandatory.
func (b *ContainerBuilder) SetLogger(value *slog.Logger) *ContainerBuilder {
	b.logger = value
	return b
}

// SetTool sets the tool used to start the Kafka broker container. This is optional, by default it will try to use
// 'podman', and if that is not available it will try to use 'docker'.
func (b *ContainerBuilder) SetTool(value string) *ContainerBuilder {
	b.tool = value
	return b
}

// Build uses the information stored in the builder to create the Kafka broker container object. The container is not
// started at this point; call the Start method to start it.
func (b *ContainerBuilder) Build() (result *Container, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Select the container tool to use:
	tool := b.tool
	if tool == "" {
		tool, err = b.selectTool()
		if err != nil {
			err = fmt.Errorf("failed to select container tool: %w", err)
			return
		}
	}
	b.logger.Info(
		"Selected container tool",
		slog.String("tool", tool),
	)

	// Prepare writers to write the output of the commands to the log:
	outLogger := b.logger.With(
		slog.String("stream", "stdout"),
	)
	outWriter, err := logging.NewWriter().
		SetLogger(outLogger).
		SetLevel(slog.LevelDebug).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create writer for command output: %w", err)
		return
	}
	errLogger := b.logger.With(
		slog.String("stream", "stderr"),
	)
	errWriter, err := logging.NewWriter().
		SetLogger(errLogger).
		SetLevel(slog.LevelDebug).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create writer for command errors: %w", err)
		return
	}

	// Create the container object:
	result = &Container{
		logger:    b.logger,
		tool:      tool,
		outWriter: outWriter,
		errWriter: errWriter,
	}
	return
}

// selectTool selects the tool to use to start the Kafka broker container.
func (b *ContainerBuilder) selectTool() (result string, err error) {
	for _, tool := range containerTools {
		var toolPath string
		toolPath, err = exec.LookPath(tool)
		if err != nil {
			b.logger.Info(
				"Container tool not available",
				slog.String("tool", tool),
				slog.Any("error", err),
			)
			continue
		}
		result = toolPath
		return
	}
	err = errors.New("can't find any available container tool")
	return
}

// Start starts the Kafka broker inside a container and waits until it is ready to accept connections. Cleaning when
// something fails, or when the container is no longer needed, is the responsibility of the caller.
func (c *Container) Start(ctx context.Context) (err error) {
	// Start the container in the foreground so that its stdout and stderr are piped directly to the log writers,
	// without going through the container runtime's log file. This avoids the latency introduced by conmon writing
	// to a file and `podman logs` tailing it. The process inside waits until advertised listeners can be set from
	// the published host port; Kafka clients follow those advertised addresses, unlike PostgreSQL.
	c.id = fmt.Sprintf("osac-kafka-%08x", rand.Uint32())
	c.runCmd = exec.Command(c.tool, c.runArgs()...) // #nosec G204
	c.runCmd.Stdout = c.outWriter
	c.runCmd.Stderr = c.errWriter
	err = c.runCmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start Kafka container: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		killCmd := exec.Command(c.tool, "kill", c.id) // #nosec G204
		killCmd.Stdout = c.outWriter
		killCmd.Stderr = c.errWriter
		_ = killCmd.Run()
		if c.runCmd != nil {
			_ = c.runCmd.Wait()
		}
	}()

	// Find out the port number assigned to the Kafka client listener. Because the container runs in the foreground,
	// the port mapping may not be available immediately, so we retry with exponential backoff.
	findPortBo := backoff.NewExponentialBackOff()
	findPortBo.InitialInterval = 100 * time.Millisecond
	findPortBo.MaxInterval = 1 * time.Second
	findPortBo.MaxElapsedTime = 30 * time.Second
	err = backoff.Retry(func() error {
		return c.findPort(ctx)
	}, backoff.WithContext(findPortBo, ctx))
	if err != nil {
		return err
	}

	// Start the Kafka broker now that advertised listeners can include the published host port:
	err = c.writeAdvertised(ctx)
	if err != nil {
		return err
	}

	// Wait till the Kafka broker is responding, using exponential backoff:
	connectBo := backoff.NewExponentialBackOff()
	connectBo.InitialInterval = 200 * time.Millisecond
	connectBo.MaxInterval = 2 * time.Second
	connectBo.MaxElapsedTime = time.Minute
	err = backoff.Retry(func() error {
		return c.ping(ctx)
	}, backoff.WithContext(connectBo, ctx))
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) runArgs() []string {
	listeners := fmt.Sprintf(
		"PLAINTEXT://0.0.0.0:%s,CONTROLLER://0.0.0.0:%s",
		containerClientPort, containerControllerPort,
	)
	quorumVoters := fmt.Sprintf("1@127.0.0.1:%s", containerControllerPort)
	return []string{
		"run",
		"--name", c.id,
		"--rm",
		"--publish", containerClientPort,
		"--env", "KAFKA_NODE_ID=1",
		"--env", "KAFKA_PROCESS_ROLES=broker,controller",
		"--env", "KAFKA_LISTENERS=" + listeners,
		"--env", "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
		"--env", "KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER",
		"--env", "KAFKA_CONTROLLER_QUORUM_VOTERS=" + quorumVoters,
		"--env", "KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1",
		"--env", "KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1",
		"--env", "KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1",
		"--env", "KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0",
		"--env", "KAFKA_NUM_PARTITIONS=1",
		"--env", "KAFKA_AUTO_CREATE_TOPICS_ENABLE=true",
		"--env", "KAFKA_HEAP_OPTS=-Xmx256m -Xms256m",
		"--entrypoint", "/bin/sh",
		containerImage,
		"-c", containerWaitScript,
	}
}

// findPort queries the container tool for the host port mapped to the Kafka client listener and stores it in the
// container's host and port fields. It returns an error if the port mapping is not yet available.
func (c *Container) findPort(ctx context.Context) error {
	// Run the 'podman port' command to find out the host port mapped to the Kafka client listener:
	portOut := &bytes.Buffer{}
	portCmd := exec.CommandContext( // #nosec G204
		ctx,
		c.tool,
		"port",
		c.id,
		containerClientPort+"/tcp",
	)
	portCmd.Stdout = portOut
	portCmd.Stderr = c.errWriter
	err := portCmd.Run()
	if err != nil {
		return fmt.Errorf("failed to query container port: %w", err)
	}
	portLines := strings.Split(portOut.String(), "\n")
	if len(portLines) < 1 || strings.TrimSpace(portLines[0]) == "" {
		return fmt.Errorf("container port output is empty")
	}
	hostPort := strings.TrimSpace(portLines[0])
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return fmt.Errorf("failed to parse host:port '%s': %w", hostPort, err)
	}

	// If the Kafka broker is listening on all network interfaces we need to choose a specific one to use, and the
	// loopback interface is a reasonable choice:
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}

	// Store the host and port:
	c.host = host
	c.port = port

	return nil
}

// writeAdvertised writes the advertised listener address into the container so the waiting entrypoint can start Kafka.
func (c *Container) writeAdvertised(ctx context.Context) error {
	advertised := fmt.Sprintf("PLAINTEXT://%s:%s", c.host, c.port)
	cmd := exec.CommandContext( // #nosec G204
		ctx,
		c.tool,
		"exec", c.id,
		"/bin/sh", "-c",
		fmt.Sprintf("printf '%%s' '%s' > %s", advertised, containerAdvertisedFile),
	)
	cmd.Stdout = c.outWriter
	cmd.Stderr = c.errWriter
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to set Kafka advertised listeners: %w", err)
	}
	return nil
}

func (c *Container) ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	config := sarama.NewConfig()
	config.Version = sarama.V4_3_1_0
	config.Net.DialTimeout = time.Second
	config.Net.ReadTimeout = time.Second
	config.Net.WriteTimeout = time.Second
	config.Metadata.Retry.Max = 0
	config.Metadata.Timeout = time.Second
	client, err := sarama.NewClient([]string{c.Brokers()}, config)
	if err != nil {
		return err
	}
	defer func() {
		_ = client.Close()
	}()
	if len(client.Brokers()) == 0 {
		return errors.New("kafka broker list is empty")
	}
	return nil
}

// Stop stops the Kafka broker and waits for the container process to exit.
func (c *Container) Stop(ctx context.Context) error {
	// Stop the Kafka broker and wait for the foreground run command to exit so that all remaining output is
	// flushed through the writers.
	killCmd := exec.CommandContext( // #nosec G204
		ctx,
		c.tool,
		"kill",
		c.id,
	)
	killCmd.Stdout = c.outWriter
	killCmd.Stderr = c.errWriter
	err := killCmd.Run()
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to kill Kafka container",
			slog.Any("error", err),
		)
	}
	if c.runCmd != nil {
		_ = c.runCmd.Wait()
	}
	return nil
}

// Brokers returns the bootstrap server address advertised to Kafka clients, in host:port form.
func (c *Container) Brokers() string {
	return net.JoinHostPort(c.host, c.port)
}

// Client creates a Sarama client connected to the broker. The caller must close the client.
func (c *Container) Client() (sarama.Client, error) {
	if c.host == "" || c.port == "" {
		return nil, errors.New("container is not started")
	}
	tool, err := NewTool().
		SetLogger(c.logger).
		SetProperties(fmt.Sprintf("%s=%s", brokersProperty, c.Brokers())).
		SetInsecure(true).
		Build()
	if err != nil {
		return nil, err
	}
	return tool.Client()
}

// containerTools is the list of container tools that we will try to use to start the Kafka broker container, in order
// of preference.
var containerTools = []string{
	"podman",
	"docker",
}

// containerImage is the Apache Kafka 4.3.1 KRaft combined-mode image used by unit tests.
const containerImage = "docker.io/apache/kafka@sha256:77e3df9054047a88b520d0cc46e16696d3b22022e1d580aeccd2632df6532837"

// containerClientPort is the client listener port inside the container that is published to the host.
const containerClientPort = "9092"

// containerControllerPort is the KRaft controller port inside the container. It is not published to the host.
const containerControllerPort = "9093"

// containerAdvertisedFile is the in-container path used to pass the advertised listener address to the entrypoint
// after the published host port is known.
const containerAdvertisedFile = "/tmp/osac-advertised-listeners"

// containerWaitScript waits until advertised listeners are written, then starts the Kafka broker. Listeners stay bound
// to the container ports; only the advertised client address uses the published host port.
const containerWaitScript = "" +
	"while [ ! -f " + containerAdvertisedFile + " ]; do sleep 0.1; done; " +
	"export KAFKA_ADVERTISED_LISTENERS=$(cat " + containerAdvertisedFile + "); " +
	"exec /etc/kafka/docker/run"
