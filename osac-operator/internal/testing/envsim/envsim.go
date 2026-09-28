/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package envsim provides a test-scoped "environment simulator" that substitutes for
// assisted-service, HyperShift, and CAPI in envtest. It lets bare-metal worker provisioning
// tests reproduce the asynchronous lifecycle the controller reacts to — InfraEnv ignition
// generation, Agent registration/unbinding, and NodePool/ClusterDeployment convergence —
// without any of those real systems running.
//
// The simulator exposes explicit, imperative "advance the world" helpers (MarkInfraEnvReady,
// RegisterAgent, UnbindAgent, ...) rather than autonomous controllers, so tests stay
// deterministic and readable: a test creates the CRs the controller would create, then calls
// the helper that models what the external system would do next, and asserts on the result.
//
// InfraEnv, Agent, and ClusterDeployment are driven as unstructured objects (no typed Go
// dependency on assisted-service or hive is required — matching the operator's existing Agent
// handling); NodePool is driven via the typed HyperShift API. The corresponding permissive
// stub CRDs live under config/crd/fakes and must be installed in the envtest environment.
//
// The simulator is reusable across test packages and is intended to be extended by the VM
// worker feature (OSAC-1589) as a shared regression baseline — add new "advance the world"
// helpers here rather than duplicating status-mutation logic in individual test files.
package envsim

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// AgentUnbindingState is the Agent debug-info state the simulator sets on unbind, matching what
// assisted-service reports once CAPA clears the binding and no reclaim is attempted (the
// assisted image service is disabled for CaaS).
const AgentUnbindingState = "unbinding-pending-user-action"

var (
	infraEnvGVK          = schema.GroupVersionKind{Group: "agent-install.openshift.io", Version: "v1beta1", Kind: "InfraEnv"}
	agentGVK             = schema.GroupVersionKind{Group: "agent-install.openshift.io", Version: "v1beta1", Kind: "Agent"}
	clusterDeploymentGVK = schema.GroupVersionKind{Group: "hive.openshift.io", Version: "v1", Kind: "ClusterDeployment"}
)

// Simulator drives the status of the external CRs a bare-metal worker controller reacts to.
// Construct it with New and call its helpers from tests to advance the simulated world.
type Simulator struct {
	c client.Client
}

// New returns a Simulator backed by the given client (typically the envtest client).
func New(c client.Client) *Simulator {
	return &Simulator{c: c}
}

// AgentOptions configures the Agent created by RegisterAgent.
type AgentOptions struct {
	// Name and Namespace identify the Agent CR to create.
	Name      string
	Namespace string
	// MAC is the inventory NIC MAC address the host registers with (used for BMI correlation).
	MAC string
	// ClusterDeploymentName, when set, binds the Agent to that ClusterDeployment (late binding).
	ClusterDeploymentName string
}

func unstructuredFor(gvk schema.GroupVersionKind, name, namespace string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(name)
	u.SetNamespace(namespace)
	return u
}

func (s *Simulator) getUnstructured(
	ctx context.Context, gvk schema.GroupVersionKind, name, namespace string,
) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	err := s.c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, u)
	return u, err
}

// MarkInfraEnvReady populates the InfraEnv's discovery ignition URL, modeling assisted-service
// generating the boot artifacts once the InfraEnv is created.
func (s *Simulator) MarkInfraEnvReady(ctx context.Context, name, namespace, ignitionURL string) error {
	u, err := s.getUnstructured(ctx, infraEnvGVK, name, namespace)
	if err != nil {
		return fmt.Errorf("getting infraenv %s/%s: %w", namespace, name, err)
	}
	if err := unstructured.SetNestedField(
		u.Object, ignitionURL, "status", "bootArtifacts", "discoveryIgnitionURL",
	); err != nil {
		return fmt.Errorf("setting discoveryIgnitionURL: %w", err)
	}
	if err := s.c.Update(ctx, u); err != nil {
		return fmt.Errorf("updating infraenv %s/%s status: %w", namespace, name, err)
	}
	return nil
}

