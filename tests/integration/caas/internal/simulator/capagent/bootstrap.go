// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ignitiontypes "github.com/coreos/ignition/v2/config/v3_2/types"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// bootstrap reads the Machine's bootstrap Secret and its single Ignition merge source.
// It returns the configuration pool name, token Secret reference and other HTTP headers.
// Each source maintains a shared unowned token Secret in the Machine namespace,
// including empty tokens for unauthenticated sources. An unset bootstrap reference
// returns empty values. Unreadable or invalid Secrets return an error.
func (r *AgentMachineReconciler) bootstrap(
	ctx context.Context,
	machine *capiv1.Machine,
) (string, *assistedv1.IgnitionEndpointTokenReference, map[string]string, error) {
	if machine.Spec.Bootstrap.DataSecretName == nil || *machine.Spec.Bootstrap.DataSecretName == "" {
		return "", nil, nil, nil
	}

	name := *machine.Spec.Bootstrap.DataSecretName
	secret := &corev1.Secret{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: machine.Namespace, Name: name}, secret); err != nil {
		return "", nil, nil, err
	}

	var config ignitiontypes.Config

	if err := json.Unmarshal(secret.Data["value"], &config); err != nil {
		return "", nil, nil, fmt.Errorf("invalid Machine bootstrap ignition")
	}

	if len(config.Ignition.Config.Merge) != 1 {
		return "", nil, nil, fmt.Errorf("machine bootstrap ignition requires one source")
	}

	// Read the worker configuration pool from the URL and separate the authorization header.
	source := config.Ignition.Config.Merge[0]
	if source.Source == nil {
		return "", nil, nil, fmt.Errorf("bootstrap ignition source is required")
	}

	pool := (*source.Source)[strings.LastIndex(*source.Source, "/")+1:]
	headers := map[string]string{}
	token := ""

	for _, h := range source.HTTPHeaders {
		if h.Value == nil {
			return "", nil, nil, fmt.Errorf("bootstrap HTTP header value is required")
		}

		if h.Name == "Authorization" {
			if !strings.HasPrefix(*h.Value, "Bearer ") {
				return "", nil, nil, fmt.Errorf("invalid bootstrap authorization header")
			}

			token = strings.TrimPrefix(*h.Value, "Bearer ")
		} else {
			headers[h.Name] = *h.Value
		}
	}

	// Share the token for this bootstrap input across its workers.
	out, err := r.ensureToken(ctx, secret, token)
	if err != nil {
		return "", nil, nil, err
	}

	ctrl.LoggerFrom(ctx).V(4).Info("prepared Agent bootstrap reference",
		"machine", machine.GetName(),
		"machineUID", machine.GetUID(),
		"secret", out.GetName(),
		"namespace", out.GetNamespace(),
	)

	return pool, &assistedv1.IgnitionEndpointTokenReference{Namespace: out.Namespace, Name: out.Name}, headers, nil
}

// ensureToken creates an unowned shared token Secret and refreshes its credentials.
// Existing metadata and other data keys are preserved through optimistic patches.
func (r *AgentMachineReconciler) ensureToken(
	ctx context.Context, source *corev1.Secret, token string,
) (*corev1.Secret, error) {
	out := &corev1.Secret{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: source.Namespace, Name: "agent-" + source.Name}, out)
	create := apierrors.IsNotFound(err)
	if err != nil && !create {
		return nil, err
	}

	if create {
		out = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: source.Namespace, Name: "agent-" + source.Name}}
	}

	if out.DeletionTimestamp != nil {
		return nil, fmt.Errorf("bootstrap token Secret is terminating")
	}

	before := out.DeepCopy()
	metav1.SetMetaDataLabel(&out.ObjectMeta, kube.ManagedByLabel, kube.ManagerName)

	if out.Data == nil {
		out.Data = map[string][]byte{}
	}

	out.Data["ignition-token"] = []byte(token)
	if create {
		return out, r.Client.Create(ctx, out)
	}

	if equality.Semantic.DeepEqual(before.Data, out.Data) && equality.Semantic.DeepEqual(before.Labels, out.Labels) {
		return out, nil
	}

	return out, r.Client.Patch(ctx, out,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}
