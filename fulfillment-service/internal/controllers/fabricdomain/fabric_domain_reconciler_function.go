/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fabricdomain

//go:generate go run go.uber.org/mock/mockgen -destination=clients_mock.go -package=fabricdomain github.com/osac-project/osac/proto/gen/osac/private/v1 FabricDomainsClient,VirtualNetworksClient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const objectPrefix = "fabricdomain-"

// FunctionBuilder configures the fulfillment-to-hub FabricDomain reconciler.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                *slog.Logger
	hubCache              controllers.HubCache
	fabricDomainsClient   privatev1.FabricDomainsClient
	virtualNetworksClient privatev1.VirtualNetworksClient
	hubsClient            privatev1.HubsClient
	maskCalculator        *masks.Calculator
}

type task struct {
	r            *function
	fabricDomain *privatev1.FabricDomain
	hubNamespace string
	hubClient    clnt.Client
}

// NewFunction creates a builder for a FabricDomain reconciler function.
func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

// SetLogger sets the mandatory logger.
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

// SetConnection sets the mandatory private API connection.
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

// SetHubCache sets the mandatory cache of hub clients.
func (b *FunctionBuilder) SetHubCache(value controllers.HubCache) *FunctionBuilder {
	b.hubCache = value
	return b
}

// Build creates the reconciler function.
func (b *FunctionBuilder) Build() (controllers.ReconcilerFunction[*privatev1.FabricDomain], error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.connection == nil {
		return nil, errors.New("client is mandatory")
	}
	if b.hubCache == nil {
		return nil, errors.New("hub cache is mandatory")
	}
	r := &function{
		logger:                b.logger,
		hubCache:              b.hubCache,
		fabricDomainsClient:   privatev1.NewFabricDomainsClient(b.connection),
		virtualNetworksClient: privatev1.NewVirtualNetworksClient(b.connection),
		hubsClient:            privatev1.NewHubsClient(b.connection),
		maskCalculator:        masks.NewCalculator().Build(),
	}
	return r.run, nil
}

func (r *function) run(ctx context.Context, domain *privatev1.FabricDomain) error {
	if domain.GetMetadata().GetTenant() == "" {
		return errors.New("fabric domain must have a tenant assigned")
	}
	before := proto.Clone(domain).(*privatev1.FabricDomain)
	t := task{r: r, fabricDomain: domain}
	var err error
	if domain.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	mask := r.maskCalculator.Calculate(before, domain)
	if len(mask.GetPaths()) == 0 {
		return nil
	}
	// Preserve concurrent spec/status changes, and never overwrite another finalizer update.
	_, err = r.fabricDomainsClient.Update(ctx, privatev1.FabricDomainsUpdateRequest_builder{
		Object: domain, UpdateMask: mask, Lock: true,
	}.Build())
	return err
}

func (t *task) update(ctx context.Context) error {
	// Persist the database finalizer before assigning a hub or creating a CR.
	if t.addFinalizer() {
		return nil
	}
	if !t.fabricDomain.HasStatus() {
		t.fabricDomain.SetStatus(&privatev1.FabricDomainStatus{})
	}
	if recovered, err := t.initHub(ctx); err != nil || recovered {
		return err
	}
	hubJustSelected := t.fabricDomain.GetStatus().GetHub() == ""
	if err := t.selectHub(ctx); err != nil {
		return err
	}
	// Persist placement before any hub writes, so deletion never needs a live VN to find the CR.
	if hubJustSelected {
		return nil
	}
	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}
	spec, err := t.buildSpec()
	if err != nil {
		t.setFailed(err)
		return nil
	}
	if object == nil {
		object = &osacv1alpha1.FabricDomain{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: t.hubNamespace, GenerateName: objectPrefix,
				Labels:      map[string]string{labels.FabricDomainUuid: t.fabricDomain.GetId()},
				Annotations: t.desiredAnnotations(),
			},
			Spec: spec,
		}
		err = t.hubClient.Create(ctx, object)
	} else {
		// An operator may still be deprovisioning an externally deleted CR. Never recreate it early.
		if !object.DeletionTimestamp.IsZero() {
			return fmt.Errorf("fabric domain CR %q is still being deleted", object.Name)
		}
		updated := object.DeepCopy()
		updated.Spec = spec
		if updated.Annotations == nil {
			updated.Annotations = map[string]string{}
		}
		maps.Copy(updated.Annotations, t.desiredAnnotations())
		if equality.Semantic.DeepEqual(object, updated) {
			return nil
		}
		err = t.hubClient.Patch(ctx, updated, clnt.MergeFrom(object))
	}
	return controllers.HandleK8sWriteError(ctx, t.r.logger, err, t.setFailed)
}

