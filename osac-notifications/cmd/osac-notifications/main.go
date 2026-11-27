/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/oauth"

	"github.com/osac-project/osac/osac-notifications/internal/consumer"
	"github.com/osac-project/osac/osac-notifications/internal/dlq"
	"github.com/osac-project/osac/osac-notifications/internal/drain"
	"github.com/osac-project/osac/osac-notifications/internal/fanout"
	"github.com/osac-project/osac/osac-notifications/internal/grant"
	"github.com/osac-project/osac/osac-notifications/internal/ingest"
	"github.com/osac-project/osac/osac-notifications/internal/intent"
	"github.com/osac-project/osac/osac-notifications/internal/match"
	"github.com/osac-project/osac/osac-notifications/internal/webhook"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func main() {
	var (
		kafkaBrokers    = flag.String("kafka-brokers", envOr("KAFKA_BROKERS", "localhost:9092"), "Comma-separated Kafka brokers")
		kafkaGroup      = flag.String("kafka-group", envOr("KAFKA_CONSUMER_GROUP", consumer.GroupID), "Kafka consumer group")
		dbURL           = flag.String("db-url", os.Getenv("DATABASE_URL"), "Postgres URL for notification_intent")
		grpcAddr        = flag.String("fulfillment-grpc", envOr("FULFILLMENT_GRPC", "localhost:8000"), "Private fulfillment gRPC address")
		grpcPlaintext   = flag.Bool("fulfillment-grpc-plaintext", envOr("FULFILLMENT_GRPC_PLAINTEXT", "false") == "true", "Use insecure gRPC")
		grpcToken       = flag.String("fulfillment-token", os.Getenv("FULFILLMENT_TOKEN"), "Bearer token for private API (required)")
		sessionInterval = flag.Duration("session-interval", consumer.DefaultSessionInterval,
			"Bound one Kafka consume session so newly created tenant topics are rediscovered")
		drainInterval = flag.Duration("intent-drain-interval", 5*time.Second, "How often to drain pending notification_intent rows")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if *dbURL == "" {
		logger.Error("db-url / DATABASE_URL is required")
		os.Exit(1)
	}
	if *grpcToken == "" {
		logger.Error("FULFILLMENT_TOKEN is required")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, *dbURL)
	if err != nil {
		logger.Error("connect postgres", slog.Any("error", err))
		os.Exit(1)
	}
	defer pool.Close()

	conn, err := dialFulfillment(ctx, *grpcAddr, *grpcPlaintext, *grpcToken)
	if err != nil {
		logger.Error("dial fulfillment", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	saramaConfig := sarama.NewConfig()
	saramaConfig.Version = sarama.V3_9_0_0
	saramaConfig.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRange()}
	saramaConfig.Consumer.Offsets.Initial = sarama.OffsetOldest
	saramaConfig.Consumer.Offsets.AutoCommit.Enable = true
	saramaConfig.Producer.Return.Successes = true
	saramaConfig.Producer.RequiredAcks = sarama.WaitForAll

	brokers := splitCSV(*kafkaBrokers)
	client, err := sarama.NewClient(brokers, saramaConfig)
	if err != nil {
		logger.Error("kafka client", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = client.Close() }()

	producer, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		logger.Error("kafka producer", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = producer.Close() }()

	group, err := sarama.NewConsumerGroupFromClient(*kafkaGroup, client)
	if err != nil {
		logger.Error("kafka consumer group", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = group.Close() }()

	deadLetter := dlq.NewKafkaPublisher(producer)
	intents := intent.NewStore(pool)
	inbox := fanout.NewInbox(logger, privatev1.NewNotificationsClient(conn))
	subsClient := privatev1.NewNotificationSubscriptionsClient(conn)
	deliveriesClient := privatev1.NewNotificationSubscriptionDeliveriesClient(conn)
	matcher := match.New(logger, subsClient, deliveriesClient)
	dispatcher := fanout.NewDispatcher(inbox, matcher)
	pipeline := ingest.New(logger, grant.DefaultMap, intents, dispatcher, deadLetter)
	handler := consumer.NewHandler(logger, pipeline)
	intentDrain := drain.New(logger, intents, dispatcher, deadLetter, *drainInterval)
	secretsClient := privatev1.NewSecretsClient(conn)
	webhookWorker := webhook.NewWorker(logger, deliveriesClient, subsClient, secretsClient, intents, *drainInterval)

	logger.Info("osac-notifications starting",
		slog.String("group", *kafkaGroup),
		slog.String("brokers", *kafkaBrokers),
	)

	go intentDrain.Run(ctx)
	go webhookWorker.Run(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- consumer.RunSessions(ctx, logger, group, handler, func(context.Context) ([]string, error) {
			return consumer.DiscoverIngestTopics(client)
		}, *sessionInterval)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil && ctx.Err() == nil {
			logger.Error("consumer stopped", slog.Any("error", err))
			os.Exit(1)
		}
	}
}

func dialFulfillment(ctx context.Context, addr string, plaintext bool, token string) (*grpc.ClientConn, error) {
	tokenCreds := oauth.TokenSource{
		TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}),
	}
	opts := []grpc.DialOption{}
	if plaintext {
		// Allow bearer auth on plaintext for local use only.
		opts = append(opts,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithPerRPCCredentials(insecureToken{PerRPCCredentials: tokenCreds}),
		)
	} else {
		opts = append(opts,
			grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})),
			grpc.WithPerRPCCredentials(tokenCreds),
		)
	}
	return grpc.DialContext(ctx, addr, opts...)
}

// insecureToken allows bearer credentials on plaintext gRPC.
type insecureToken struct {
	credentials.PerRPCCredentials
}

func (insecureToken) RequireTransportSecurity() bool { return false }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
