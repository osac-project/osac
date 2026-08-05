/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package securitygroup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/fulfillment-service/internal/masks"
)

// objectPrefix is the prefix that will be used in the `generateName` field of the resources created in the hub.
const objectPrefix = "securitygroup-"

// FunctionBuilder contains the data and logic needed to build a function that reconciles security groups.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                *slog.Logger
	hubCache              controllers.HubCache
	securityGroupsClient  privatev1.SecurityGroupsClient
	virtualNetworksClient privatev1.VirtualNetworksClient
	hubsClient            privatev1.HubsClient
	maskCalculator        *masks.Calculator
}

type task struct {
	r             *function
	securityGroup *privatev1.SecurityGroup
	hubId         string
	hubNamespace  string
	hubClient     clnt.Client
}

// NewFunction creates a new builder that can then be used to create a new security group reconciler function.
func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

// SetConnection sets the gRPC client connection. This is mandatory.
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

// SetHubCache sets the cache of hubs. This is mandatory.
func (b *FunctionBuilder) SetHubCache(value controllers.HubCache) *FunctionBuilder {
	b.hubCache = value
	return b
}

// Build uses the information stored in the builder to create a new security group reconciler.
func (b *FunctionBuilder) Build() (result controllers.ReconcilerFunction[*privatev1.SecurityGroup], err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.connection == nil {
		err = errors.New("client is mandatory")
		return
	}
	if b.hubCache == nil {
		err = errors.New("hub cache is mandatory")
		return
	}

	// Create and populate the object:
	object := &function{
		logger:                b.logger,
		securityGroupsClient:  privatev1.NewSecurityGroupsClient(b.connection),
		virtualNetworksClient: privatev1.NewVirtualNetworksClient(b.connection),
		hubsClient:            privatev1.NewHubsClient(b.connection),
		hubCache:              b.hubCache,
		maskCalculator:        masks.NewCalculator().Build(),
	}
	result = object.run
	return
}

func (r *function) run(ctx context.Context, securityGroup *privatev1.SecurityGroup) error {
	oldSecurityGroup := proto.Clone(securityGroup).(*privatev1.SecurityGroup)
	t := task{
		r:             r,
		securityGroup: securityGroup,
	}
	var err error
	if securityGroup.HasMetadata() && securityGroup.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	// Calculate which fields the reconciler actually modified and use a field mask
	// to update only those fields. This prevents overwriting concurrent user changes.
	updateMask := r.maskCalculator.Calculate(oldSecurityGroup, securityGroup)

	// Only send an update if there are actual changes
	_, err = r.securityGroupsClient.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
		Object:     securityGroup,
		UpdateMask: updateMask,
	}.Build())

	return err
}

func (t *task) update(ctx context.Context) error {
	// Add the finalizer and return immediately if it was added. This ensures the finalizer is persisted before any
	// other work is done, reducing the chance of the object being deleted before the finalizer is saved.
	if t.addFinalizer() {
		return nil
	}

	// Set the default values:
	t.setDefaults()

	// Validate that exactly one tenant is assigned:
	if err := t.validateTenant(); err != nil {
		return err
	}

	// Look up the parent VirtualNetwork to get the hub assignment:
	parentVN, err := t.getParentVirtualNetwork(ctx)
	if err != nil {
		return err
	}

	// Get the hub from the parent VirtualNetwork:
	t.hubId = parentVN.GetStatus().GetHub()
	if t.hubId == "" {
		return errors.New("parent virtual network does not have a hub assigned yet")
	}

	// Look up the hub client:
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client

	// Get the K8S object:
	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}

	// Prepare the changes to the spec:
	spec := t.buildSpec()

	// Create or update the Kubernetes object:
	if object == nil {
		object := &osacv1alpha1.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.hubNamespace,
				GenerateName: objectPrefix,
				Labels: map[string]string{
					labels.SecurityGroupUuid: t.securityGroup.GetId(),
				},
				Annotations: map[string]string{
					annotations.Tenant: t.securityGroup.GetMetadata().GetTenant(),
				},
			},
			Spec: spec,
		}
		err = t.hubClient.Create(ctx, object)
		if err != nil {
			return err
		}
		t.r.logger.DebugContext(
			ctx,
			"Created security group",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		update := object.DeepCopy()
		update.Spec = spec
		err = t.hubClient.Patch(ctx, update, clnt.MergeFrom(object))
		if err != nil {
			return err
		}
		t.r.logger.DebugContext(
			ctx,
			"Updated security group",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return err
}

func (t *task) setDefaults() {
	if !t.securityGroup.HasStatus() {
		t.securityGroup.SetStatus(&privatev1.SecurityGroupStatus{})
	}
	if t.securityGroup.GetStatus().GetState() == privatev1.SecurityGroupState_SECURITY_GROUP_STATE_UNSPECIFIED {
		t.securityGroup.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING)
	}
}

func (t *task) validateTenant() error {
	if !t.securityGroup.HasMetadata() || t.securityGroup.GetMetadata().GetTenant() == "" {
		return errors.New("security group must have a tenant assigned")
	}
	return nil
}

func (t *task) getParentVirtualNetwork(ctx context.Context) (*privatev1.VirtualNetwork, error) {
	vnID := t.securityGroup.GetSpec().GetVirtualNetwork()
	if vnID == "" {
		return nil, errors.New("security group must reference a parent virtual network")
	}
	response, err := t.r.virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
		Id: vnID,
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to get parent virtual network '%s': %w", vnID, err)
	}
	return response.GetObject(), nil
}

