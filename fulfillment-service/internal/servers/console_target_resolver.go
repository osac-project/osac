/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/console"
	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
)

// lookupResult contains the database-sourced state needed to resolve a console target.
type lookupResult struct {
	ciInfo       *ConsoleComputeInstanceInfo
	kubeconfig   []byte
	hubNamespace string
}

// HubClientFactory creates a Kubernetes client from a parsed REST config.
type HubClientFactory func(config *rest.Config) (clnt.Client, error)

// NewDefaultHubClientFactory creates a HubClientFactory that uses the given scheme
// to create Kubernetes clients from a parsed REST config.
func NewDefaultHubClientFactory(scheme *runtime.Scheme) HubClientFactory {
	return func(config *rest.Config) (clnt.Client, error) {
		return clnt.New(config, clnt.Options{Scheme: scheme})
	}
}

// ComputeInstanceLookup provides compute instance data for console resolution.
// Implementations are pure readers that consume a tx-bound context.
type ComputeInstanceLookup interface {
	GetForConsole(ctx context.Context, id string) (*ConsoleComputeInstanceInfo, error)
}

// ConsoleComputeInstanceInfo is the subset of compute instance state needed by
// the resolver: running status and hub assignment.
type ConsoleComputeInstanceInfo struct {
	State privatev1.ComputeInstanceState
	HubID string
}

// HubLookup provides hub cluster access for console resolution.
// Implementations are pure readers that consume a tx-bound context.
type HubLookup interface {
	GetKubeconfig(ctx context.Context, hubID string) (kubeconfig []byte, namespace string, err error)
}

// ConsoleTargetResolverBuilder contains the data and logic needed to create a console target resolver. Don't create
// instances of this type directly, use the NewConsoleTargetResolver function instead.
type ConsoleTargetResolverBuilder struct {
	logger           *slog.Logger
	ciLookup         ComputeInstanceLookup
	hubLookup        HubLookup
	hubClientFactory HubClientFactory
}

// ConsoleTargetResolver resolves a compute instance ID to hub cluster data needed for
// backend target construction. It handles DB lookups and K8s CR validation; the caller
// (SessionService) uses the result to build the KubeVirt target and seal the ticket.
type ConsoleTargetResolver struct {
	logger           *slog.Logger
	ciLookup         ComputeInstanceLookup
	hubLookup        HubLookup
	hubClientFactory HubClientFactory
}

// NewConsoleTargetResolver creates a builder that can then be used to configure and create a new console target
// resolver.
func NewConsoleTargetResolver() *ConsoleTargetResolverBuilder {
	return &ConsoleTargetResolverBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *ConsoleTargetResolverBuilder) SetLogger(value *slog.Logger) *ConsoleTargetResolverBuilder {
	b.logger = value
	return b
}

// SetComputeInstanceLookup sets the compute instance lookup. This is mandatory.
func (b *ConsoleTargetResolverBuilder) SetComputeInstanceLookup(value ComputeInstanceLookup) *ConsoleTargetResolverBuilder {
	b.ciLookup = value
	return b
}

// SetHubLookup sets the hub lookup. This is mandatory.
func (b *ConsoleTargetResolverBuilder) SetHubLookup(value HubLookup) *ConsoleTargetResolverBuilder {
	b.hubLookup = value
	return b
}

// SetHubClientFactory sets the hub client factory. This is mandatory.
func (b *ConsoleTargetResolverBuilder) SetHubClientFactory(value HubClientFactory) *ConsoleTargetResolverBuilder {
	b.hubClientFactory = value
	return b
}

// Build uses the data stored in the builder to create and configure a new console target resolver.
func (b *ConsoleTargetResolverBuilder) Build() (*ConsoleTargetResolver, error) {
	// Check parameters:
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.ciLookup == nil {
		return nil, errors.New("compute instance lookup is mandatory")
	}
	if b.hubLookup == nil {
		return nil, errors.New("hub lookup is mandatory")
	}
	if b.hubClientFactory == nil {
		return nil, errors.New("hub client factory is mandatory")
	}

	// Create and populate the object:
	return &ConsoleTargetResolver{
		logger:           b.logger,
		ciLookup:         b.ciLookup,
		hubLookup:        b.hubLookup,
		hubClientFactory: b.hubClientFactory,
	}, nil
}

