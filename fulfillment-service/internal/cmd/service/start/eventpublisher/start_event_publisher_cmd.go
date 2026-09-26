/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package eventpublisher

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/kafka"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	shtdwn "github.com/osac-project/osac/fulfillment-service/internal/shutdown"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
)

// Cmd creates and returns the `start event-publisher` command.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	command := &cobra.Command{
		Use:                   "event-publisher [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := command.Flags()
	network.AddListenerFlags(flags, network.GrpcListenerName, network.DefaultGrpcAddress)
	network.AddListenerFlags(flags, network.MetricsListenerName, network.DefaultMetricsAddress)
	database.AddFlags(flags)
	kafka.AddFlags(flags)
	flags.StringSliceVar(
		&runner.args.caFiles,
		"ca-file",
		[]string{},
		caFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.topicPrefix,
		"kafka-topic-prefix",
		servers.DefaultEventTopicPrefix,
		topicPrefixFlagHelp,
	)
	return command
}

// runnerContext contains the data and logic needed to run the `start event-publisher` command.
type runnerContext struct {
	logger *slog.Logger
	flags  *pflag.FlagSet
	args   struct {
		caFiles     []string
		topicPrefix string
	}
}

// run runs the `start event-publisher` command.
func (c *runnerContext) run(cmd *cobra.Command, argv []string) (err error) {
	ctx, cancel := context.WithCancel(cmd.Context())
	c.logger = logging.LoggerFromContext(ctx)
	c.flags = cmd.Flags()

	metricsRegisterer := prometheus.DefaultRegisterer

	c.logger.InfoContext(ctx, "Creating shutdown sequence")
	shutdown, err := shtdwn.NewSequence().
		SetLogger(c.logger).
		AddSignals(syscall.SIGTERM, syscall.SIGINT).
		AddContext("context", 0, cancel).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create shutdown sequence: %w", err)
	}

	c.logger.InfoContext(ctx, "Creating database connection pool")
	dbTool, err := database.NewTool().
		SetLogger(c.logger).
		SetFlags(c.flags).
		Build()
	if err != nil {
		return err
	}
	c.logger.InfoContext(ctx, "Waiting for database")
	err = dbTool.Wait(ctx)
	if err != nil {
		return err
	}
	dbPool, err := dbTool.Pool(ctx)
	if err != nil {
		return err
	}
	shutdown.AddDatabasePool("database", 0, dbPool)

	c.logger.InfoContext(ctx, "Loading trusted CA certificates")
	caPool, err := trust.NewCertPool().
		SetLogger(c.logger).
		AddSystemFiles(true).
		AddKubernetesFiles(true).
		AddFiles(c.args.caFiles...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to load trusted CA certificates: %w", err)
	}

	c.logger.InfoContext(ctx, "Creating Kafka client")
	kafkaTool, err := kafka.NewTool().
		SetLogger(c.logger).
		SetFlags(c.flags).
		SetCAPool(caPool).
		Build()
	if err != nil {
		return err
	}
	kafkaClient, err := kafkaTool.Client()
	if err != nil {
		return err
	}
	shutdown.AddFunction("kafka", 0, func(context.Context) error {
		return kafkaClient.Close()
	})

	c.logger.InfoContext(ctx, "Creating event publisher")
	eventPublisher, err := servers.NewEventPublisher().
		SetLogger(c.logger).
		SetDatabasePool(dbPool).
		SetKafkaClient(kafkaClient).
		SetKafkaTopicPrefix(c.args.topicPrefix).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create event publisher: %w", err)
	}
	shutdown.AddFunction("publisher", 2, func(context.Context) error {
		return eventPublisher.Close()
	})

	c.logger.InfoContext(ctx, "Creating gRPC listener")
	grpcListener, err := network.NewListener().
		SetLogger(c.logger).
		SetFlags(c.flags, network.GrpcListenerName).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create gRPC listener: %w", err)
	}
	grpcServer := grpc.NewServer()
	shutdown.AddGrpcServer(network.GrpcListenerName, 0, grpcServer)

	c.logger.InfoContext(ctx, "Registering gRPC reflection server")
	reflection.RegisterV1(grpcServer)

	c.logger.InfoContext(ctx, "Registering gRPC health server")
	healthServer := health.NewServer()
	healthv1.RegisterHealthServer(grpcServer, healthServer)

	c.logger.InfoContext(
		ctx,
		"Starting gRPC server",
		slog.String("address", grpcListener.Addr().String()),
	)
	go func() {
		err := grpcServer.Serve(grpcListener)
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			c.logger.ErrorContext(
				ctx,
				"gRPC server failed",
				slog.Any("error", err),
			)
			shutdown.Start(1)
		}
	}()

	c.logger.InfoContext(ctx, "Creating metrics listener")
	metricsListener, err := network.NewListener().
		SetLogger(c.logger).
		SetFlags(c.flags, network.MetricsListenerName).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create metrics listener: %w", err)
	}

	c.logger.InfoContext(
		ctx,
		"Starting metrics server",
		slog.String("address", metricsListener.Addr().String()),
	)
	metricsServer := &http.Server{
		Addr:              metricsListener.Addr().String(),
		Handler:           promhttp.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdown.AddHttpServer(network.MetricsListenerName, 0, metricsServer)
	go func() {
		err := metricsServer.Serve(metricsListener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.logger.ErrorContext(
				ctx,
				"Metrics server failed",
				slog.Any("error", err),
			)
			shutdown.Start(1)
		}
	}()

	c.logger.InfoContext(ctx, "Starting event publisher")
	go func() {
		err := eventPublisher.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			c.logger.ErrorContext(
				ctx,
				"Event publisher failed",
				slog.Any("error", err),
			)
			shutdown.Start(1)
		}
	}()

	c.logger.InfoContext(ctx, "Waiting for shutdown sequence to complete")
	return shutdown.Wait()
}

const shortHelp = `Starts the event publisher`

const longHelp = `
Starts the event publisher.

The publisher listens for {{ bt }}changes{{ bt }} notifications, claims committed rows from the {{ bt }}changes{{ bt }}
table, and produces them to Kafka topics named with the {{ bt }}--kafka-topic-prefix{{ bt }} followed by the tenant. The
default prefix is {{ bt }}osac.events.{{ bt }}.
`

const caFileFlagHelp = `
_FILE_ - Files or directories containing trusted CA certificates in PEM format. Used for TLS connections to Kafka.
`

const topicPrefixFlagHelp = `
_PREFIX_ - Prefix prepended to the tenant identifier to form Kafka topic names.
`
