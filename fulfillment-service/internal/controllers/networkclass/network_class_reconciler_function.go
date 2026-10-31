/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package networkclass

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var (
	errNoLogger                    = errors.New("logger is mandatory")
	errNoConnection                = errors.New("client connection is mandatory")
	errNoHubCache                  = errors.New("hub cache is mandatory")
	canonicalHubStatusMessage      = "status.hub"
	canonicalHubStatusStateMessage = "status.state"
	canonicalHubStatusMessageField = "status.message"
)

// FunctionBuilder contains the dependencies needed to build a NetworkClass reconciler.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
	resolver   controllers.NetworkClassHubResolver
	client     controllers.NetworkClassStatusClient
}

// SetLogger sets the logger used by the reconciler.
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

// NewFunction creates a builder for a NetworkClass reconciler function.
func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

// SetConnection sets the fulfillment-service gRPC connection.
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

// SetHubCache sets the Hub client cache.
func (b *FunctionBuilder) SetHubCache(value controllers.HubCache) *FunctionBuilder {
	b.hubCache = value
	return b
}

// SetResolver injects the resolver used to reconcile canonical Hub status.
func (b *FunctionBuilder) SetResolver(value controllers.NetworkClassHubResolver) *FunctionBuilder {
	b.resolver = value
	return b
}

// SetNetworkClassesClient sets the status client used by the reconciler.
func (b *FunctionBuilder) SetNetworkClassesClient(value controllers.NetworkClassStatusClient) *FunctionBuilder {
	b.client = value
	return b
}

// Build creates a NetworkClass reconciliation function.
func (b *FunctionBuilder) Build() (controllers.ReconcilerFunction[*privatev1.NetworkClass], error) {
	if b.logger == nil {
		return nil, errNoLogger
	}
	resolver := b.resolver
	client := b.client
	if resolver == nil {
		if b.connection == nil {
			return nil, errNoConnection
		}
		if b.hubCache == nil {
			return nil, errNoHubCache
		}
		var err error
		client = privatev1.NewNetworkClassesClient(b.connection)
		resolver, err = controllers.NewNetworkingHubResolver().
			SetNetworkClassesClient(privatev1.NewNetworkClassesClient(b.connection)).
			SetHubsClient(privatev1.NewHubsClient(b.connection)).
			SetHubCache(b.hubCache).
			Build()
		if err != nil {
			return nil, err
		}
	}
	if client == nil {
		return nil, errors.New("network classes client is mandatory")
	}

	return (&function{resolver: resolver, client: client, hubCache: b.hubCache}).run, nil
}

type function struct {
	resolver controllers.NetworkClassHubResolver
	client   controllers.NetworkClassStatusClient
	hubCache controllers.HubCache
}

func (r *function) run(ctx context.Context, networkClass *privatev1.NetworkClass) error {
	if networkClass.HasMetadata() && networkClass.GetMetadata().HasDeletionTimestamp() {
		return r.delete(ctx, networkClass)
	}

	// Persist the finalizer before the CR exists, so the record is removed only after its CR.
	if !slices.Contains(networkClass.GetMetadata().GetFinalizers(), finalizers.Controller) {
		return r.setFinalizers(ctx, networkClass,
			append(slices.Clone(networkClass.GetMetadata().GetFinalizers()), finalizers.Controller))
	}

	resolution, resolveErr := r.resolver.Resolve(ctx)
	if resolution.State == privatev1.NetworkClassState_NETWORK_CLASS_STATE_UNSPECIFIED {
		return resolveErr
	}
	var crErr error
	if resolution.State == privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY && resolution.Client != nil {
		crErr = r.ensureCR(ctx, networkClass, resolution.Namespace, resolution.Client)
	}
	statusErr := r.updateStatus(ctx, networkClass, resolution)
	if statusErr != nil && resolveErr != nil {
		return fmt.Errorf("%w; failed to update network class status", errors.Join(resolveErr, statusErr))
	}
	if statusErr != nil {
		return fmt.Errorf("failed to update network class status: %w", statusErr)
	}
	if crErr != nil {
		return crErr
	}
	return resolveErr
}

// ensureCR creates the NetworkClass CR in the hub networking namespace, or updates the address plan of the existing
// one. The other fields don't change after installation.
func (r *function) ensureCR(ctx context.Context, networkClass *privatev1.NetworkClass, namespace string,
	client clnt.Client) error {
	spec, err := buildSpec(networkClass)
	if err != nil {
		return err
	}
	object, err := getCR(ctx, client, namespace, networkClass.GetId())
	if err != nil {
		return err
	}
	if object == nil {
		object = &osacv1alpha1.NetworkClass{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    namespace,
				GenerateName: "networkclass-",
				Labels:       map[string]string{labels.NetworkClassUuid: networkClass.GetId()},
			},
			Spec: spec,
		}
		if err := client.Create(ctx, object); err != nil {
			return fmt.Errorf("failed to create the NetworkClass CR: %w", err)
		}
		return nil
	}
	if object.Spec.AddressPlan == spec.AddressPlan {
		return nil
	}
	update := object.DeepCopy()
	update.Spec.AddressPlan = spec.AddressPlan
	if err := client.Patch(ctx, update, clnt.MergeFrom(object)); err != nil {
		return fmt.Errorf("failed to update the NetworkClass CR address plan: %w", err)
	}
	return nil
}

