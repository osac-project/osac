/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalinstance

//go:generate mockgen -source=../../api/osac/private/v1/baremetal_instances_service_grpc.pb.go -destination=bare_metal_instances_client_mock.go -package=baremetalinstance BareMetalInstancesClient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
)

const objectPrefix = "bmi-"

// defaultHostType is a placeholder until host type is modeled in the template proto.
const defaultHostType = "default"

const userDataSecretSuffix = "-user-data"

const userDataSecretKey = "userdata"

// FunctionBuilder contains the data and logic needed to build a function that reconciles bare metal instances.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                              *slog.Logger
	hubCache                            controllers.HubCache
	bareMetalInstancesClient            privatev1.BareMetalInstancesClient
	bareMetalInstanceCatalogItemsClient privatev1.BareMetalInstanceCatalogItemsClient
	hubsClient                          privatev1.HubsClient
	maskCalculator                      *masks.Calculator
}

type task struct {
	r                  *function
	bareMetalInstance  *privatev1.BareMetalInstance
	hubId              string
	hubNamespace       string
	hubClient          clnt.Client
	userDataSecretName string
}

// NewFunction creates a new builder that can then be used to create a new bare metal instance reconciler function.
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

// Build uses the information stored in the builder to create a new bare metal instance reconciler.
func (b *FunctionBuilder) Build() (result controllers.ReconcilerFunction[*privatev1.BareMetalInstance], err error) {
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

	object := &function{
		logger:                              b.logger,
		bareMetalInstancesClient:            privatev1.NewBareMetalInstancesClient(b.connection),
		bareMetalInstanceCatalogItemsClient: privatev1.NewBareMetalInstanceCatalogItemsClient(b.connection),
		hubsClient:                          privatev1.NewHubsClient(b.connection),
		hubCache:                            b.hubCache,
		maskCalculator:                      masks.NewCalculator().Build(),
	}
	result = object.run
	return
}

func (r *function) run(ctx context.Context, bareMetalInstance *privatev1.BareMetalInstance) error {
	oldBareMetalInstance := proto.Clone(bareMetalInstance).(*privatev1.BareMetalInstance)
	t := task{
		r:                 r,
		bareMetalInstance: bareMetalInstance,
	}
	var err error
	if bareMetalInstance.HasMetadata() && bareMetalInstance.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	updateMask := r.maskCalculator.Calculate(oldBareMetalInstance, bareMetalInstance)

	_, err = r.bareMetalInstancesClient.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
		Object:     bareMetalInstance,
		UpdateMask: updateMask,
	}.Build())

	return err
}

func (t *task) update(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	t.setDefaults()

	if err := t.validateTenant(); err != nil {
		return err
	}

	if err := t.selectHub(ctx); err != nil {
		return err
	}

	t.bareMetalInstance.GetStatus().SetHub(t.hubId)

	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}

	if t.bareMetalInstance.GetSpec().HasUserData() {
		t.userDataSecretName = fmt.Sprintf("%s%s", t.bareMetalInstance.GetId(), userDataSecretSuffix)
	}

	if object == nil {
		object = &bmfov1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: t.hubNamespace,
				Name:      fmt.Sprintf("%s%s", objectPrefix, t.bareMetalInstance.GetId()),
			},
		}
	}

	result, err := controllerutil.CreateOrPatch(ctx, t.hubClient, object, func() error {
		return t.mutateBMI(ctx, object)
	})
	if err != nil {
		return controllers.HandleK8sWriteError(ctx, t.r.logger, err, t.setFailed)
	}
	t.r.logger.DebugContext(
		ctx,
		fmt.Sprintf("%s bare metal instance", result),
		slog.String("namespace", object.GetNamespace()),
		slog.String("name", object.GetName()),
	)

	t.syncStatus(object)

	if err := t.ensureUserDataSecret(ctx, object); err != nil {
		return err
	}

	return nil
}

