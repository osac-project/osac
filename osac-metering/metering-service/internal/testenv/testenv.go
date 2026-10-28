/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package testenv starts the real PostgreSQL and Kafka containers integration tests run
// against, and writes test events to topics.
package testenv

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/kafkatest"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/topics"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var (
	kafkaOnce      sync.Once
	kafkaContainer *kafkatest.Container
	kafkaErr       error
	dbOnce         sync.Once
	dbContainer    *database.Container
	dbErr          error
)

// Kafka starts the shared Kafka broker. Tests must call StopContainers when they are done,
// usually from TestMain.
func Kafka(t *testing.T) *kafkatest.Container {
	t.Helper()
	kafkaOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		kafkaContainer, kafkaErr = kafkatest.Start(ctx, logr.Discard())
	})
	if kafkaErr != nil {
		t.Fatalf("starting kafka: %v", kafkaErr)
	}
	return kafkaContainer
}

// Database starts the shared PostgreSQL container and returns a fresh database on it.
func Database(t *testing.T) *database.Store {
	t.Helper()
	dbOnce.Do(func() {
		var err error
		dbContainer, err = database.NewContainer(logr.Discard())
		if err == nil {
			err = dbContainer.Start(context.Background())
		}
		dbErr = err
	})
	if dbErr != nil {
		t.Fatalf("starting database container: %v", dbErr)
	}
	instance := dbContainer.NewInstance()
	pool, err := instance.Pool(context.Background())
	if err != nil {
		t.Fatalf("creating pool: %v", err)
	}
	t.Cleanup(func() { _ = instance.Close(context.Background()) })
	return database.NewStore(pool)
}

// StopContainers stops the shared containers. Call it from TestMain after m.Run.
func StopContainers() {
	if kafkaContainer != nil {
		_ = kafkaContainer.Stop()
	}
	if dbContainer != nil {
		_ = dbContainer.Stop(context.Background())
	}
}

// Broker returns the shared broker address.
func Broker(t *testing.T) string {
	t.Helper()
	return Kafka(t).Brokers()
}

// SaramaConfig returns a plain configuration for the shared broker.
func SaramaConfig(t *testing.T) *sarama.Config {
	t.Helper()
	return Kafka(t).Config()
}

