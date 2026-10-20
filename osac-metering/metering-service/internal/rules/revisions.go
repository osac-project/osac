/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package rules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/osac-project/osac-metering/internal/database"
)

// Source is where metering reads rule revisions at startup.
type Source interface {
	// List returns every revision the source holds, sorted by revision number.
	List(ctx context.Context) ([]Revision, error)
}

// ConfigMapSource reads revisions from immutable ConfigMaps in a namespace. Metering only
// reads them; only cluster administrators create them.
type ConfigMapSource struct {
	client    kubernetes.Interface
	namespace string
}

// NewConfigMapSource creates a source on the in-cluster Kubernetes client.
func NewConfigMapSource(namespace string) (*ConfigMapSource, error) {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubernetes client config: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}
	return &ConfigMapSource{client: client, namespace: namespace}, nil
}

// List returns every revision ConfigMap in the namespace.
func (s *ConfigMapSource) List(ctx context.Context) ([]Revision, error) {
	configMaps, err := s.client.CoreV1().ConfigMaps(s.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing rule ConfigMaps in %s: %w", s.namespace, err)
	}
	var revisions []Revision
	for _, configMap := range configMaps.Items {
		if !strings.HasPrefix(configMap.Name, NamePrefix) {
			continue
		}
		revision, err := Parse(
			configMap.Name, configMap.Data["effective_at"], configMap.Data["meters.yaml"],
		)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	sortRevisions(revisions)
	return revisions, nil
}

// DirectorySource reads revision ConfigMap manifests from a directory. It serves tests and
// development installs.
type DirectorySource struct {
	directory string
}

// NewDirectorySource creates a source on a directory of ConfigMap YAML manifests.
func NewDirectorySource(directory string) *DirectorySource {
	return &DirectorySource{directory: directory}
}

// configMapManifest is the part of a ConfigMap manifest the source reads.
type configMapManifest struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Data map[string]string `yaml:"data"`
}

// List returns every revision manifest in the directory.
func (s *DirectorySource) List(ctx context.Context) ([]Revision, error) {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return nil, fmt.Errorf("reading rules directory %s: %w", s.directory, err)
	}
	var revisions []Revision
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(s.directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", entry.Name(), err)
		}
		var manifest configMapManifest
		if err := yaml.Unmarshal(content, &manifest); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}
		revision, err := Parse(
			manifest.Metadata.Name, manifest.Data["effective_at"], manifest.Data["meters.yaml"],
		)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	sortRevisions(revisions)
	return revisions, nil
}

func sortRevisions(revisions []Revision) {
	// Sort numerically, not as text: Parse only produces zero-padded four-digit
	// numbers, which happen to order the same either way, but a revision built
	// any other way must still order 0002 before 0010, not "10" before "2".
	sort.Slice(revisions, func(i, j int) bool {
		left, leftErr := strconv.Atoi(revisions[i].Number)
		right, rightErr := strconv.Atoi(revisions[j].Number)
		if leftErr != nil || rightErr != nil {
			return revisions[i].Number < revisions[j].Number
		}
		return left < right
	})
}

// CheckStartup validates the revisions the source holds against the revisions metering has
// used. It fails when metering would silently change past usage: when a revision it has used
// is missing, when a used revision changed, or when a new revision would take effect in the
// past while metering already used another revision. It returns every present revision, so the
// caller records the ones it has not used yet.
func CheckStartup(
	used []database.RuleRevisionRow, present []Revision, now func() time.Time,
) ([]Revision, error) {
	if len(present) == 0 {
		return nil, fmt.Errorf("no rule revisions found; create %s0001 first", NamePrefix)
	}
	byNumber := make(map[string]Revision, len(present))
	for _, revision := range present {
		if _, repeated := byNumber[revision.Number]; repeated {
			return nil, fmt.Errorf("revision %s appears more than once", revision.Number)
		}
		byNumber[revision.Number] = revision
	}
	for i := 1; i < len(present); i++ {
		if !present[i].EffectiveAt.After(present[i-1].EffectiveAt) {
			return nil, fmt.Errorf(
				"revision %s takes effect at %s, not after revision %s at %s",
				present[i].Number, present[i].EffectiveAt,
				present[i-1].Number, present[i-1].EffectiveAt,
			)
		}
	}

	usedSet := make(map[string]database.RuleRevisionRow, len(used))
	for _, row := range used {
		usedSet[row.Revision] = row
	}
	for number, row := range usedSet {
		revision, found := byNumber[number]
		if !found {
			return nil, fmt.Errorf("revision %s was used before but is now missing", number)
		}
		if !revision.EffectiveAt.Equal(row.EffectiveAt) || revision.MetersYAML != row.MetersYAML {
			return nil, fmt.Errorf("revision %s changed after metering used it", number)
		}
	}
	for _, revision := range present {
		if _, isUsed := usedSet[revision.Number]; isUsed {
			continue
		}
		if len(used) > 0 && !revision.EffectiveAt.After(now()) {
			return nil, fmt.Errorf(
				"revision %s takes effect at %s, which is in the past; "+
					"recording it now would change past usage",
				revision.Number, revision.EffectiveAt,
			)
		}
	}

	// A meter's unit never changes; a different unit needs a new meter name. The component
	// names the billed component, so it holds the same rule. Without this check a revision
	// could change a meter's unit mid-stream and silently re-scale its usage.
	type meterIdentity struct{ resource, meter string }
	type meterShape struct{ unit, component string }
	shapes := map[meterIdentity]meterShape{}
	for _, revision := range present {
		for _, meter := range revision.Meters {
			identity := meterIdentity{meter.Resource, meter.Meter}
			shape := meterShape{meter.Unit, meter.EffectiveComponent()}
			if previous, seen := shapes[identity]; seen && previous != shape {
				return nil, fmt.Errorf(
					"revision %s changes meter %s of %s from unit %s component %s to unit %s component %s",
					revision.Number, meter.Meter, meter.Resource,
					previous.unit, previous.component, shape.unit, shape.component,
				)
			}
			shapes[identity] = shape
		}
	}
	return present, nil
}
