// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package writeproxy

import (
	"bytes"
	"math"
	"sort"
	"testing"

	"github.com/golang/snappy"
	"google.golang.org/protobuf/encoding/protowire"
)

// ---- minimal remote-write encoder used by the tests -------------------------

type testLabel struct{ name, value string }

func appendBytesField(dst []byte, num protowire.Number, body []byte) []byte {
	dst = protowire.AppendTag(dst, num, protowire.BytesType)
	return protowire.AppendBytes(dst, body)
}

func testEncodeLabel(l testLabel) []byte {
	var b []byte
	b = appendBytesField(b, 1, []byte(l.name))
	b = appendBytesField(b, 2, []byte(l.value))
	return b
}

func encodeSample(value float64, ts int64) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.Fixed64Type)
	b = protowire.AppendFixed64(b, uint64Bits(value))
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(ts))
	return b
}

func encodeTimeSeries(labels []testLabel, samples ...[]byte) []byte {
	var b []byte
	for _, l := range labels {
		b = appendBytesField(b, 1, testEncodeLabel(l))
	}
	for _, s := range samples {
		b = appendBytesField(b, 2, s)
	}
	return b
}

func encodeRequest(series [][]byte, metadata [][]byte) []byte {
	var b []byte
	for _, s := range series {
		b = appendBytesField(b, 1, s)
	}
	for _, m := range metadata {
		b = appendBytesField(b, 3, m)
	}
	return b
}

func mustEncode(t *testing.T, raw []byte) []byte {
	t.Helper()
	return snappy.Encode(nil, raw)
}

// decodeRequest parses the transformed payload back into label maps per series.
func decodeRequest(t *testing.T, body []byte) ([]map[string]string, [][]byte, [][]byte) {
	t.Helper()
	raw, err := snappy.Decode(nil, body)
	if err != nil {
		t.Fatalf("snappy decode: %v", err)
	}
	var out []map[string]string
	var metadata [][]byte
	var unknown [][]byte
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			t.Fatalf("bad tag")
		}
		raw = raw[n:]
		switch num {
		case 1:
			v, m := protowire.ConsumeBytes(raw)
			if m < 0 {
				t.Fatalf("bad timeseries")
			}
			raw = raw[m:]
			out = append(out, decodeLabels(t, v))
		case 3:
			v, m := protowire.ConsumeBytes(raw)
			if m < 0 {
				t.Fatalf("bad metadata")
			}
			raw = raw[m:]
			metadata = append(metadata, append([]byte(nil), v...))
		default:
			_ = typ
			m := protowire.ConsumeFieldValue(num, typ, raw)
			if m < 0 {
				t.Fatalf("bad field value")
			}
			unknown = append(unknown, append([]byte(nil), raw[:m]...))
			raw = raw[m:]
		}
	}
	return out, metadata, unknown
}

func decodeLabels(t *testing.T, ts []byte) map[string]string {
	t.Helper()
	labels := map[string]string{}
	for len(ts) > 0 {
		num, typ, n := protowire.ConsumeTag(ts)
		if n < 0 {
			t.Fatalf("bad tag")
		}
		ts = ts[n:]
		if num == 1 {
			v, m := protowire.ConsumeBytes(ts)
			if m < 0 {
				t.Fatalf("bad label")
			}
			ts = ts[m:]
			name, value := testDecodeLabel(t, v)
			labels[name] = value
			continue
		}
		m := protowire.ConsumeFieldValue(num, typ, ts)
		if m < 0 {
			t.Fatalf("bad field")
		}
		ts = ts[m:]
	}
	return labels
}

func testDecodeLabel(t *testing.T, b []byte) (string, string) {
	t.Helper()
	var name, value string
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			t.Fatalf("bad tag")
		}
		b = b[n:]
		switch num {
		case 1:
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				t.Fatalf("bad name")
			}
			name = string(v)
			b = b[m:]
		case 2:
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				t.Fatalf("bad value")
			}
			value = string(v)
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			b = b[m:]
		}
	}
	return name, value
}