func (t *task) selectHub(ctx context.Context) error {
	vnID := t.fabricDomain.GetSpec().GetVirtualNetwork()
	if vnID == "" {
		return errors.New("fabric domain must reference a virtual network")
	}
	response, err := t.r.virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnID}.Build())
	if err != nil {
		return err
	}
	vn := response.GetObject()
	if vn == nil || vn.GetMetadata().HasDeletionTimestamp() {
		return errors.New("referenced virtual network is missing or deleting")
	}
	if vn.GetMetadata().GetTenant() != t.fabricDomain.GetMetadata().GetTenant() {
		return errors.New("fabric domain and virtual network must belong to the same tenant")
	}
	hubID := vn.GetStatus().GetHub()
	if hubID == "" {
		return errors.New("waiting for virtual network hub assignment")
	}
	if assigned := t.fabricDomain.GetStatus().GetHub(); assigned != "" && assigned != hubID {
		return errors.New("virtual network hub differs from the fabric domain's recorded hub")
	}
	if err := t.getHub(ctx, hubID); err != nil {
		return err
	}
	t.fabricDomain.GetStatus().SetHub(hubID)
	return nil
}

func (t *task) getHub(ctx context.Context, hubID string) error {
	entry, err := t.r.hubCache.Get(ctx, hubID)
	if err != nil {
		return err
	}
	if entry == nil || entry.Client == nil || entry.Namespace == "" {
		return errors.New("hub has no usable client or namespace")
	}
	t.hubNamespace, t.hubClient = entry.Namespace, entry.Client
	return nil
}

func (t *task) delete(ctx context.Context) error {
	if recovered, err := t.initHub(ctx); err != nil || recovered {
		// Persist recovered placement before requesting deletion on the hub.
		return err
	}
	hubID := t.fabricDomain.GetStatus().GetHub()
	if hubID == "" {
		// The complete registered-hub search found no matching CR.
		t.removeFinalizer()
		return nil
	}
	if err := t.getHub(ctx, hubID); err != nil {
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, hubID,
				"fabric_domain_id", t.fabricDomain.GetId(), t.removeFinalizer)
			return nil
		}
		return err
	}
	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}
	if object == nil {
		t.removeFinalizer()
		return nil
	}
	if object.DeletionTimestamp.IsZero() {
		return t.hubClient.Delete(ctx, object)
	}
	// The operator must finish provider cleanup and feedback before the API record is archived.
	return nil
}

// initHub recovers unrecorded placement before creation or deletion. Missing status alone
// does not prove that no CR exists (for example after a partial migration or manual repair).
// Search every registered hub, and make no changes if any lookup fails or matches are ambiguous.
func (t *task) initHub(ctx context.Context) (bool, error) {
	if t.fabricDomain.GetStatus().GetHub() != "" {
		return false, nil
	}
	hubs, err := t.listHubs(ctx)
	if err != nil {
		return false, err
	}
	var found string
	for _, hub := range hubs {
		if hub.GetId() == "" {
			return false, errors.New("hub search returned an empty hub identifier")
		}
		if err := t.getHub(ctx, hub.GetId()); err != nil {
			return false, err
		}
		object, err := t.getKubeObject(ctx)
		if err != nil {
			return false, err
		}
		if object == nil {
			continue
		}
		if found != "" {
			return false, errors.New("fabric domain CR exists on more than one hub")
		}
		found = hub.GetId()
	}
	if found == "" {
		return false, nil
	}
	if !t.fabricDomain.HasStatus() {
		t.fabricDomain.SetStatus(&privatev1.FabricDomainStatus{})
	}
	t.fabricDomain.GetStatus().SetHub(found)
	return true, nil
}