func (t *task) setDefaults() {
	if !t.bareMetalInstance.HasStatus() {
		t.bareMetalInstance.SetStatus(&privatev1.BareMetalInstanceStatus{})
	}
	if t.bareMetalInstance.GetStatus().GetState() == privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_UNSPECIFIED {
		t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING)
	}
	for value := range privatev1.BareMetalInstanceConditionType_name {
		if value != 0 {
			t.setConditionDefaults(privatev1.BareMetalInstanceConditionType(value))
		}
	}
}

func (t *task) setConditionDefaults(value privatev1.BareMetalInstanceConditionType) {
	exists := false
	for _, current := range t.bareMetalInstance.GetStatus().GetConditions() {
		if current.GetType() == value {
			exists = true
			break
		}
	}
	if !exists {
		t.updateCondition(value, privatev1.ConditionStatus_CONDITION_STATUS_FALSE, "", "")
	}
}

func (t *task) validateTenant() error {
	if !t.bareMetalInstance.HasMetadata() || t.bareMetalInstance.GetMetadata().GetTenant() == "" {
		return errors.New("BareMetalInstance must have a tenant assigned")
	}
	return nil
}

func (t *task) delete(ctx context.Context) (err error) {
	// Do nothing if we don't know the hub yet:
	t.hubId = t.bareMetalInstance.GetStatus().GetHub()
	if t.hubId == "" {
		t.removeFinalizer()
		return nil
	}

	t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING)
	t.updateCondition(
		privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_READY,
		privatev1.ConditionStatus_CONDITION_STATUS_FALSE, "", "")
	err = t.getHub(ctx)
	if err != nil {
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "bare_metal_instance_id", t.bareMetalInstance.GetId(), t.removeFinalizer)
			return nil
		}
		return
	}

	object, err := t.getKubeObject(ctx)
	if err != nil {
		return
	}
	if object == nil {
		t.r.logger.DebugContext(
			ctx,
			"Bare metal instance doesn't exist",
			slog.String("id", t.bareMetalInstance.GetId()),
		)
		t.removeFinalizer()
		return
	}

	if object.GetDeletionTimestamp() == nil {
		err = t.hubClient.Delete(ctx, object)
		if err != nil {
			return
		}
		t.r.logger.DebugContext(
			ctx,
			"Deleted bare metal instance",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		t.r.logger.DebugContext(
			ctx,
			"Bare metal instance is still being deleted, waiting for K8s finalizers",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return
}

func (t *task) selectHub(ctx context.Context) error {
	t.hubId = t.bareMetalInstance.GetStatus().GetHub()
	if t.hubId == "" {
		response, err := t.r.hubsClient.List(ctx, privatev1.HubsListRequest_builder{}.Build())
		if err != nil {
			return err
		}
		if len(response.Items) == 0 {
			return errors.New("there are no hubs")
		}
		t.hubId = response.Items[rand.IntN(len(response.Items))].GetId()
	}
	t.r.logger.DebugContext(
		ctx,
		"Selected hub",
		slog.String("id", t.hubId),
	)
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getHub(ctx context.Context) error {
	t.hubId = t.bareMetalInstance.GetStatus().GetHub()
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getKubeObject(ctx context.Context) (result *bmfov1alpha1.BareMetalInstance, err error) {
	list := &bmfov1alpha1.BareMetalInstanceList{}
	err = t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.BareMetalInstanceUuid: t.bareMetalInstance.GetId(),
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		err = fmt.Errorf(
			"expected at most one bare metal instance with identifier '%s' but found %d",
			t.bareMetalInstance.GetId(), count,
		)
		return
	}
	if count > 0 {
		result = &items[0]
	}
	return
}

func (t *task) addFinalizer() bool {
	if !t.bareMetalInstance.HasMetadata() {
		t.bareMetalInstance.SetMetadata(&privatev1.Metadata{})
	}
	list := t.bareMetalInstance.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.bareMetalInstance.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.bareMetalInstance.HasMetadata() {
		return
	}
	list := t.bareMetalInstance.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.bareMetalInstance.GetMetadata().SetFinalizers(list)
	}
}

func (t *task) setFailed(err error) {
	if !t.bareMetalInstance.HasStatus() {
		t.bareMetalInstance.SetStatus(&privatev1.BareMetalInstanceStatus{})
	}
	t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_FAILED)
	t.updateCondition(
		privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED,
		privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
		"ValidationFailed",
		err.Error(),
	)
}

func (t *task) updateCondition(conditionType privatev1.BareMetalInstanceConditionType, status privatev1.ConditionStatus,
	reason string, message string) {
	conditions := t.bareMetalInstance.GetStatus().GetConditions()
	updated := false
	for i, condition := range conditions {
		if condition.GetType() == conditionType {
			conditions[i] = privatev1.BareMetalInstanceCondition_builder{
				Type:    conditionType,
				Status:  status,
				Reason:  &reason,
				Message: &message,
			}.Build()
			updated = true
			break
		}
	}
	if !updated {
		conditions = append(conditions, privatev1.BareMetalInstanceCondition_builder{
			Type:    conditionType,
			Status:  status,
			Reason:  &reason,
			Message: &message,
		}.Build())
	}
	t.bareMetalInstance.GetStatus().SetConditions(conditions)
}

func (t *task) syncStatus(object *bmfov1alpha1.BareMetalInstance) {
	if object == nil {
		return
	}

	powerSynced := object.GetStatusCondition(bmfov1alpha1.HostConditionPowerSynced)

	t.syncState(object, powerSynced)

	readyStatus := privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	state := t.bareMetalInstance.GetStatus().GetState()
	if state == privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING ||
		state == privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING ||
		state == privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_FAILED {
		readyStatus = privatev1.ConditionStatus_CONDITION_STATUS_FALSE
	}
	t.updateCondition(privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_READY, readyStatus, "", "")

	// PROVISIONED is a ratchet: only promoted True once the template completes;
	// never demoted False once set (re-provisioning cycles must not un-provision the instance).
	// TemplateComplete=True implies Allocated=True by ordering, so no separate allocation check needed.
	templateCond := object.GetStatusCondition(bmfov1alpha1.HostConditionProvisionTemplateComplete)
	if templateCond != nil && templateCond.Status == metav1.ConditionTrue {
		t.updateCondition(
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_PROVISIONED,
			privatev1.ConditionStatus_CONDITION_STATUS_TRUE, "", "")
	}

	restartPending := t.bareMetalInstance.GetSpec().GetRestartTrigger() != t.bareMetalInstance.GetStatus().GetRestartTrigger()

	for _, cond := range object.Status.Conditions {
		condType := bmfov1alpha1.BareMetalInstanceConditionType(cond.Type)
		status := mapConditionStatus(cond.Status)

		switch condType {
		case bmfov1alpha1.HostConditionProvisionTemplateComplete:
			t.updateCondition(
				privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED,
				status, cond.Reason, sanitizeConditionMessage(condType, cond.Status))
		case bmfov1alpha1.HostConditionPowerSynced:
			if !restartPending {
				continue
			}
			if cond.Status == metav1.ConditionFalse && cond.Reason == bmfov1alpha1.HostConditionReasonProgressing {
				t.updateCondition(
					privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS,
					privatev1.ConditionStatus_CONDITION_STATUS_TRUE, cond.Reason, "Restart in progress")
				t.updateCondition(
					privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED,
					privatev1.ConditionStatus_CONDITION_STATUS_FALSE, "", "")
			} else if cond.Status == metav1.ConditionFalse {
				t.updateCondition(
					privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED,
					privatev1.ConditionStatus_CONDITION_STATUS_TRUE, "PowerSyncFailed", "Restart failed")
				t.updateCondition(
					privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS,
					privatev1.ConditionStatus_CONDITION_STATUS_FALSE, "", "")
			} else if cond.Status == metav1.ConditionTrue {
				if object.Spec.RestartTrigger == object.Status.RestartTrigger {
					t.updateCondition(
						privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS,
						privatev1.ConditionStatus_CONDITION_STATUS_FALSE, "", "")
					t.updateCondition(
						privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED,
						privatev1.ConditionStatus_CONDITION_STATUS_FALSE, "", "")
					t.bareMetalInstance.GetStatus().SetRestartTrigger(
						t.bareMetalInstance.GetSpec().GetRestartTrigger())
				} else {
					t.updateCondition(
						privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS,
						privatev1.ConditionStatus_CONDITION_STATUS_TRUE, cond.Reason, "Restart in progress")
					t.updateCondition(
						privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED,
						privatev1.ConditionStatus_CONDITION_STATUS_FALSE, "", "")
				}
			}
		}
	}
}

func (t *task) syncState(object *bmfov1alpha1.BareMetalInstance, powerSynced *metav1.Condition) {
	switch object.Status.Phase {
	case bmfov1alpha1.BareMetalInstancePhaseFailed:
		t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_FAILED)
		return
	case bmfov1alpha1.BareMetalInstancePhaseDeleting:
		t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING)
		return
	}

	if powerSynced == nil {
		t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING)
		return
	}

	switch {
	case powerSynced.Status == metav1.ConditionTrue && powerSynced.Reason == bmfov1alpha1.HostConditionReasonPowerOn:
		t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING)
	case powerSynced.Status == metav1.ConditionTrue && powerSynced.Reason == bmfov1alpha1.HostConditionReasonPowerOff:
		t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPED)
	case powerSynced.Status == metav1.ConditionFalse && powerSynced.Reason == bmfov1alpha1.HostConditionReasonProgressing:
		if object.Spec.RunStrategy == bmfov1alpha1.RunStrategyHalted {
			t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPING)
		} else {
			t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STARTING)
		}
	case powerSynced.Status == metav1.ConditionFalse:
		t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_FAILED)
	default:
		t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING)
	}
}