// delete deletes the NetworkClass CR and removes the finalizer once the CR is gone.
func (r *function) delete(ctx context.Context, networkClass *privatev1.NetworkClass) error {
	if !slices.Contains(networkClass.GetMetadata().GetFinalizers(), finalizers.Controller) {
		return nil
	}
	hubID := networkClass.GetStatus().GetHub()
	if hubID != "" && r.hubCache != nil {
		hub, err := r.hubCache.Get(ctx, hubID)
		if err != nil && !errors.Is(err, controllers.ErrHubNotFound) {
			return err
		}
		if err == nil {
			object, err := getCR(ctx, hub.Client, hub.Namespace, networkClass.GetId())
			if err != nil {
				return err
			}
			if object != nil {
				if object.GetDeletionTimestamp() == nil {
					if err := hub.Client.Delete(ctx, object); err != nil && !apierrors.IsNotFound(err) {
						return fmt.Errorf("failed to delete the NetworkClass CR: %w", err)
					}
				}
				return controllers.RequeueAfterKubernetesDeletion("network class")
			}
		}
	}
	return r.setFinalizers(ctx, networkClass, slices.DeleteFunc(slices.Clone(networkClass.GetMetadata().GetFinalizers()),
		func(value string) bool { return value == finalizers.Controller }))
}

func (r *function) setFinalizers(ctx context.Context, networkClass *privatev1.NetworkClass, values []string) error {
	object := proto.Clone(networkClass).(*privatev1.NetworkClass)
	if !object.HasMetadata() {
		object.SetMetadata(&privatev1.Metadata{})
	}
	object.GetMetadata().SetFinalizers(values)
	_, err := r.client.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
		Object:     object,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
	}.Build())
	return err
}

func getCR(ctx context.Context, client clnt.Client, namespace, id string) (*osacv1alpha1.NetworkClass, error) {
	list := &osacv1alpha1.NetworkClassList{}
	err := client.List(ctx, list, clnt.InNamespace(namespace), clnt.MatchingLabels{labels.NetworkClassUuid: id})
	if err != nil {
		return nil, fmt.Errorf("failed to list NetworkClass CRs: %w", err)
	}
	if len(list.Items) > 1 {
		return nil, fmt.Errorf("expected at most one NetworkClass CR with identifier '%s' but found %d", id,
			len(list.Items))
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	return &list.Items[0], nil
}

// buildSpec builds the NetworkClass CR spec from the record. The address plan shares become sixteenths.
func buildSpec(networkClass *privatev1.NetworkClass) (osacv1alpha1.NetworkClassSpec, error) {
	defaults := networkClass.GetSpec().GetDefaults()
	ingress, err := controllers.ConvertSecurityRules(defaults.GetIngressRules(), true)
	if err != nil {
		return osacv1alpha1.NetworkClassSpec{}, err
	}
	egress, err := controllers.ConvertSecurityRules(defaults.GetEgressRules(), false)
	if err != nil {
		return osacv1alpha1.NetworkClassSpec{}, err
	}
	spec := osacv1alpha1.NetworkClassSpec{
		FabricManager: networkClass.GetFabricManager(),
		K8sManager:    networkClass.GetK8SManager(),
		Defaults: osacv1alpha1.NetworkClassDefaults{
			VirtualNetworkIPv4CIDR: defaults.GetVirtualNetworkIpv4Cidr(),
			SubnetIPv4CIDR:         defaults.GetSubnetIpv4Cidr(),
			IngressRules:           ingress,
			EgressRules:            egress,
		},
		AddressPlan: controllers.AddressPlanSixteenths(networkClass.GetSpec().GetAddressPlan()),
	}
	if capabilities := networkClass.GetSpec().GetEastWestCapabilities(); capabilities != nil {
		spec.EastWestCapabilities = &osacv1alpha1.NetworkClassEastWestCapabilities{
			SupportsEastWestEthernet: capabilities.GetSupportsEastWestEthernet(),
		}
	}
	return spec, nil
}

func (r *function) updateStatus(
	ctx context.Context,
	networkClass *privatev1.NetworkClass,
	resolution controllers.NetworkingHubResolution,
) error {
	object := proto.Clone(networkClass).(*privatev1.NetworkClass)
	if !object.HasStatus() {
		object.SetStatus(&privatev1.NetworkClassStatus{})
	}
	status := object.GetStatus()
	// A resolution without a HubID means that no canonical Hub is currently
	// selectable (for example, when there are no Hubs or multiple active
	// Hubs). Clear any previously persisted assignment so status describes the
	// current resolution rather than a stale READY binding. Resolutions for an
	// explicitly configured canonical Hub always include its ID, including
	// FAILED and PENDING results, so those assignments remain visible.
	status.SetHub(resolution.HubID)
	// The operator owns the state once the hub is resolved: it reports READY or FAILED from the manager
	// registrations, and it resolves a READY NetworkClass only once (contract §4.2). This controller only reports
	// hub-resolution problems, and never downgrades READY: a problem on a READY NetworkClass goes to the message.
	ready := status.GetState() == privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY
	switch {
	case resolution.State != privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY:
		if !ready {
			status.SetState(resolution.State)
		}
		if resolution.Message == "" {
			status.ClearMessage()
		} else {
			status.SetMessage(resolution.Message)
		}
	case ready:
		status.ClearMessage()
	case status.GetState() == privatev1.NetworkClassState_NETWORK_CLASS_STATE_UNSPECIFIED:
		status.SetState(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING)
	}
	if networkClass.HasStatus() && proto.Equal(networkClass.GetStatus(), status) {
		return nil
	}

	_, err := r.client.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
		Object: object,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
			canonicalHubStatusMessage,
			canonicalHubStatusStateMessage,
			canonicalHubStatusMessageField,
		}},
		Lock: true,
	}.Build())
	return err
}
