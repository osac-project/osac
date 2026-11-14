// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const ignitionFixtureName = "caas-it-ignition"

// CreateIgnition prepares independent artifact keys for the requested inventory groups.
func CreateIgnition(
	ctx context.Context, kube kubernetes.Interface, namespace string, groups []string,
) (*corev1.ConfigMap, error) {
	data := make(map[string]string, len(groups))
	for _, group := range groups {
		data[ignitionKey(group)] = `{"ignition":{"version":"3.2.0"}}`
	}

	return kube.CoreV1().ConfigMaps(namespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ignitionFixtureName, Namespace: namespace}, Data: data,
	}, metav1.CreateOptions{})
}

// ignitionKey maps a group to its ConfigMap key and served filename.
func ignitionKey(group string) string {
	return group + ".json"
}

// Ignition reads and updates one group's mutable discovery-data HTTP fixture.
type Ignition struct {
	Kube      kubernetes.Interface
	Namespace string
	Group     string
}

// URL returns this group's artifact endpoint on the shared static server.
func (i *Ignition) URL() string {
	return "http://" + ignitionFixtureName + "." + i.Namespace + ".svc/" + ignitionKey(i.Group)
}

// Read returns the served bytes without exposing user-data in errors.
func (i *Ignition) Read(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var status int

	result := i.Kube.CoreV1().RESTClient().Get().Namespace(i.Namespace).
		Resource("services").Name("http:caas-it-ignition:80").SubResource("proxy").
		Suffix(ignitionKey(i.Group)).Do(ctx).StatusCode(&status)

	content, err := result.Raw()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, fmt.Errorf("read discovery ignition through service proxy: HTTP %d", status)
	}

	slog.DebugContext(ctx, "observed served discovery ignition",
		"namespace", i.Namespace,
		"group", i.Group,
		"httpStatus", status,
		"bytes", len(content),
		"validJSON", json.Valid(content),
	)

	return content, nil
}

// WaitForContent waits for the expected bytes to reach the HTTP server.
// Read errors and differing content retry for two minutes; errors keep payloads private.
func (i *Ignition) WaitForContent(ctx context.Context, expected []byte) error {
	var readError error

	err := wait.PollUntilContextTimeout(ctx, time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		served, err := i.Read(ctx)
		readError = err

		return err == nil && bytes.Equal(served, expected), nil
	})
	if err != nil {
		return fmt.Errorf("wait for discovery ignition content for group %s: %w", i.Group, errors.Join(err, readError))
	}

	return nil
}

// Stored reads the configured discovery bytes from the fixture ConfigMap.
// The HTTP server observes ConfigMap updates asynchronously; Read returns served bytes.
func (i *Ignition) Stored(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	settings, err := i.Kube.CoreV1().ConfigMaps(i.Namespace).Get(ctx, ignitionFixtureName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return []byte(settings.Data[ignitionKey(i.Group)]), nil
}

// Set changes the configured bytes and preserves the fixture's other data.
func (i *Ignition) Set(ctx context.Context, content []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	patch, err := json.Marshal(map[string]map[string]string{"data": {ignitionKey(i.Group): string(content)}})
	if err != nil {
		return err
	}

	_, err = i.Kube.CoreV1().ConfigMaps(i.Namespace).
		Patch(ctx, ignitionFixtureName, types.MergePatchType, patch, metav1.PatchOptions{})
	if err == nil {
		slog.DebugContext(ctx, "updated discovery ignition ConfigMap; awaiting HTTP propagation",
			"namespace", i.Namespace,
			"group", i.Group,
			"bytes", len(content),
			"validJSON", json.Valid(content),
		)
	}

	return err
}
