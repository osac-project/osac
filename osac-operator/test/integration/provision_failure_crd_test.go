// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// TestProvisionFailureCRDValidation requires the generated operator-crds Helm
// chart installed in the selected Kind cluster. It exercises API admission
// directly; no operator, Fulfillment or AAP process participates.
func TestProvisionFailureCRDValidation(t *testing.T) {
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{})
	raw, err := loader.RawConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw.CurrentContext, "kind-") {
		t.Skip("requires an explicitly selected Kind context with the current operator-crds chart installed")
	}
	config, err := loader.ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	core, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	api, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	namespace, err := core.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "provision-failure-crd-"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		current, readErr := core.CoreV1().Namespaces().Get(cleanupCtx, namespace.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(readErr) {
			return
		}
		if readErr != nil || current.UID != namespace.UID {
			t.Error("refusing namespace cleanup without matching fresh UID")
			return
		}
		if deleteErr := core.CoreV1().Namespaces().Delete(cleanupCtx, namespace.Name, metav1.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &current.UID},
		}); deleteErr != nil {
			t.Errorf("namespace cleanup: %v", deleteErr)
		}
	})
	resource := api.Resource(schema.GroupVersionResource{
		Group: "osac.openshift.io", Version: "v1alpha1", Resource: "computeinstances",
	}).Namespace(namespace.Name)
	instance, err := resource.Create(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "osac.openshift.io/v1alpha1", "kind": "ComputeInstance",
		"metadata": map[string]any{"name": "checkpoint", "annotations": map[string]any{
			"osac.openshift.io/management-state": "unmanaged", "osac.openshift.io/tenant": namespace.Name,
		}},
		"spec": map[string]any{
			"templateID": "fixture_template",
			"image": map[string]any{
				"sourceType": "registry", "sourceRef": "quay.io/fedora/fedora-coreos:stable",
			},
			"vcpus":     int64(2),
			"memoryGiB": int64(4), "runStrategy": "Always",
			"bootDisk": map[string]any{"sizeGiB": int64(20), "storageTier": "standard"},
		},
	}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	failure := map[string]any{
		"reason": "ProviderReported", "message": "Provisioning failed.",
		"attempt": int64(1), "observedAt": time.Now().UTC().Format(time.RFC3339),
	}
	for _, reason := range []string{
		"ProviderReported", "ResultRejected", "TimedOut", "JobLost", "JobNotStarted", "InvalidResult", "ProviderError",
	} {
		failure["reason"] = reason
		failure["message"] = "Internal error"
		switch reason {
		case "ProviderReported":
			failure["message"] = "Provisioning failed."
		case "TimedOut":
			failure["message"] = "The provisioning attempt timed out."
		}
		setProvisionFailureStatus(instance, failure)
		instance, err = resource.UpdateStatus(ctx, instance, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("valid %s checkpoint: %v", reason, err)
		}
		stored, readErr := resource.Get(ctx, instance.GetName(), metav1.GetOptions{})
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !reflect.DeepEqual(provisionFailureSnapshot(t, stored), failure) {
			t.Fatal("valid checkpoint changed after API persistence")
		}
	}
	cases := []struct {
		name, field string
		value       any
		omit        bool
	}{
		{"invalid reason", "reason", "Unrecognized", false},
		{"empty message", "message", "", false},
		{"zero failed attempt", "attempt", int64(0), false},
		{"invalid observation time", "observedAt", "not-a-timestamp", false},
		{"missing reason", "reason", nil, true},
		{"missing message", "message", nil, true},
		{"missing attempt", "attempt", nil, true},
		{"missing observation time", "observedAt", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current, readErr := resource.Get(ctx, instance.GetName(), metav1.GetOptions{})
			if readErr != nil {
				t.Fatal(readErr)
			}
			invalid := provisionFailureSnapshot(t, current)
			if tc.omit {
				delete(invalid, tc.field)
			} else {
				invalid[tc.field] = tc.value
			}
			setProvisionFailureStatus(current, invalid)
			if _, updateErr := resource.UpdateStatus(ctx, current, metav1.UpdateOptions{}); !apierrors.IsInvalid(updateErr) {
				t.Fatalf("expected schema rejection, got %v", updateErr)
			}
			stored, readErr := resource.Get(ctx, instance.GetName(), metav1.GetOptions{})
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !reflect.DeepEqual(provisionFailureSnapshot(t, stored), failure) {
				t.Fatal("rejected checkpoint changed committed status")
			}
		})
	}
}

func setProvisionFailureStatus(instance *unstructured.Unstructured, failure map[string]any) {
	instance.Object["status"] = map[string]any{"jobs": []any{map[string]any{
		"task": "provision", "manager": "none", "attempt": int64(1), "state": "Retrying", "lastFailure": failure,
	}}}
}

func provisionFailureSnapshot(t *testing.T, instance *unstructured.Unstructured) map[string]any {
	t.Helper()
	jobs, found, err := unstructured.NestedSlice(instance.Object, "status", "jobs")
	if err != nil || !found || len(jobs) != 1 {
		t.Fatal("expected exactly one persisted job")
	}
	job, ok := jobs[0].(map[string]any)
	if !ok {
		t.Fatal("invalid persisted job shape")
	}
	failure, found, err := unstructured.NestedMap(job, "lastFailure")
	if err != nil || !found {
		t.Fatal("expected a persisted failure checkpoint")
	}
	return failure
}
