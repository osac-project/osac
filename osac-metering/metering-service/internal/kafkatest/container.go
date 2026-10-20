/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package kafkatest starts a Kafka broker in a container for integration tests. It borrows the
// pattern of the fulfillment-service Kafka container: the broker starts only after the published
// host port is known, so the advertised listener matches it.
package kafkatest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
)

// Container is a Kafka broker running in a container. It is for tests only.
type Container struct {
	logger logr.Logger
	tool   string
	id     string
	host   string
	port   string
	runCmd *exec.Cmd
}

// Start starts the broker and waits until it accepts connections.
func Start(ctx context.Context, logger logr.Logger) (*Container, error) {
	tool, err := selectTool()
	if err != nil {
		return nil, err
	}
	c := &Container{logger: logger, tool: tool}
	c.id = fmt.Sprintf("osac-metering-kafka-%d", time.Now().UnixNano())
	// The run command must outlive the startup context: callers cancel the context they
	// start the broker with as soon as Start returns, and the broker runs until Stop
	// kills it. A command bound to the startup context would die with it.
	c.runCmd = exec.Command(c.tool, c.runArgs()...) // #nosec G204
	c.runCmd.Stdout = os.Stdout
	c.runCmd.Stderr = os.Stderr
	if err := c.runCmd.Start(); err != nil {
		return nil, fmt.Errorf("starting kafka container: %w", err)
	}

	var stopErr error
	defer func() {
		if stopErr != nil {
			_ = c.Stop()
		}
	}()

	if stopErr = c.waitForPort(ctx); stopErr != nil {
		return nil, stopErr
	}
	if stopErr = c.writeAdvertised(ctx); stopErr != nil {
		return nil, stopErr
	}
	if stopErr = c.waitForConnection(ctx); stopErr != nil {
		return nil, stopErr
	}
	return c, nil
}

// Brokers returns the bootstrap address of the broker.
func (c *Container) Brokers() string {
	return net.JoinHostPort(c.host, c.port)
}

// Stop kills the broker container and waits for it to exit.
func (c *Container) Stop() error {
	killCmd := exec.Command(c.tool, "kill", c.id)
	_ = killCmd.Run()
	if c.runCmd != nil {
		_ = c.runCmd.Wait()
	}
	return nil
}

// Config returns a plain sarama configuration for this broker.
func (c *Container) Config() *sarama.Config {
	config := sarama.NewConfig()
	config.Version = sarama.V3_9_0_0
	config.Producer.Return.Successes = true
	return config
}

// GetOffsetHigh returns the high watermark of a topic's only partition.
func (c *Container) GetOffsetHigh(topic string) (int64, error) {
	client, err := sarama.NewClient([]string{c.Brokers()}, c.Config())
	if err != nil {
		return 0, fmt.Errorf("connecting kafka client: %w", err)
	}
	defer func() { _ = client.Close() }()
	return client.GetOffset(topic, 0, sarama.OffsetNewest)
}

// CreateTopic creates a topic with one partition.
func (c *Container) CreateTopic(topic string) error {
	admin, err := sarama.NewClusterAdmin([]string{c.Brokers()}, c.Config())
	if err != nil {
		return fmt.Errorf("connecting kafka admin: %w", err)
	}
	defer func() { _ = admin.Close() }()
	err = admin.CreateTopic(topic, &sarama.TopicDetail{
		NumPartitions:     1,
		ReplicationFactor: 1,
	}, false)
	if err != nil && !errors.Is(err, sarama.ErrTopicAlreadyExists) {
		return fmt.Errorf("creating topic %s: %w", topic, err)
	}
	return nil
}

func (c *Container) runArgs() []string {
	listeners := fmt.Sprintf(
		"PLAINTEXT://0.0.0.0:%s,CONTROLLER://0.0.0.0:%s", clientPort, controllerPort,
	)
	return []string{
		"run",
		"--name", c.id,
		"--rm",
		"--publish", clientPort,
		"--env", "KAFKA_NODE_ID=1",
		"--env", "KAFKA_PROCESS_ROLES=broker,controller",
		"--env", "KAFKA_LISTENERS=" + listeners,
		"--env", "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
		"--env", "KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER",
		"--env", "KAFKA_CONTROLLER_QUORUM_VOTERS=1@127.0.0.1:" + controllerPort,
		"--env", "KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1",
		"--env", "KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1",
		"--env", "KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1",
		"--env", "KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0",
		"--env", "KAFKA_NUM_PARTITIONS=1",
		"--env", "KAFKA_AUTO_CREATE_TOPICS_ENABLE=true",
		"--env", "KAFKA_HEAP_OPTS=-Xmx256m -Xms256m",
		"--entrypoint", "/bin/sh",
		containerImage,
		"-c", waitScript,
	}
}

func (c *Container) waitForPort(ctx context.Context) error {
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return errors.New("timed out waiting for kafka container port")
		case <-ticker.C:
			var portOut strings.Builder
			portCmd := exec.CommandContext(ctx, c.tool, "port", c.id, clientPort+"/tcp")
			portCmd.Stdout = &portOut
			if err := portCmd.Run(); err != nil {
				continue
			}
			host, port, err := net.SplitHostPort(strings.TrimSpace(portOut.String()))
			if err != nil {
				continue
			}
			if host == "0.0.0.0" {
				host = "127.0.0.1"
			}
			c.host, c.port = host, port
			return nil
		}
	}
}

func (c *Container) writeAdvertised(ctx context.Context) error {
	advertised := fmt.Sprintf("PLAINTEXT://%s:%s", c.host, c.port)
	cmd := exec.CommandContext( // #nosec G204
		ctx, c.tool, "exec", c.id, "/bin/sh", "-c",
		fmt.Sprintf("printf '%%s' '%s' > %s", advertised, advertisedFile),
	)
	return cmd.Run()
}

func (c *Container) waitForConnection(ctx context.Context) error {
	deadline := time.After(time.Minute)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	config := c.Config()
	config.Net.DialTimeout = time.Second
	config.Net.ReadTimeout = time.Second
	config.Net.WriteTimeout = time.Second
	config.Metadata.Retry.Max = 0
	config.Metadata.Timeout = time.Second
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return errors.New("timed out waiting for kafka connection")
		case <-ticker.C:
			client, err := sarama.NewClient([]string{c.Brokers()}, config)
			if err != nil {
				continue
			}
			_ = client.Close()
			return nil
		}
	}
}

func selectTool() (string, error) {
	for _, tool := range []string{"podman", "docker"} {
		if path, err := exec.LookPath(tool); err == nil {
			return path, nil
		}
	}
	return "", errors.New("neither podman nor docker found in PATH")
}

const (
	containerImage = "docker.io/apache/kafka@sha256:77e3df9054047a88b520d0cc46e16696d3b22022e1d580aeccd2632df6532837"
	clientPort     = "9092"
	controllerPort = "9093"
	advertisedFile = "/tmp/osac-advertised-listeners"
	waitScript     = "while [ ! -f " + advertisedFile + " ]; do sleep 0.1; done; " +
		"export KAFKA_ADVERTISED_LISTENERS=$(cat " + advertisedFile + "); " +
		"exec /etc/kafka/docker/run"
)