func mapConditionStatus(status metav1.ConditionStatus) privatev1.ConditionStatus {
	switch status {
	case metav1.ConditionTrue:
		return privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	case metav1.ConditionFalse:
		return privatev1.ConditionStatus_CONDITION_STATUS_FALSE
	default:
		return privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
	}
}

// sanitizeConditionMessage returns a tenant-facing message for a given condition type and status,
// replacing internal implementation details from the bare-metal-fulfillment-operator.
func sanitizeConditionMessage(condType bmfov1alpha1.BareMetalInstanceConditionType,
	status metav1.ConditionStatus) string {
	if status == metav1.ConditionTrue {
		switch condType {
		case bmfov1alpha1.HostConditionAllocated:
			return "BareMetalInstance successfully provisioned"
		case bmfov1alpha1.HostConditionProvisionTemplateComplete:
			return "Configuration successfully applied"
		case bmfov1alpha1.HostConditionAvailable:
			return "BareMetalInstance is ready"
		}
	}
	return ""
}

// mutateBMI sets the fulfillment-service-owned metadata and spec fields, leaving
// operator-managed fields (ExternalHostID, HostClass, etc.) untouched.
func (t *task) mutateBMI(ctx context.Context, object *bmfov1alpha1.BareMetalInstance) error {
	if object.Labels == nil {
		object.Labels = make(map[string]string)
	}
	object.Labels[labels.BareMetalInstanceUuid] = t.bareMetalInstance.GetId()
	if object.Annotations == nil {
		object.Annotations = make(map[string]string)
	}
	object.Annotations[annotations.Tenant] = t.bareMetalInstance.GetMetadata().GetTenant()

	catalogItemID := t.bareMetalInstance.GetSpec().GetCatalogItem()
	catalogItemResp, err := t.r.bareMetalInstanceCatalogItemsClient.Get(ctx, privatev1.BareMetalInstanceCatalogItemsGetRequest_builder{
		Id: catalogItemID.GetId(),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to get catalog item '%s': %w", catalogItemID, err)
	}

	object.Spec.HostType = defaultHostType
	object.Spec.TemplateID = catalogItemResp.GetObject().GetTemplate().GetId()
	object.Spec.TemplateParameters = ""
	object.Spec.RunStrategy = bmfov1alpha1.RunStrategyUnspecified
	object.Spec.RestartTrigger = t.bareMetalInstance.GetSpec().GetRestartTrigger()

	params := map[string]any{}

	userTemplateParams := t.bareMetalInstance.GetSpec().GetTemplateParameters()
	if len(userTemplateParams) > 0 {
		userParamsJSON, err := utils.ConvertTemplateParametersToJSON(userTemplateParams)
		if err != nil {
			return fmt.Errorf("failed to convert template parameters: %w", err)
		}
		if err := json.Unmarshal([]byte(userParamsJSON), &params); err != nil {
			return fmt.Errorf("failed to unmarshal template parameters: %w", err)
		}
	}

	if t.bareMetalInstance.GetSpec().HasSshPublicKey() {
		params["sshPublicKey"] = t.bareMetalInstance.GetSpec().GetSshPublicKey()
	}
	if t.userDataSecretName != "" {
		params["userDataSecret"] = t.userDataSecretName
	}
	if t.bareMetalInstance.GetSpec().HasImage() {
		params["imageURL"] = t.bareMetalInstance.GetSpec().GetImage().GetSourceRef()
		if st := t.bareMetalInstance.GetSpec().GetImage().GetSourceType(); st != "" {
			params["imageSourceType"] = st
		} else {
			delete(params, "imageSourceType")
		}
	}
	if len(params) > 0 {
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal template parameters: %w", err)
		}
		object.Spec.TemplateParameters = string(paramsJSON)
	}

	if t.bareMetalInstance.GetSpec().HasRunStrategy() {
		switch t.bareMetalInstance.GetSpec().GetRunStrategy() {
		case privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS:
			object.Spec.RunStrategy = bmfov1alpha1.RunStrategyAlways
		case privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED:
			object.Spec.RunStrategy = bmfov1alpha1.RunStrategyHalted
		}
	}

	protoAttachments := t.bareMetalInstance.GetSpec().GetNetworkAttachments()
	if len(protoAttachments) > 0 {
		networkAttachments := make([]bmfov1alpha1.BareMetalNetworkAttachment, 0, len(protoAttachments))
		for _, att := range protoAttachments {
			secGroupRefs := make([]string, 0, len(att.GetSecurityGroups()))
			for _, sg := range att.GetSecurityGroups() {
				secGroupRefs = append(secGroupRefs, controllers.RefKeyStr(sg))
			}
			networkAttachments = append(networkAttachments, bmfov1alpha1.BareMetalNetworkAttachment{
				SubnetRef:         controllers.RefKeyStr(att.GetSubnet()),
				SecurityGroupRefs: secGroupRefs,
				Interface:         att.GetInterface(),
				Primary:           att.GetPrimary(),
			})
		}
		object.Spec.NetworkAttachments = networkAttachments
	}

	return nil
}

// ensureUserDataSecret creates a Kubernetes Secret containing the cloud-init user data
// provided via the fulfillment API. The Secret is owned by the BareMetalInstance CR so that
// Kubernetes garbage collection handles cleanup automatically on deletion.
func (t *task) ensureUserDataSecret(ctx context.Context, owner *bmfov1alpha1.BareMetalInstance) error {
	if t.userDataSecretName == "" {
		return nil
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.hubNamespace,
			Name:      t.userDataSecretName,
			Labels: map[string]string{
				labels.BareMetalInstanceUuid: t.bareMetalInstance.GetId(),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: bmfov1alpha1.GroupVersion.String(),
					Kind:       "BareMetalInstance",
					Name:       owner.GetName(),
					UID:        owner.GetUID(),
				},
			},
		},
		StringData: map[string]string{
			userDataSecretKey: t.bareMetalInstance.GetSpec().GetUserData(),
		},
	}

	err := t.hubClient.Create(ctx, secret)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	if err != nil {
		return err
	}
	t.r.logger.DebugContext(
		ctx,
		"Created user data secret",
		slog.String("namespace", secret.GetNamespace()),
		slog.String("name", secret.GetName()),
	)
	return nil
}