// RegisterAgent creates an Agent CR with the configured inventory MAC, modeling a host booting
// from the discovery image and registering with assisted-service. When
// AgentOptions.ClusterDeploymentName is set, the Agent is created already bound to it.
func (s *Simulator) RegisterAgent(ctx context.Context, opts AgentOptions) error {
	agent := unstructuredFor(agentGVK, opts.Name, opts.Namespace)

	spec := map[string]interface{}{"approved": true}
	if opts.ClusterDeploymentName != "" {
		spec["clusterDeploymentName"] = map[string]interface{}{
			"name":      opts.ClusterDeploymentName,
			"namespace": opts.Namespace,
		}
	}
	if err := unstructured.SetNestedMap(agent.Object, spec, "spec"); err != nil {
		return fmt.Errorf("setting agent spec: %w", err)
	}

	interfaces := []interface{}{map[string]interface{}{"macAddress": opts.MAC}}
	if err := unstructured.SetNestedSlice(agent.Object, interfaces, "status", "inventory", "interfaces"); err != nil {
		return fmt.Errorf("setting agent inventory: %w", err)
	}

	if err := s.c.Create(ctx, agent); err != nil {
		return fmt.Errorf("creating agent %s/%s: %w", opts.Namespace, opts.Name, err)
	}
	return nil
}

// UnbindAgent clears the Agent's clusterDeploymentName and moves it to
// unbinding-pending-user-action, modeling CAPA clearing the binding on scale-down with the
// assisted image service disabled (no reclaim).
func (s *Simulator) UnbindAgent(ctx context.Context, name, namespace string) error {
	u, err := s.getUnstructured(ctx, agentGVK, name, namespace)
	if err != nil {
		return fmt.Errorf("getting agent %s/%s: %w", namespace, name, err)
	}
	unstructured.RemoveNestedField(u.Object, "spec", "clusterDeploymentName")
	if err := unstructured.SetNestedField(u.Object, AgentUnbindingState, "status", "debugInfo", "state"); err != nil {
		return fmt.Errorf("setting agent unbinding state: %w", err)
	}
	if err := s.c.Update(ctx, u); err != nil {
		return fmt.Errorf("updating agent %s/%s: %w", namespace, name, err)
	}
	return nil
}

// EnsureClusterDeployment creates the ClusterDeployment if it does not already exist, modeling
// the HyperShift-managed ClusterDeployment the controller waits for before creating an InfraEnv
// (design §Provisioning Flow: the InfraEnv is created only after the ClusterDeployment exists).
// It is idempotent.
//
// This models ClusterDeployment *existence* only — the controller gates InfraEnv creation on the
// object's presence, not on its status. A status-driving helper is intentionally deferred until a
// concrete consumer (the OSAC-4152 reconciler, or OSAC-1589) defines which ClusterDeployment
// status field it reads, so the simulator never asserts a status shape nothing consumes.
// NodePool status, which the convergence check does read, is driven by SetNodePoolReplicas.
func (s *Simulator) EnsureClusterDeployment(ctx context.Context, name, namespace string) error {
	_, err := s.getUnstructured(ctx, clusterDeploymentGVK, name, namespace)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting clusterdeployment %s/%s: %w", namespace, name, err)
	}
	cd := unstructuredFor(clusterDeploymentGVK, name, namespace)
	if err := unstructured.SetNestedField(cd.Object, false, "spec", "installed"); err != nil {
		return fmt.Errorf("setting clusterdeployment spec: %w", err)
	}
	if err := s.c.Create(ctx, cd); err != nil {
		return fmt.Errorf("creating clusterdeployment %s/%s: %w", namespace, name, err)
	}
	return nil
}

// SetNodePoolReplicas sets the NodePool's observed replica count, modeling HyperShift scaling
// the pool as agents join, so the controller's convergence check can observe it.
func (s *Simulator) SetNodePoolReplicas(ctx context.Context, name, namespace string, replicas int32) error {
	np := &hypershiftv1beta1.NodePool{}
	if err := s.c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, np); err != nil {
		return fmt.Errorf("getting nodepool %s/%s: %w", namespace, name, err)
	}
	np.Status.Replicas = replicas
	if err := s.c.Status().Update(ctx, np); err != nil {
		return fmt.Errorf("updating nodepool %s/%s status: %w", namespace, name, err)
	}
	return nil
}
