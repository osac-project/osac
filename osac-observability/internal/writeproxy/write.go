// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

// Package writeproxy implements the managed-cluster remote-write endpoint. It verifies the
// client certificate, stamps the cluster identity from that certificate, drops metric
// families that must never leave the tenant cluster, and forwards the request to Thanos
// Receive.
package writeproxy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/golang/snappy"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	// ClusterLabel is the external label that identifies the writing cluster.
	ClusterLabel = "cluster"
	// ManagedByLabel marks series that arrived through the managed write path.
	ManagedByLabel = "managed_by"
	// ManagedByValue is the only value the proxy writes for ManagedByLabel.
	ManagedByValue = "osac"
	// ManagementCluster is the identity the management cluster writes with directly. It is
	// never accepted from a client certificate.
	ManagementCluster = "management"
)

// Identity is the trusted identity established from the client certificate. The proxy, not
// the client, decides the values stamped from it.
type Identity struct {
	// Cluster is the certificate common name.
	Cluster string
}

// DropRule describes which metric names may not leave a tenant cluster. The proxy applies it
// again even though the cluster's Prometheus already does, because a tenant controls its own
// Prometheus configuration.
type DropRule struct {
	// KeepKubePrefix is the only kube_ family allowed through.
	KeepKubePrefix string
	// DropPrefixes are denied metric-name prefixes.
	DropPrefixes []string
}

// DefaultDropRule is the deny rule from the metrics-store design: of the kube_ families only
// kube_node_ survives, and tenant-application, model-serving and GPU families are dropped.
func DefaultDropRule() DropRule {
	return DropRule{
		KeepKubePrefix: "kube_node_",
		DropPrefixes:   []string{"container_", "claim", "mount", "kserve_", "vllm_", "dcgm_"},
	}
}

// Drop reports whether a metric name must be discarded.
func (r DropRule) Drop(name string) bool {
	if strings.HasPrefix(name, "kube_") {
		return !strings.HasPrefix(name, r.KeepKubePrefix)
	}
	for _, prefix := range r.DropPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

type wireLabel struct {
	name  string
	value string
}

const metricNameLabel = "__name__"

// Transform decodes a snappy-compressed remote-write request, replaces the identity labels,
// applies the drop rule, and re-encodes the surviving series. Metadata and any field it does
// not understand are copied through unchanged. dropAll reports that no series survived; it is
// not an error, because a batch made only of dropped families is a valid request.
func Transform(body []byte, identity Identity, rule DropRule) (out []byte, dropAll bool, err error) {
	raw, err := snappy.Decode(nil, body)
	if err != nil {
		return nil, false, fmt.Errorf("decoding snappy payload: %w", err)
	}
	encoded := make([]byte, 0, len(raw))
	kept := 0
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return nil, false, errMalformed("request tag")
		}
		field := raw
		raw = raw[n:]
		if num == 1 && typ == protowire.BytesType {
			value, m := protowire.ConsumeBytes(raw)
			if m < 0 {
				return nil, false, errMalformed("timeseries")
			}
			raw = raw[m:]
			series, keep, err := rewriteTimeSeries(value, identity, rule)
			if err != nil {
				return nil, false, err
			}
			if !keep {
				continue
			}
			kept++
			encoded = protowire.AppendTag(encoded, 1, protowire.BytesType)
			encoded = protowire.AppendBytes(encoded, series)
			continue
		}
		m := protowire.ConsumeFieldValue(num, typ, raw)
		if m < 0 {
			return nil, false, errMalformed("request field")
		}
		encoded = append(encoded, field[:n+m]...)
		raw = raw[m:]
	}
	return snappy.Encode(nil, encoded), kept == 0, nil
}

func rewriteTimeSeries(series []byte, identity Identity, rule DropRule) (out []byte, keep bool, err error) {
	var labels []wireLabel
	var rest [][]byte
	for len(series) > 0 {
		num, typ, n := protowire.ConsumeTag(series)
		if n < 0 {
			return nil, false, errMalformed("timeseries tag")
		}
		field := series
		series = series[n:]
		if num == 1 && typ == protowire.BytesType {
			value, m := protowire.ConsumeBytes(series)
			if m < 0 {
				return nil, false, errMalformed("label")
			}
			series = series[m:]
			label, err := decodeLabel(value)
			if err != nil {
				return nil, false, err
			}
			labels = append(labels, label)
			continue
		}
		m := protowire.ConsumeFieldValue(num, typ, series)
		if m < 0 {
			return nil, false, errMalformed("timeseries field")
		}
		rest = append(rest, append([]byte(nil), field[:n+m]...))
		series = series[m:]
	}

	name := ""
	rewritten := make([]wireLabel, 0, len(labels)+2)
	for _, label := range labels {
		switch label.name {
		case metricNameLabel:
			name = label.value
		case ClusterLabel, ManagedByLabel:
			// Never trust a label the writer chose.
			continue
		}
		rewritten = append(rewritten, label)
	}
	if rule.Drop(name) {
		return nil, false, nil
	}
	rewritten = append(rewritten,
		wireLabel{name: ClusterLabel, value: identity.Cluster},
		wireLabel{name: ManagedByLabel, value: ManagedByValue},
	)
	sort.Slice(rewritten, func(i, j int) bool { return rewritten[i].name < rewritten[j].name })

	out = make([]byte, 0, len(series))
	for _, label := range rewritten {
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendBytes(out, encodeLabel(label))
	}
	for _, field := range rest {
		out = append(out, field...)
	}
	return out, true, nil
}

func decodeLabel(b []byte) (wireLabel, error) {
	var label wireLabel
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return label, errMalformed("label tag")
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			value, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return label, errMalformed("label name")
			}
			label.name = string(value)
			b = b[m:]
		case num == 2 && typ == protowire.BytesType:
			value, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return label, errMalformed("label value")
			}
			label.value = string(value)
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return label, errMalformed("label field")
			}
			b = b[m:]
		}
	}
	return label, nil
}

func encodeLabel(label wireLabel) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, label.name)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, label.value)
	return b
}

func errMalformed(what string) error {
	return fmt.Errorf("malformed remote-write request: %s", what)
}