func (t *task) listHubs(ctx context.Context) ([]*privatev1.Hub, error) {
	const limit int32 = 100
	var result []*privatev1.Hub
	var offset int32
	for {
		response, err := t.r.hubsClient.List(ctx, privatev1.HubsListRequest_builder{
			Offset: &offset, Limit: proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("hub search returned an empty response")
		}
		items := response.GetItems()
		result = append(result, items...)
		if int64(len(result)) >= int64(response.GetTotal()) {
			return result, nil
		}
		if len(items) == 0 {
			return nil, errors.New("hub search returned an incomplete page")
		}
		if int64(response.GetSize()) != int64(len(items)) {
			return nil, errors.New("hub search returned an inconsistent page size")
		}
		offset += response.GetSize()
	}
}

func (t *task) getKubeObject(ctx context.Context) (*osacv1alpha1.FabricDomain, error) {
	list := &osacv1alpha1.FabricDomainList{}
	if err := t.hubClient.List(ctx, list, clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{labels.FabricDomainUuid: t.fabricDomain.GetId()}); err != nil {
		return nil, err
	}
	if len(list.Items) > 1 {
		return nil, fmt.Errorf("expected at most one fabric domain with identifier %q but found %d",
			t.fabricDomain.GetId(), len(list.Items))
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	object := &list.Items[0]
	if object.Annotations[annotations.Tenant] != t.fabricDomain.GetMetadata().GetTenant() {
		return nil, errors.New("fabric domain CR tenant does not match the API object")
	}
	return object, nil
}

func (t *task) desiredAnnotations() map[string]string {
	// Derive the owner annotation from the associated VN's API identifier, overriding
	// caller-supplied values just like the tenant annotation. This does not create
	// Kubernetes OwnerReferences; FabricDomain finalizers control cleanup.
	const ownerReferenceAnnotation = "osac.openshift.io/owner-reference"
	result := maps.Clone(t.fabricDomain.GetMetadata().GetAnnotations())
	if result == nil {
		result = map[string]string{}
	}
	result[annotations.Tenant] = t.fabricDomain.GetMetadata().GetTenant()
	result[ownerReferenceAnnotation] = t.fabricDomain.GetSpec().GetVirtualNetwork()
	return result
}

func (t *task) addFinalizer() bool {
	metadata := t.fabricDomain.GetMetadata()
	if slices.Contains(metadata.GetFinalizers(), finalizers.Controller) {
		return false
	}
	metadata.SetFinalizers(append(metadata.GetFinalizers(), finalizers.Controller))
	return true
}

func (t *task) removeFinalizer() {
	metadata := t.fabricDomain.GetMetadata()
	if metadata != nil {
		metadata.SetFinalizers(slices.DeleteFunc(metadata.GetFinalizers(), func(value string) bool {
			return value == finalizers.Controller
		}))
	}
}

func (t *task) setFailed(err error) {
	const reason = "InvalidFabricDomain"
	status := t.fabricDomain.GetStatus()
	for _, condition := range status.GetConditions() {
		if condition.GetType() == privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_FAILED &&
			condition.GetStatus() == privatev1.ConditionStatus_CONDITION_STATUS_TRUE &&
			condition.GetReason() == reason && condition.GetMessage() == err.Error() {
			return
		}
	}
	message := err.Error()
	status.SetConditions([]*privatev1.FabricDomainCondition{privatev1.FabricDomainCondition_builder{
		Type:   privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_FAILED,
		Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE, LastTransitionTime: timestamppb.Now(),
		Reason: proto.String(reason), Message: &message,
	}.Build()})
}

func (t *task) buildSpec() (osacv1alpha1.FabricDomainSpec, error) {
	spec := t.fabricDomain.GetSpec()
	if spec.GetType() != privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW {
		return osacv1alpha1.FabricDomainSpec{}, errors.New("only EthernetEW fabric domains are supported")
	}
	return osacv1alpha1.FabricDomainSpec{
		Type:    osacv1alpha1.FabricDomainTypeEthernetEW,
		Servers: slices.Clone(spec.GetServers()), VirtualNetwork: spec.GetVirtualNetwork(),
	}, nil
}
