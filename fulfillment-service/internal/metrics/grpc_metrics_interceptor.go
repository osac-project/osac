/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// This file contains the implementation of a gRPC interceptor that generates Prometheus metrics.

package metrics

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// GrpcInterceptorBuilder contains the data and logic needed to build a new metrics interceptor that creates
// gRPC interceptors generating the following Prometheus metrics:
//
// For unary calls:
//
//	<subsystem>_unary_request_count - Number of gRPC unary requests received.
//	<subsystem>_unary_request_duration - Duration of gRPC unary requests in seconds.
//
// For streaming calls:
//
//	<subsystem>_stream_count - Number of gRPC streams opened.
//	<subsystem>_stream_duration - Duration of gRPC streams in seconds.
//	<subsystem>_stream_messages_sent - Number of messages sent over gRPC streams.
//	<subsystem>_stream_messages_received - Number of messages received over gRPC streams.
//
// To set the subsystem prefix use the SetSubsystem method.
//
// The duration buckets metrics contain an `le` label that indicates the upper bound. For example if the `le` label is
// `1` then the value will be the number of requests that were processed in less than one second.
//
// The metrics will have the following labels:
//
//	service - Name of the gRPC service, for example osac.public.v1.Clusters.
//	method - Name of the gRPC method, for example Create.
//	code - gRPC response code, for example OK or NotFound.
//
// To calculate the average request duration during the last 10 minutes, for example, use a Prometheus expression like
// this:
//
//	rate(<subsystem>_unary_request_duration_sum[10m]) / rate(<subsystem>_unary_request_duration_count[10m])
//
// Don't create objects of this type directly; use the NewGrpcInterceptor function instead.
type GrpcInterceptorBuilder struct {
	subsystem  string
	registerer prometheus.Registerer
}

// GrpcInterceptor contains the data and logic needed to intercept gRPC calls and generate Prometheus metrics.
type GrpcInterceptor struct {
	requestCount           *prometheus.CounterVec
	requestDuration        *prometheus.HistogramVec
	streamCount            *prometheus.CounterVec
	streamDuration         *prometheus.HistogramVec
	streamMessagesSent     *prometheus.CounterVec
	streamMessagesReceived *prometheus.CounterVec
}

// NewGrpcInterceptor creates a builder that can then be used to configure and create a new metrics interceptor.
func NewGrpcInterceptor() *GrpcInterceptorBuilder {
	return &GrpcInterceptorBuilder{}
}

// SetSubsystem sets the name of the subsystem that will be used to register the metrics with Prometheus. For example,
// if the value is `grpc_server` then the following metrics will be registered:
//
//	grpc_server_unary_request_count - Number of gRPC unary requests received.
//	grpc_server_unary_request_duration_sum - Total time to process gRPC unary requests, in seconds.
//	grpc_server_unary_request_duration_count - Total number of gRPC unary requests measured.
//	grpc_server_unary_request_duration_bucket - Number of gRPC unary requests organized in buckets.
//
// This is mandatory.
func (b *GrpcInterceptorBuilder) SetSubsystem(value string) *GrpcInterceptorBuilder {
	b.subsystem = value
	return b
}

// SetRegisterer sets the Prometheus registerer that will be used to register the metrics. this is mandatory.
func (b *GrpcInterceptorBuilder) SetRegisterer(value prometheus.Registerer) *GrpcInterceptorBuilder {
	b.registerer = value
	return b
}