func labelsOf(pairs ...string) []testLabel {
	if len(pairs)%2 != 0 {
		panic("pairs must be even")
	}
	out := make([]testLabel, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, testLabel{pairs[i], pairs[i+1]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func uint64Bits(f float64) uint64 { return math.Float64bits(f) }

// ---- tests ------------------------------------------------------------------

func TestTransformOverwritesIdentityAndKeepsNamespace(t *testing.T) {
	raw := encodeRequest([][]byte{
		encodeTimeSeries(
			labelsOf("__name__", "node_cpu_seconds_total", "cluster", "forged", "managed_by", "attacker", "namespace", "team-a"),
			encodeSample(1, 1000),
		),
	}, nil)

	out, dropAll, err := Transform(mustEncode(t, raw), Identity{Cluster: "cluster-a"}, DefaultDropRule())
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if dropAll {
		t.Fatal("expected the series to survive")
	}
	series, _, _ := decodeRequest(t, out)
	if len(series) != 1 {
		t.Fatalf("got %d series, want 1", len(series))
	}
	got := series[0]
	if got["cluster"] != "cluster-a" {
		t.Errorf("cluster = %q, want cluster-a", got["cluster"])
	}
	if got["managed_by"] != "osac" {
		t.Errorf("managed_by = %q, want osac", got["managed_by"])
	}
	if got["namespace"] != "team-a" {
		t.Errorf("namespace = %q, want it unchanged", got["namespace"])
	}
	if got["__name__"] != "node_cpu_seconds_total" {
		t.Errorf("__name__ = %q, want it unchanged", got["__name__"])
	}
}

func TestTransformDropsDeniedFamilies(t *testing.T) {
	denied := []string{
		"kube_pod_info",
		"kube_namespace_labels",
		"container_memory_usage_bytes",
		"container_cpu_usage_seconds_total",
		"claim_refresh_latency_seconds",
		"mount_operation_seconds_total",
		"kserve_request_duration_seconds",
		"vllm_generation_tokens",
		"dcgm_gpu_utilization",
	}
	for _, name := range denied {
		t.Run(name, func(t *testing.T) {
			raw := encodeRequest([][]byte{
				encodeTimeSeries(labelsOf("__name__", name), encodeSample(1, 1)),
			}, nil)
			_, dropAll, err := Transform(mustEncode(t, raw), Identity{Cluster: "c"}, DefaultDropRule())
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if !dropAll {
				t.Errorf("%s must be dropped", name)
			}
		})
	}
}

func TestTransformKeepsAllowedFamilies(t *testing.T) {
	allowed := []string{
		"kube_node_info",
		"kube_node_status_capacity",
		"node_cpu_seconds_total",
		"node_network_receive_bytes_total",
	}
	for _, name := range allowed {
		t.Run(name, func(t *testing.T) {
			raw := encodeRequest([][]byte{
				encodeTimeSeries(labelsOf("__name__", name), encodeSample(1, 1)),
			}, nil)
			out, dropAll, err := Transform(mustEncode(t, raw), Identity{Cluster: "c"}, DefaultDropRule())
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if dropAll {
				t.Fatalf("%s must survive", name)
			}
			series, _, _ := decodeRequest(t, out)
			if len(series) != 1 || series[0]["__name__"] != name {
				t.Errorf("series = %v, want %s", series, name)
			}
		})
	}
}

func TestTransformMixedBatchKeepsOnlyAllowedSeries(t *testing.T) {
	raw := encodeRequest([][]byte{
		encodeTimeSeries(labelsOf("__name__", "kube_node_info"), encodeSample(1, 1)),
		encodeTimeSeries(labelsOf("__name__", "kube_pod_info"), encodeSample(1, 1)),
		encodeTimeSeries(labelsOf("__name__", "node_cpu_seconds_total"), encodeSample(1, 1)),
	}, nil)
	out, dropAll, err := Transform(mustEncode(t, raw), Identity{Cluster: "c"}, DefaultDropRule())
	if err != nil || dropAll {
		t.Fatalf("Transform: dropAll=%v err=%v", dropAll, err)
	}
	series, _, _ := decodeRequest(t, out)
	var names []string
	for _, s := range series {
		names = append(names, s["__name__"])
	}
	if len(names) != 2 || names[0] != "kube_node_info" || names[1] != "node_cpu_seconds_total" {
		t.Errorf("names = %v", names)
	}
}

func TestTransformPreservesMetadataAndUnknownFields(t *testing.T) {
	metadata := testEncodeLabel(testLabel{"__name__", "node_cpu_seconds_total"})
	raw := encodeRequest([][]byte{
		encodeTimeSeries(labelsOf("__name__", "node_cpu_seconds_total"), encodeSample(1, 1)),
	}, [][]byte{metadata})

	out, _, err := Transform(mustEncode(t, raw), Identity{Cluster: "c"}, DefaultDropRule())
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	_, gotMetadata, _ := decodeRequest(t, out)
	if len(gotMetadata) != 1 || !bytes.Equal(gotMetadata[0], metadata) {
		t.Errorf("metadata = %v, want %v", gotMetadata, metadata)
	}
}

func TestTransformRejectsMalformedBody(t *testing.T) {
	if _, _, err := Transform([]byte("not snappy"), Identity{Cluster: "c"}, DefaultDropRule()); err == nil {
		t.Fatal("expected an error for a malformed body")
	}
}