// NewClient creates a sarama client on the shared broker.
func NewClient(t *testing.T) sarama.Client {
	t.Helper()
	client, err := sarama.NewClient([]string{Broker(t)}, SaramaConfig(t))
	if err != nil {
		t.Fatalf("creating client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// NewConsumer creates a consumer on the shared broker.
func NewConsumer(t *testing.T) sarama.Consumer {
	t.Helper()
	consumer, err := sarama.NewConsumerFromClient(NewClient(t))
	if err != nil {
		t.Fatalf("creating consumer: %v", err)
	}
	t.Cleanup(func() { _ = consumer.Close() })
	return consumer
}

// NewProducer creates a sync producer on the shared broker.
func NewProducer(t *testing.T) sarama.SyncProducer {
	t.Helper()
	producer, err := sarama.NewSyncProducer([]string{Broker(t)}, SaramaConfig(t))
	if err != nil {
		t.Fatalf("creating producer: %v", err)
	}
	t.Cleanup(func() { _ = producer.Close() })
	return producer
}

// DefaultMetersYAML returns the default meters exactly as the chart ships them, so
// tests bill under what an installation gets and the two can never drift.
func DefaultMetersYAML(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the testenv source file")
	}
	chartPath := filepath.Join(
		filepath.Dir(thisFile), "..", "..", "..", "charts", "osac-metering", "rules", "0001-meters.yaml",
	)
	content, err := os.ReadFile(chartPath)
	if err != nil {
		t.Fatalf("reading the chart's default meters: %v", err)
	}
	return string(content)
}

// DefaultRevision parses the chart's default meters as revision 0001, effective from
// the epoch.
func DefaultRevision(t *testing.T) rules.Revision {
	t.Helper()
	revision, err := rules.Parse(
		rules.NamePrefix+"0001", "1970-01-01T00:00:00Z", DefaultMetersYAML(t),
	)
	if err != nil {
		t.Fatalf("parsing the chart's default meters: %v", err)
	}
	return revision
}

// ProduceEvents writes events to the tenant's event topic and returns the record count.
// Each event's payload is stamped with the topic's tenant first, the invariant the
// publisher keeps: an event's payload tenant is the tenant of the topic it was written to.
func ProduceEvents(t *testing.T, tenant string, events []*privatev1.Event) int {
	t.Helper()
	return produceEvents(t, tenant, events, true)
}

// ProduceEventsAsWritten writes events without stamping the payload tenant. Tests of
// misrouted events need the payload to disagree with the topic it lands on.
func ProduceEventsAsWritten(t *testing.T, tenant string, events []*privatev1.Event) int {
	t.Helper()
	return produceEvents(t, tenant, events, false)
}

func produceEvents(t *testing.T, tenant string, events []*privatev1.Event, stamp bool) int {
	t.Helper()
	topic := topics.Events(tenant)
	if err := Kafka(t).CreateTopic(topic); err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	producer := NewProducer(t)
	for _, event := range events {
		if stamp {
			StampTenant(event, tenant)
		}
		value, err := proto.Marshal(event)
		if err != nil {
			t.Fatalf("marshaling event: %v", err)
		}
		_, _, err = producer.SendMessage(&sarama.ProducerMessage{
			Topic: topic, Key: sarama.StringEncoder(event.GetId()), Value: sarama.ByteEncoder(value),
		})
		if err != nil {
			t.Fatalf("producing event: %v", err)
		}
	}
	return len(events)
}

// StampTenant sets the payload's metadata tenant. It walks the event's payload oneof, so
// every resource type is covered without enumerating them.
func StampTenant(event *privatev1.Event, tenant string) {
	message := event.ProtoReflect()
	payloadOneof := message.Descriptor().Oneofs().ByName("payload")
	if payloadOneof == nil {
		return
	}
	payloadField := message.WhichOneof(payloadOneof)
	if payloadField == nil {
		return
	}
	payload := message.Get(payloadField).Message()
	metadataField := payload.Descriptor().Fields().ByName("metadata")
	if metadataField == nil {
		return
	}
	metadata := payload.Mutable(metadataField).Message()
	tenantField := metadata.Descriptor().Fields().ByName("tenant")
	if tenantField == nil {
		return
	}
	metadata.Set(tenantField, protoreflect.ValueOfString(tenant))
}

// ConsumeAll reads every record of a topic from the beginning.
func ConsumeAll(t *testing.T, topic string) [][]byte {
	t.Helper()
	consumer := NewConsumer(t)
	high, err := Kafka(t).GetOffsetHigh(topic)
	if err != nil {
		t.Fatalf("reading the topic end: %v", err)
	}
	if high <= 0 {
		return nil
	}
	partition, err := consumer.ConsumePartition(topic, 0, sarama.OffsetOldest)
	if err != nil {
		t.Fatalf("consuming %s: %v", topic, err)
	}
	defer func() { _ = partition.Close() }()
	var records [][]byte
	deadline := time.Now().Add(10 * time.Second)
	for int64(len(records)) < high {
		select {
		case message, ok := <-partition.Messages():
			if !ok {
				return records
			}
			records = append(records, message.Value)
		case <-time.After(time.Until(deadline)):
			t.Fatalf("timed out reading %s: got %d of %d records", topic, len(records), high)
		}
	}
	return records
}

// VMEvent builds a compute instance snapshot event with the given state, state change time,
// running instance type, and type change time.
func VMEvent(
	id string,
	typ privatev1.EventType,
	timestamp time.Time,
	creation time.Time,
	project string,
	state string,
	stateSince time.Time,
	runningType string,
	typeSince time.Time,
) *privatev1.Event {
	return VMEventOf("vm-7", id, typ, timestamp, creation, project, state, stateSince, runningType, typeSince)
}

// VMEventOf builds a compute event like VMEvent for the given compute instance, so a
// test can bill several VMs of one tenant side by side.
func VMEventOf(
	vmID string,
	id string,
	typ privatev1.EventType,
	timestamp time.Time,
	creation time.Time,
	project string,
	state string,
	stateSince time.Time,
	runningType string,
	typeSince time.Time,
) *privatev1.Event {
	return &privatev1.Event{
		Id:        id,
		Type:      typ,
		Timestamp: timestamppb.New(timestamp),
		Payload: &privatev1.Event_ComputeInstance{
			ComputeInstance: &privatev1.ComputeInstance{
				Id: vmID,
				Metadata: &privatev1.Metadata{
					Tenant:            "acme",
					Project:           project,
					CreationTimestamp: timestamppb.New(creation),
				},
				Spec: &privatev1.ComputeInstanceSpec{
					Template:  &privatev1.ComputeInstanceTemplateReference{Id: "1", Name: "tpl-1"},
					DiskImage: &privatev1.DiskImageReference{Id: "img-1", Name: "rhel-9"},
					BootDisk:  &privatev1.ComputeInstanceDisk{SizeGib: int32Ptr(30)},
				},
				Status: &privatev1.ComputeInstanceStatus{
					State:                      computeState(state),
					StateTransitionTime:        timestamppb.New(stateSince),
					InstanceType:               &privatev1.InstanceTypeReference{Id: "it-1", Name: runningType},
					InstanceTypeTransitionTime: timestamppb.New(typeSince),
				},
			},
		},
	}
}

func computeState(state string) privatev1.ComputeInstanceState {
	return privatev1.ComputeInstanceState(
		privatev1.ComputeInstanceState_value["COMPUTE_INSTANCE_STATE_"+state],
	)
}

func int32Ptr(v int32) *int32 { return &v }

// VolumeEvent builds a volume snapshot event with the given state, state change time, storage
// tier, and provisioned size. The protocol and vendor volume ID are set from creation, like a
// provisioned volume the vendor reports with its first snapshot.
func VolumeEvent(
	id string,
	typ privatev1.EventType,
	timestamp time.Time,
	creation time.Time,
	project string,
	state string,
	stateSince time.Time,
	storageTier string,
	sizeGib int64,
) *privatev1.Event {
	return &privatev1.Event{
		Id:        id,
		Type:      typ,
		Timestamp: timestamppb.New(timestamp),
		Payload: &privatev1.Event_Volume{
			Volume: &privatev1.Volume{
				Id: "vol-1",
				Metadata: &privatev1.Metadata{
					Tenant:            "acme",
					Project:           project,
					CreationTimestamp: timestamppb.New(creation),
				},
				Spec: &privatev1.VolumeSpec{StorageTier: storageTier, SizeGib: sizeGib},
				Status: &privatev1.VolumeStatus{
					State:               volumeState(state),
					StateTransitionTime: timestamppb.New(stateSince),
					VendorVolumeId:      "vend-9",
					Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					ProvisionedSizeGib:  sizeGib,
				},
			},
		},
	}
}

func volumeState(state string) privatev1.VolumeState {
	return privatev1.VolumeState(privatev1.VolumeState_value["VOLUME_STATE_"+state])
}

// ClusterEvent builds a cluster snapshot event with the given state, state change time,
// template, version, and version change time.
func ClusterEvent(
	id string,
	typ privatev1.EventType,
	timestamp time.Time,
	creation time.Time,
	project string,
	state string,
	stateSince time.Time,
	template string,
	version string,
	versionSince time.Time,
) *privatev1.Event {
	return &privatev1.Event{
		Id:        id,
		Type:      typ,
		Timestamp: timestamppb.New(timestamp),
		Payload: &privatev1.Event_Cluster{
			Cluster: &privatev1.Cluster{
				Id: "cluster-1",
				Metadata: &privatev1.Metadata{
					Tenant:            "acme",
					Project:           project,
					CreationTimestamp: timestamppb.New(creation),
				},
				Spec: &privatev1.ClusterSpec{
					Template: &privatev1.ClusterTemplateReference{Id: "ctpl-1", Name: template},
					Version:  &privatev1.ClusterVersionReference{Id: "cver-1", Name: version},
				},
				Status: &privatev1.ClusterStatus{
					State:                 clusterState(state),
					StateTransitionTime:   timestamppb.New(stateSince),
					VersionTransitionTime: timestamppb.New(versionSince),
				},
			},
		},
	}
}

func clusterState(state string) privatev1.ClusterState {
	return privatev1.ClusterState(privatev1.ClusterState_value["CLUSTER_STATE_"+state])
}

// BMIEvent builds a bare metal instance snapshot event with the given state, state change
// time, and instance type. The cluster and node set name the cluster the worker serves:
// both are fixed once set, and an empty cluster leaves them unset, like a worker the system
// has not assigned yet.
func BMIEvent(
	id string,
	typ privatev1.EventType,
	timestamp time.Time,
	creation time.Time,
	project string,
	state string,
	stateSince time.Time,
	instanceType string,
	cluster string,
	nodeSet string,
) *privatev1.Event {
	status := &privatev1.BareMetalInstanceStatus{
		State:               baremetalState(state),
		StateTransitionTime: timestamppb.New(stateSince),
	}
	if cluster != "" {
		// The name diverges from the id on purpose: the owner reference renders by its id,
		// which never changes, so the cluster dimension and the root resolution stay stable
		// even when a name differs.
		status.Cluster = &privatev1.ClusterLocalReference{Id: cluster, Name: cluster + "-display"}
	}
	if nodeSet != "" {
		status.NodeSet = &nodeSet
	}
	return &privatev1.Event{
		Id:        id,
		Type:      typ,
		Timestamp: timestamppb.New(timestamp),
		Payload: &privatev1.Event_BareMetalInstance{
			BareMetalInstance: &privatev1.BareMetalInstance{
				Id: "bmi-1",
				Metadata: &privatev1.Metadata{
					Tenant:            "acme",
					Project:           project,
					CreationTimestamp: timestamppb.New(creation),
				},
				Spec: &privatev1.BareMetalInstanceSpec{
					InstanceType: &privatev1.BareMetalInstanceTypeReference{Id: "bmit-1", Name: instanceType},
				},
				Status: status,
			},
		},
	}
}

func baremetalState(state string) privatev1.BareMetalInstanceState {
	return privatev1.BareMetalInstanceState(
		privatev1.BareMetalInstanceState_value["BARE_METAL_INSTANCE_STATE_"+state],
	)
}