// Build uses the information stored in the builder to create a new interceptor.
func (b *GrpcInterceptorBuilder) Build() (result *GrpcInterceptor, err error) {
	// Check parameters:
	if b.subsystem == "" {
		err = errors.New("subsystem is mandatory")
		return
	}
	if b.registerer == nil {
		err = errors.New("registerer is mandatory")
		return
	}

	// Register the request count metric:
	requestCount := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: b.subsystem,
			Name:      "unary_request_count",
			Help:      "Number of gRPC unary requests received.",
		},
		requestLabelNames,
	)
	err = b.registerer.Register(requestCount)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if errors.As(err, &registered) {
			requestCount = registered.ExistingCollector.(*prometheus.CounterVec)
		} else {
			return
		}
	}

	// Register the request duration metric:
	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: b.subsystem,
			Name:      "unary_request_duration",
			Help:      "Duration of gRPC unary requests in seconds.",
			Buckets:   unaryDurationBuckets,
		},
		requestLabelNames,
	)
	err = b.registerer.Register(requestDuration)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if errors.As(err, &registered) {
			requestDuration = registered.ExistingCollector.(*prometheus.HistogramVec)
		} else {
			return
		}
	}

	// Register the stream count metric:
	streamCount := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: b.subsystem,
			Name:      "stream_count",
			Help:      "Number of gRPC streams opened.",
		},
		streamLabelNames,
	)
	err = b.registerer.Register(streamCount)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if errors.As(err, &registered) {
			streamCount = registered.ExistingCollector.(*prometheus.CounterVec)
		} else {
			return
		}
	}

	// Register the stream duration metric:
	streamDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: b.subsystem,
			Name:      "stream_duration",
			Help:      "gRPC stream duration in seconds.",
			Buckets:   streamDurationBuckets,
		},
		streamLabelNames,
	)
	err = b.registerer.Register(streamDuration)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if errors.As(err, &registered) {
			streamDuration = registered.ExistingCollector.(*prometheus.HistogramVec)
		} else {
			return
		}
	}

	// Register the stream messages sent metric:
	streamMessagesSent := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: b.subsystem,
			Name:      "stream_messages_sent",
			Help:      "Number of messages sent over gRPC streams.",
		},
		streamLabelNames,
	)
	err = b.registerer.Register(streamMessagesSent)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if errors.As(err, &registered) {
			streamMessagesSent = registered.ExistingCollector.(*prometheus.CounterVec)
		} else {
			return
		}
	}

	// Register the stream messages received metric:
	streamMessagesReceived := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: b.subsystem,
			Name:      "stream_messages_received",
			Help:      "Number of messages received over gRPC streams.",
		},
		streamLabelNames,
	)
	err = b.registerer.Register(streamMessagesReceived)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if errors.As(err, &registered) {
			streamMessagesReceived = registered.ExistingCollector.(*prometheus.CounterVec)
			err = nil
		} else {
			return
		}
	}

	// Create and populate the object:
	result = &GrpcInterceptor{
		requestCount:           requestCount,
		requestDuration:        requestDuration,
		streamCount:            streamCount,
		streamDuration:         streamDuration,
		streamMessagesSent:     streamMessagesSent,
		streamMessagesReceived: streamMessagesReceived,
	}
	return
}