// ResolveComputeInstance resolves a compute instance ID to the hub cluster data needed for
// backend target construction. It verifies the instance is running and has a CR on the hub.
//
// Resolution is split into two phases:
//
//  1. lookupDBState — reads compute instance state and hub kubeconfig inside a scoped
//     transaction (via WithNewTx), then releases the DB connection.
//  2. findCROnHub — queries the hub Kubernetes API for the ComputeInstance CR.
//     No DB connection is held during this phase.
func (r *ConsoleTargetResolver) ResolveComputeInstance(ctx context.Context, resourceID string) (*console.ResolveResult, error) {
	// Phase 1: DB reads inside a scoped transaction — released before Kubernetes API calls.
	state, err := database.WithNewTx(ctx, func(txCtx context.Context) (*lookupResult, error) {
		return r.lookupDBState(txCtx, resourceID)
	})
	if err != nil {
		// lookupDBState already wraps domain errors with gRPC status codes.
		// WithNewTx infrastructure errors (missing manager, begin/end failure) are plain
		// errors that need mapping to codes.Internal to preserve the RPC error contract.
		if _, ok := status.FromError(err); !ok {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		return nil, err
	}

	// Phase 2: Kubernetes API call — no DB connection held.
	config, err := clientcmd.RESTConfigFromKubeConfig(state.kubeconfig)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to parse hub kubeconfig: %v", err)
	}

	namespace, crName, err := r.findCROnHub(ctx, config, state.hubNamespace, state.ciInfo.HubID, resourceID)
	if err != nil {
		return nil, err
	}

	return &console.ResolveResult{
		HubConfig: config,
		Namespace: namespace,
		CRName:    crName,
	}, nil
}

// lookupDBState validates the compute instance is running, has a hub assigned, and retrieves the
// hub kubeconfig and namespace. The caller is responsible for transaction scoping.
func (r *ConsoleTargetResolver) lookupDBState(ctx context.Context, resourceID string) (*lookupResult, error) {
	// Look up the compute instance to check its state and get the hub assignment.
	ciInfo, err := r.ciLookup.GetForConsole(ctx, resourceID)
	if err != nil {
		// Preserve the original gRPC status code if available (e.g., Internal for DB
		// errors, Unavailable for transient failures) so clients can retry appropriately.
		if st, ok := status.FromError(err); ok {
			return nil, status.Errorf(st.Code(), "failed to get compute instance %q: %v", resourceID, st.Message())
		}
		return nil, status.Errorf(codes.Internal, "failed to get compute instance %q: %v", resourceID, err)
	}

	// Verify running state.
	if ciInfo.State != privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING {
		return nil, status.Errorf(codes.FailedPrecondition,
			"compute instance %q is not running (state: %s)", resourceID, ciInfo.State.String())
	}

	// Verify hub assignment.
	if ciInfo.HubID == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"compute instance %q has no hub assigned", resourceID)
	}

	// Read the hub kubeconfig and namespace.
	kubeconfig, hubNamespace, err := r.hubLookup.GetKubeconfig(ctx, ciInfo.HubID)
	if err != nil {
		if st, ok := status.FromError(err); ok {
			return nil, status.Errorf(st.Code(), "failed to get hub %q: %v", ciInfo.HubID, st.Message())
		}
		return nil, status.Errorf(codes.Internal, "failed to get hub %q: %v", ciInfo.HubID, err)
	}
	if hubNamespace == "" {
		return nil, status.Errorf(codes.Internal, "hub %q returned empty namespace", ciInfo.HubID)
	}

	return &lookupResult{
		ciInfo:       ciInfo,
		kubeconfig:   kubeconfig,
		hubNamespace: hubNamespace,
	}, nil
}