func (t *task) delete(ctx context.Context) (err error) {
	// Look up the parent VirtualNetwork to get the hub. If the parent is already deleted,
	// we can just remove the finalizer since there's nothing to clean up.
	parentVN, vnErr := t.getParentVirtualNetwork(ctx)
	if vnErr != nil {
		// Parent VN is gone — nothing to clean up on K8s side.
		t.r.logger.DebugContext(
			ctx,
			"Parent virtual network not found during delete, removing finalizer",
			slog.String("id", t.securityGroup.GetId()),
		)
		t.removeFinalizer()
		return nil
	}

	t.hubId = parentVN.GetStatus().GetHub()
	if t.hubId == "" {
		// No hub assigned, nothing to clean up on K8s side.
		t.removeFinalizer()
		return nil
	}

	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		// Check if the hub has been decommissioned (deleted from database)
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "security_group_id", t.securityGroup.GetId(), t.removeFinalizer)
			return nil
		}
		// For transient errors (network, timeout, etc.), continue retrying
		return
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client

	// Check if the K8S object still exists:
	object, err := t.getKubeObject(ctx)
	if err != nil {
		return
	}
	if object == nil {
		// K8s object is fully gone (all K8s finalizers processed).
		// Safe to remove our DB finalizer and allow archiving.
		t.r.logger.DebugContext(
			ctx,
			"Security group doesn't exist",
			slog.String("id", t.securityGroup.GetId()),
		)
		t.removeFinalizer()
		return
	}

	// Initiate K8s deletion if not already in progress:
	if object.GetDeletionTimestamp() == nil {
		err = t.hubClient.Delete(ctx, object)
		if err != nil {
			return
		}
		t.r.logger.DebugContext(
			ctx,
			"Deleted security group",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		t.r.logger.DebugContext(
			ctx,
			"Security group is still being deleted, waiting for K8s finalizers",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	// Don't remove finalizer — K8s object still exists with finalizers being processed.
	return
}

func (t *task) getKubeObject(ctx context.Context) (result *osacv1alpha1.SecurityGroup, err error) {
	list := &osacv1alpha1.SecurityGroupList{}
	err = t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.SecurityGroupUuid: t.securityGroup.GetId(),
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		err = fmt.Errorf(
			"expected at most one security group with identifier '%s' but found %d",
			t.securityGroup.GetId(), count,
		)
		return
	}
	if count > 0 {
		result = &items[0]
	}
	return
}

// addFinalizer adds the controller finalizer if it is not already present. Returns true if the finalizer was added,
// false if it was already present.
func (t *task) addFinalizer() bool {
	if !t.securityGroup.HasMetadata() {
		t.securityGroup.SetMetadata(&privatev1.Metadata{})
	}
	list := t.securityGroup.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.securityGroup.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.securityGroup.HasMetadata() {
		return
	}
	list := t.securityGroup.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.securityGroup.GetMetadata().SetFinalizers(list)
	}
}

// buildSpec constructs the spec for the Kubernetes SecurityGroup object based on the
// security group from the database.
func (t *task) buildSpec() osacv1alpha1.SecurityGroupSpec {
	spec := osacv1alpha1.SecurityGroupSpec{
		VirtualNetwork: t.securityGroup.GetSpec().GetVirtualNetwork(),
	}

	// Add implementation strategy if present:
	implStrategy := t.securityGroup.GetSpec().GetImplementationStrategy()
	if implStrategy != "" {
		spec.ImplementationStrategy = implStrategy
	}

	// Add ingress rules if present:
	ingressRules := t.securityGroup.GetSpec().GetIngress()
	if len(ingressRules) > 0 {
		spec.IngressRules = convertRules(ingressRules)
	}

	// Add egress rules if present:
	egressRules := t.securityGroup.GetSpec().GetEgress()
	if len(egressRules) > 0 {
		spec.EgressRules = convertRules(egressRules)
	}

	return spec
}

// convertRules converts a slice of proto SecurityRule messages to a slice of typed
// SecurityRule structs for the Kubernetes SecurityGroup object.
func convertRules(rules []*privatev1.SecurityRule) []osacv1alpha1.SecurityRule {
	result := make([]osacv1alpha1.SecurityRule, 0, len(rules))
	for _, rule := range rules {
		r := osacv1alpha1.SecurityRule{
			Protocol: osacv1alpha1.SecurityGroupProtocol(protocolToString(rule.GetProtocol())),
		}
		if rule.HasPortFrom() {
			portFrom := int32(rule.GetPortFrom())
			r.PortFrom = &portFrom
		}
		if rule.HasPortTo() {
			portTo := int32(rule.GetPortTo())
			r.PortTo = &portTo
		}
		if rule.HasIpv4Cidr() {
			r.SourceCIDR = rule.GetIpv4Cidr()
		}
		if rule.HasIpv6Cidr() {
			r.DestinationCIDR = rule.GetIpv6Cidr()
		}
		result = append(result, r)
	}
	return result
}

// protocolToString converts a Protocol enum value to a lowercase string matching the K8s CR enum.
func protocolToString(p privatev1.Protocol) string {
	switch p {
	case privatev1.Protocol_PROTOCOL_TCP:
		return "tcp"
	case privatev1.Protocol_PROTOCOL_UDP:
		return "udp"
	case privatev1.Protocol_PROTOCOL_ICMP:
		return "icmp"
	case privatev1.Protocol_PROTOCOL_ALL:
		return "all"
	default:
		return strings.ToLower(p.String())
	}
}