// UnaryServer is the unary server interceptor function that records metrics.
func (i *GrpcInterceptor) UnaryServer(ctx context.Context, request any, info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (response any, err error) {
	start := time.Now()
	response, err = handler(ctx, request)
	elapsed := time.Since(start)
	service, method := splitMethod(info.FullMethod)
	code := grpcstatus.Code(err)
	labels := prometheus.Labels{
		serviceLabelName: service,
		methodLabelName:  method,
		codeLabelName:    code.String(),
	}
	i.requestCount.With(labels).Inc()
	i.requestDuration.With(labels).Observe(elapsed.Seconds())
	return
}

// StreamServer is the stream server interceptor function that records metrics.
func (i *GrpcInterceptor) StreamServer(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo,
	handler grpc.StreamHandler) error {
	wrapped := &wrappedServerStream{ServerStream: stream}
	start := time.Now()
	err := handler(server, wrapped)
	elapsed := time.Since(start)
	service, method := splitMethod(info.FullMethod)
	code := grpcstatus.Code(err)
	labels := prometheus.Labels{
		serviceLabelName: service,
		methodLabelName:  method,
		codeLabelName:    code.String(),
	}
	i.streamCount.With(labels).Inc()
	i.streamDuration.With(labels).Observe(elapsed.Seconds())
	i.streamMessagesSent.With(labels).Add(float64(wrapped.sentCount))
	i.streamMessagesReceived.With(labels).Add(float64(wrapped.receivedCount))
	return err
}

// UnaryClient is the unary client interceptor function that records metrics.
func (i *GrpcInterceptor) UnaryClient(ctx context.Context, method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	start := time.Now()
	err := invoker(ctx, method, req, reply, cc, opts...)
	elapsed := time.Since(start)
	service, methodName := splitMethod(method)
	code := grpcstatus.Code(err)
	labels := prometheus.Labels{
		serviceLabelName: service,
		methodLabelName:  methodName,
		codeLabelName:    code.String(),
	}
	i.requestCount.With(labels).Inc()
	i.requestDuration.With(labels).Observe(elapsed.Seconds())
	return err
}

// StreamClient is the stream client interceptor function that records metrics.
func (i *GrpcInterceptor) StreamClient(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
	method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	start := time.Now()
	stream, err := streamer(ctx, desc, cc, method, opts...)
	if err != nil {
		elapsed := time.Since(start)
		service, methodName := splitMethod(method)
		code := grpcstatus.Code(err)
		labels := prometheus.Labels{
			serviceLabelName: service,
			methodLabelName:  methodName,
			codeLabelName:    code.String(),
		}
		i.streamCount.With(labels).Inc()
		i.streamDuration.With(labels).Observe(elapsed.Seconds())
		return nil, err
	}
	wrapped := &wrappedClientStream{
		ClientStream: stream,
		interceptor:  i,
		method:       method,
		start:        start,
	}
	return wrapped, nil
}

// splitMethod splits a gRPC full method name into service and method names.
func splitMethod(full string) (service, method string) {
	full = strings.TrimPrefix(full, "/")
	index := strings.LastIndex(full, "/")
	if index < 0 {
		service, method = full, ""
		return
	}
	service, method = full[:index], full[index+1:]
	return
}

// wrappedServerStream wraps a grpc.ServerStream to count sent and received messages.
type wrappedServerStream struct {
	grpc.ServerStream
	sentCount     int
	receivedCount int
}

// SendMsg wraps the underlying SendMsg and counts sent messages.
func (w *wrappedServerStream) SendMsg(m any) error {
	err := w.ServerStream.SendMsg(m)
	if err == nil {
		w.sentCount++
	}
	return err
}

// RecvMsg wraps the underlying RecvMsg and counts received messages.
func (w *wrappedServerStream) RecvMsg(m any) error {
	err := w.ServerStream.RecvMsg(m)
	if err == nil {
		w.receivedCount++
	}
	return err
}

// wrappedClientStream wraps a grpc.ClientStream to count sent and received messages and record metrics when the stream
// closes.
type wrappedClientStream struct {
	grpc.ClientStream
	interceptor   *GrpcInterceptor
	method        string
	start         time.Time
	sentCount     int
	receivedCount int
	finalized     bool
}

// SendMsg wraps the underlying SendMsg and counts sent messages.
func (w *wrappedClientStream) SendMsg(m any) error {
	err := w.ClientStream.SendMsg(m)
	if err == nil {
		w.sentCount++
	}
	return err
}

// RecvMsg wraps the underlying RecvMsg and counts received messages. When the stream ends (io.EOF or error), it
// records the metrics.
func (w *wrappedClientStream) RecvMsg(m any) error {
	err := w.ClientStream.RecvMsg(m)
	if err == nil {
		w.receivedCount++
	} else {
		w.finalize(err)
	}
	return err
}

// CloseSend wraps the underlying CloseSend.
func (w *wrappedClientStream) CloseSend() error {
	return w.ClientStream.CloseSend()
}

// finalize records the stream metrics when the stream ends. It ensures metrics are recorded only once.
func (w *wrappedClientStream) finalize(err error) {
	if w.finalized {
		return
	}
	w.finalized = true
	elapsed := time.Since(w.start)
	service, method := splitMethod(w.method)
	// io.EOF indicates normal stream completion, so we treat it as OK:
	var code grpccodes.Code
	if errors.Is(err, io.EOF) {
		code = grpccodes.OK
	} else {
		code = grpcstatus.Code(err)
	}
	labels := prometheus.Labels{
		serviceLabelName: service,
		methodLabelName:  method,
		codeLabelName:    code.String(),
	}
	w.interceptor.streamCount.With(labels).Inc()
	w.interceptor.streamDuration.With(labels).Observe(elapsed.Seconds())
	w.interceptor.streamMessagesSent.With(labels).Add(float64(w.sentCount))
	w.interceptor.streamMessagesReceived.With(labels).Add(float64(w.receivedCount))
}

// Label names for the metrics:
const (
	serviceLabelName = "service"
	methodLabelName  = "method"
	codeLabelName    = "code"
)

// requestLabelNames is the list of label names used for unary request metrics.
var requestLabelNames = []string{
	serviceLabelName,
	methodLabelName,
	codeLabelName,
}

// streamLabelNames is the list of label names used for stream metrics.
var streamLabelNames = []string{
	serviceLabelName,
	methodLabelName,
	codeLabelName,
}

// unaryDurationBuckets defines the histogram buckets for unary request durations.
var unaryDurationBuckets = []float64{
	0.005,
	0.01,
	0.025,
	0.05,
	0.1,
	0.25,
	0.5,
	1.0,
	2.5,
	5.0,
	10.0,
}

// streamDurationBuckets defines the histogram buckets for stream durations. Streams typically last longer than unary
// calls, so these buckets extend to higher values.
var streamDurationBuckets = []float64{
	0.1,
	0.5,
	1.0,
	5.0,
	10.0,
	30.0,
	60.0,
	120.0,
	300.0,
	600.0,
}