// findCROnHub queries the hub Kubernetes API for the ComputeInstance CR matching the given
// instance ID, and returns its namespace and name. The rest.Config must already be parsed
// from the hub kubeconfig.
func (r *ConsoleTargetResolver) findCROnHub(ctx context.Context, config *rest.Config, hubNamespace, hubID, instanceID string) (namespace, crName string, err error) {
	// Create a Kubernetes client for the hub cluster.
	hubClient, err := r.hubClientFactory(config)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to create client for hub %q: %v", hubID, err)
		return
	}

	// Query for the ComputeInstance CR by UUID label.
	list := &osacv1alpha1.ComputeInstanceList{}
	err = hubClient.List(
		ctx, list,
		clnt.InNamespace(hubNamespace),
		clnt.MatchingLabels{
			labels.ComputeInstanceUuid: instanceID,
		},
	)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to list compute instances on hub %q: %v", hubID, err)
		return
	}

	items := list.Items
	if len(items) == 0 {
		r.logger.WarnContext(ctx, "Running compute instance not found on hub",
			slog.String("instance_id", instanceID),
			slog.String("hub_id", hubID),
		)
		err = status.Errorf(codes.FailedPrecondition,
			"compute instance %q not found on hub %q; it may still be provisioning", instanceID, hubID)
		return
	}
	if len(items) > 1 {
		err = status.Errorf(codes.Internal,
			"expected one compute instance with ID %q on hub %q but found %d", instanceID, hubID, len(items))
		return
	}

	obj := items[0]
	if obj.Status.Phase != osacv1alpha1.ComputeInstancePhaseRunning {
		phase := string(obj.Status.Phase)
		r.logger.WarnContext(ctx, "Compute instance is not running on hub",
			slog.String("instance_id", instanceID),
			slog.String("hub_id", hubID),
			slog.String("cr_name", obj.GetName()),
			slog.String("phase", phase),
		)
		msg := fmt.Sprintf(
			"compute instance %q is not running on hub %q (phase: %s)",
			instanceID, hubID, phase)
		if obj.Status.Phase == osacv1alpha1.ComputeInstancePhaseStarting {
			msg += "; it may still be provisioning"
		}
		err = status.Errorf(codes.FailedPrecondition, "%s", msg)
		return
	}
	return obj.GetNamespace(), obj.GetName(), nil
}

// privateServerCILookup wraps the private ComputeInstancesServer to implement ComputeInstanceLookup.
// It is a pure reader -- the caller provides a tx-bound context.
type privateServerCILookup struct {
	ciServer privatev1.ComputeInstancesServer
}

// NewPrivateServerCILookup creates a ComputeInstanceLookup backed by the private ComputeInstances server.
func NewPrivateServerCILookup(ciServer privatev1.ComputeInstancesServer) ComputeInstanceLookup {
	return &privateServerCILookup{ciServer: ciServer}
}

func (l *privateServerCILookup) GetForConsole(ctx context.Context, id string) (*ConsoleComputeInstanceInfo, error) {
	resp, err := l.ciServer.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{
		Id: id,
	}.Build())
	if err != nil {
		return nil, err
	}
	ci := resp.GetObject()
	ciStatus := ci.GetStatus()

	return &ConsoleComputeInstanceInfo{
		State: ciStatus.GetState(),
		HubID: ciStatus.GetHub(),
	}, nil
}

// privateServerHubLookup wraps the private HubsServer to implement HubLookup.
// It is a pure reader -- the caller provides a tx-bound context.
type privateServerHubLookup struct {
	hubServer privatev1.HubsServer
}

// NewPrivateServerHubLookup creates a HubLookup backed by the private Hubs server.
func NewPrivateServerHubLookup(hubServer privatev1.HubsServer) HubLookup {
	return &privateServerHubLookup{hubServer: hubServer}
}

func (l *privateServerHubLookup) GetKubeconfig(ctx context.Context, hubID string) (kubeconfig []byte, namespace string, err error) {
	hubResp, err := l.hubServer.Get(ctx, privatev1.HubsGetRequest_builder{
		Id: hubID,
	}.Build())
	if err != nil {
		return nil, "", err
	}
	hub := hubResp.GetObject()
	return hub.GetSpec().GetKubeconfig(), hub.GetSpec().GetNamespace(), nil
}
