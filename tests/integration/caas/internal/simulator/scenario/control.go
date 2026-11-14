// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Profile chooses provider responses for one test case.
type Profile string

const (
	settingsName      = "caas-it-scenarios"
	infraEnvRoot      = "InfraEnv"
	hostedClusterRoot = "HostedCluster"
	clusterRoot       = "Cluster"
)

const (
	Normal              Profile = "normal"
	InstallationFailed  Profile = "installation-failed"
	InstallationStalled Profile = "installation-stalled"
	DetachStalled       Profile = "detach-stalled"
	ControlPlaneStalled Profile = "control-plane-stalled"
)

// caseSettings holds response inputs and the original native roots for one case.
type caseSettings struct {
	ID           types.UID            `json:"id"`
	Profile      Profile              `json:"profile"`
	DiscoveryURL string               `json:"discoveryURL"`
	Roots        map[string]types.UID `json:"roots,omitempty"`
}

// validateProfile returns an error for any name outside the five supported profiles.
// Callers check the name before storing or applying response settings.
func validateProfile(profile Profile) error {
	switch profile {
	case Normal, InstallationFailed, InstallationStalled, DetachStalled, ControlPlaneStalled:
		return nil
	}

	return fmt.Errorf("unknown provider profile %q", profile)
}

// readSettings decodes the shared response inputs; malformed or terminating settings fail closed.
func readSettings(ctx context.Context, reader client.Reader, namespace string) (
	*corev1.ConfigMap, map[string]caseSettings, error,
) {
	settings := &corev1.ConfigMap{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: settingsName}, settings); err != nil {
		return nil, nil, err
	}

	if settings.DeletionTimestamp != nil {
		return nil, nil, fmt.Errorf("case settings are terminating")
	}

	cases := make(map[string]caseSettings, len(settings.Data))
	for name, data := range settings.Data {
		var entry caseSettings

		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			return nil, nil, fmt.Errorf("decode case settings %s: %w", name, err)
		}

		if entry.ID == "" {
			return nil, nil, fmt.Errorf("case %s has no settings identity", name)
		}

		cases[name] = entry
	}

	return settings, cases, nil
}

// setCase encodes one entry while preserving other cases and ConfigMap metadata.
func setCase(settings *corev1.ConfigMap, name string, entry caseSettings) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	if settings.Data == nil {
		settings.Data = map[string]string{}
	}

	settings.Data[name] = string(data)

	return nil
}

// Scenario owns one case's desired responses; provider resources hold all progress.
type Scenario struct {
	Client    client.Client
	Namespace string
	Name      string
	uid       types.UID
	caseID    types.UID
}

// read validates the original store and case identities before changing shared settings.
func (environment *Scenario) read(ctx context.Context) (*corev1.ConfigMap, map[string]caseSettings, error) {
	settings, cases, err := readSettings(ctx, environment.Client, environment.Namespace)
	if err != nil {
		return nil, nil, err
	}

	if settings.UID != environment.uid || cases[environment.Name].ID != environment.caseID {
		return nil, nil, fmt.Errorf("case settings were replaced")
	}

	return settings, cases, nil
}

// SetProfile changes only this case's live response selection.
// Reconcilers apply the selection on their next pass; product assertions await progress.
func (environment *Scenario) SetProfile(ctx context.Context, profile Profile) error {
	if err := validateProfile(profile); err != nil {
		return err
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		settings, cases, err := environment.read(ctx)
		if err != nil {
			return err
		}

		entry := cases[environment.Name]
		if entry.Profile == profile {
			return nil
		}

		entry.Profile = profile
		if err := setCase(settings, environment.Name, entry); err != nil {
			return err
		}

		return environment.Client.Update(ctx, settings)
	}); err != nil {
		return err
	}

	slog.InfoContext(ctx, "simulator profile configured",
		"namespace", environment.Namespace,
		"settings", environment.Name,
		"profile", profile,
	)

	return nil
}

// Create adds a case to the shared ConfigMap and retains both store and case identities.
// The caller must create these settings before submitting the cluster request.
func (environment *Scenario) Create(ctx context.Context, profile Profile, discoveryURL string) error {
	if err := validateProfile(profile); err != nil {
		return err
	}

	endpoint, err := url.Parse(discoveryURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return fmt.Errorf("discovery ignition URL must be an absolute HTTP URL")
	}

	entry := caseSettings{ID: uuid.NewUUID(), Profile: profile, DiscoveryURL: discoveryURL}

	return retry.OnError(retry.DefaultRetry, func(err error) bool {
		// Another case can create the store or retire it before this write.
		return apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) || apierrors.IsNotFound(err)
	}, func() error {
		settings, cases, err := readSettings(ctx, environment.Client, environment.Namespace)
		missing := apierrors.IsNotFound(err)
		if missing {
			settings = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: environment.Namespace, Name: settingsName}}
		} else if err != nil {
			return err
		}

		if _, exists := cases[environment.Name]; exists {
			return fmt.Errorf("case settings %s already exist", environment.Name)
		}

		if err := setCase(settings, environment.Name, entry); err != nil {
			return err
		}

		if missing {
			err = environment.Client.Create(ctx, settings)
		} else {
			err = environment.Client.Update(ctx, settings)
		}

		if err == nil {
			environment.uid, environment.caseID = settings.UID, entry.ID
		}

		return err
	})
}

// BindInfraEnv binds this case exclusively to its original InfraEnv UID.
func (environment *Scenario) BindInfraEnv(ctx context.Context, uid types.UID) error {
	return environment.bind(ctx, infraEnvRoot, uid)
}

// BindHostedCluster binds this case exclusively to its original HostedCluster UID.
func (environment *Scenario) BindHostedCluster(ctx context.Context, uid types.UID) error {
	return environment.bind(ctx, hostedClusterRoot, uid)
}

// BindCluster binds this case exclusively to its original CAPI Cluster UID.
func (environment *Scenario) BindCluster(ctx context.Context, uid types.UID) error {
	return environment.bind(ctx, clusterRoot, uid)
}

// bind checks every case and records the root in the same resource-version-protected write.
func (environment *Scenario) bind(ctx context.Context, kind string, uid types.UID) error {
	if uid == "" {
		return fmt.Errorf("native root UID is required")
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		settings, cases, err := environment.read(ctx)
		if err != nil {
			return err
		}

		entry := cases[environment.Name]
		if current := entry.Roots[kind]; current != "" && current != uid {
			return fmt.Errorf("case root incarnation changed")
		}

		for name, other := range cases {
			if name != environment.Name && other.Roots[kind] == uid {
				return fmt.Errorf("%s %s is bound to another case", kind, uid)
			}
		}

		if entry.Roots[kind] == uid {
			return nil
		}

		if entry.Roots == nil {
			entry.Roots = map[string]types.UID{}
		}

		entry.Roots[kind] = uid
		if err := setCase(settings, environment.Name, entry); err != nil {
			return err
		}

		return environment.Client.Update(ctx, settings)
	})
}

// Delete retires the original case after resource cleanup and terminal automation.
// Removing the last case deletes the store with UID and resource-version preconditions.
func (environment *Scenario) Delete(ctx context.Context) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		settings, cases, err := environment.read(ctx)
		if err != nil {
			return err
		}

		if len(cases) == 1 {
			return environment.Client.Delete(ctx, settings, client.Preconditions{
				UID: &environment.uid, ResourceVersion: &settings.ResourceVersion,
			})
		}

		delete(settings.Data, environment.Name)

		return environment.Client.Update(ctx, settings)
	})
}
