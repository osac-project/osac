/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstance

//go:generate mockgen -source=../../api/osac/private/v1/compute_instances_service_grpc.pb.go -destination=compute_instances_client_mock.go -package=computeinstance ComputeInstancesClient
//go:generate mockgen -source=../../api/osac/private/v1/instance_types_service_grpc.pb.go -destination=instance_types_client_mock.go -package=computeinstance InstanceTypesClient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"

	"github.com/osac-project/osac/fulfillment-service/internal/computeinstancespec"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
)

// objectPrefix is the prefix that will be used in the `generateName` field of the resources created in the hub.
const objectPrefix = "vm-"

// userDataSecretSuffix is appended to the compute instance ID to form the user data Secret name.
const userDataSecretSuffix = "-user-data"

// userDataSecretKey is the key used in the Secret's stringData to store the cloud-init user data.
const userDataSecretKey = "userdata"

// errTransientK8sError marks errors from Kubernetes Create/Patch calls that are not permanent
// validation failures. Reconciliation errors are normally recorded as a FAILED state via
// setReconciliationFailed, but transient errors should just be retried, leaving the compute
// instance's state untouched so it doesn't look like a permanent failure while it is retried.
var errTransientK8sError = errors.New("transient kubernetes error")

// FunctionBuilder contains the data and logic needed to build a function that reconciles compute instances.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                 *slog.Logger
	hubCache               controllers.HubCache
	computeInstancesClient privatev1.ComputeInstancesClient
	hubsClient             privatev1.HubsClient
	instanceTypesClient    privatev1.InstanceTypesClient
	maskCalculator         *masks.Calculator
}

type task struct {
	r                  *function
	computeInstance    *privatev1.ComputeInstance
	hubId              string
	hubNamespace       string
	hubClient          clnt.Client
	userDataSecretName string
}

// NewFunction creates a new builder that can then be used to create a new compute instance reconciler function.
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

// Build uses the information stored in the builder to create a new compute instance reconciler.
func (b *FunctionBuilder) Build() (result controllers.ReconcilerFunction[*privatev1.ComputeInstance], err error) {
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
		logger:                 b.logger,
		computeInstancesClient: privatev1.NewComputeInstancesClient(b.connection),
		hubsClient:             privatev1.NewHubsClient(b.connection),
		instanceTypesClient:    privatev1.NewInstanceTypesClient(b.connection),
		hubCache:               b.hubCache,
		maskCalculator:         masks.NewCalculator().Build(),
	}
	result = object.run
	return
}

func (r *function) run(ctx context.Context, computeInstance *privatev1.ComputeInstance) error {
	oldComputeInstance := proto.Clone(computeInstance).(*privatev1.ComputeInstance)
	t := task{
		r:               r,
		computeInstance: computeInstance,
	}
	var reconcileErr error
	if computeInstance.HasMetadata() && computeInstance.GetMetadata().HasDeletionTimestamp() {
		reconcileErr = t.delete(ctx)
	} else {
		reconcileErr = t.update(ctx)
	}
	if reconcileErr != nil && !errors.Is(reconcileErr, errTransientK8sError) {
		t.setReconciliationFailed(reconcileErr)
	}
	// Calculate which fields the reconciler actually modified and use a field mask
	// to update only those fields. This prevents overwriting concurrent user changes.
	updateMask := r.maskCalculator.Calculate(oldComputeInstance, computeInstance)

	// Only send an update if there are actual changes
	var updateErr error
	if len(updateMask.GetPaths()) > 0 {
		_, updateErr = r.computeInstancesClient.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
			Object:     computeInstance,
			UpdateMask: updateMask,
		}.Build())
	}

	if reconcileErr != nil {
		if updateErr != nil {
			r.logger.WarnContext(ctx, "Failed to persist status after reconciliation error",
				slog.String("reconcile_error", reconcileErr.Error()),
				slog.String("update_error", updateErr.Error()),
			)
		}
		return reconcileErr
	}
	return updateErr
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

	// Select the hub and return immediately if it was just selected. This ensures the hub is
	// persisted before any Kubernetes objects are created.
	hubJustSelected := t.computeInstance.GetStatus().GetHub() == ""
	if err := t.selectHub(ctx); err != nil {
		return err
	}
	t.computeInstance.GetStatus().SetHub(t.hubId)
	if hubJustSelected {
		return nil
	}

	// Get the K8S object:
	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}

	// Set the user data Secret name if user data is provided (no K8s call yet):
	if t.computeInstance.GetSpec().HasUserData() {
		t.userDataSecretName = fmt.Sprintf("%s%s", t.computeInstance.GetId(), userDataSecretSuffix)
	}

	// Prepare the changes to the spec:
	spec, err := t.buildSpec(ctx)
	if err != nil {
		return err
	}

	// Create or update the Kubernetes object:
	if object == nil {
		objectLabels := map[string]string{
			labels.ComputeInstanceUuid: t.computeInstance.GetId(),
		}
		if instanceTypeName := t.computeInstance.GetSpec().GetInstanceType(); instanceTypeName != nil {
			objectLabels[labels.InstanceTypeName] = instanceTypeName.GetName()
		}
		object = &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.hubNamespace,
				GenerateName: objectPrefix,
				Labels:       objectLabels,
				Annotations: map[string]string{
					annotations.Tenant: t.computeInstance.GetMetadata().GetTenant(),
				},
			},
			Spec: spec,
		}
		err = t.hubClient.Create(ctx, object)
		if err != nil {
			if err := controllers.HandleK8sWriteError(ctx, t.r.logger, err, t.setFailed); err != nil {
				return fmt.Errorf("%w: %w", errTransientK8sError, err)
			}
			return nil
		}
		t.r.logger.DebugContext(
			ctx,
			"Created compute instance",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		update := object.DeepCopy()
		update.Spec = spec
		err = t.hubClient.Patch(ctx, update, clnt.MergeFrom(object))
		if err != nil {
			if err := controllers.HandleK8sWriteError(ctx, t.r.logger, err, t.setFailed); err != nil {
				return fmt.Errorf("%w: %w", errTransientK8sError, err)
			}
			return nil
		}
		t.r.logger.DebugContext(
			ctx,
			"Updated compute instance",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	// Create the user data Secret with owner reference to the CR (immutable, created once):
	if err := t.ensureUserDataSecret(ctx, object); err != nil {
		return err
	}

	return nil
}

func (t *task) setDefaults() {
	if !t.computeInstance.HasStatus() {
		t.computeInstance.SetStatus(&privatev1.ComputeInstanceStatus{})
	}
	if t.computeInstance.GetStatus().GetState() == privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED {
		t.computeInstance.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING)
		t.computeInstance.GetStatus().SetStateTransitionTime(timestamppb.Now())
	}
	for value := range privatev1.ComputeInstanceConditionType_name {
		if value != 0 {
			t.setConditionDefaults(privatev1.ComputeInstanceConditionType(value))
		}
	}
}

func (t *task) setConditionDefaults(value privatev1.ComputeInstanceConditionType) {
	// Check if condition already exists
	exists := false
	for _, current := range t.computeInstance.GetStatus().GetConditions() {
		if current.GetType() == value {
			exists = true
			break
		}
	}
	// Only set default if condition doesn't exist
	if !exists {
		t.updateCondition(value, privatev1.ConditionStatus_CONDITION_STATUS_FALSE, "", "")
	}
}

func (t *task) validateTenant() error {
	if !t.computeInstance.HasMetadata() || t.computeInstance.GetMetadata().GetTenant() == "" {
		return errors.New("ComputeInstance must have a tenant assigned")
	}
	return nil
}

func (t *task) delete(ctx context.Context) (err error) {
	// Do nothing if we don't know the hub yet:
	t.hubId = t.computeInstance.GetStatus().GetHub()
	if t.hubId == "" {
		// No hub assigned, nothing to clean up on K8s side.
		t.removeFinalizer()
		return nil
	}
	err = t.getHub(ctx)
	if err != nil {
		// Check if the hub has been decommissioned (deleted from database)
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "compute_instance_id", t.computeInstance.GetId(), t.removeFinalizer)
			return nil
		}
		// For transient errors (network, timeout, etc.), continue retrying
		return
	}

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
			"Compute instance doesn't exist",
			slog.String("id", t.computeInstance.GetId()),
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
			"Deleted compute instance",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		t.r.logger.DebugContext(
			ctx,
			"Compute instance is still being deleted, waiting for K8s finalizers",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	// Don't remove finalizer — K8s object still exists with finalizers being processed.
	return
}

func (t *task) selectHub(ctx context.Context) error {
	t.hubId = t.computeInstance.GetStatus().GetHub()
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
	t.hubId = t.computeInstance.GetStatus().GetHub()
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getKubeObject(ctx context.Context) (result *osacv1alpha1.ComputeInstance, err error) {
	list := &osacv1alpha1.ComputeInstanceList{}
	err = t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.ComputeInstanceUuid: t.computeInstance.GetId(),
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		err = fmt.Errorf(
			"expected at most one compute instance with identifier '%s' but found %d",
			t.computeInstance.GetId(), count,
		)
		return
	}
	if count > 0 {
		result = &items[0]
	}
	return
}

// getSubnetCR looks up a Subnet CR in the hub cluster by its fulfillment UUID label.
// Returns the Subnet CR if exactly one is found, nil if none found, or an error if multiple found.
func (t *task) getSubnetCR(ctx context.Context, subnetID string) (*osacv1alpha1.Subnet, error) {
	list := &osacv1alpha1.SubnetList{}
	err := t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.SubnetUuid: subnetID,
		},
	)
	if err != nil {
		return nil, err
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		return nil, fmt.Errorf(
			"expected at most one subnet with identifier '%s' but found %d",
			subnetID, count,
		)
	}
	if count == 0 {
		return nil, nil
	}
	return &items[0], nil
}

// getSecurityGroupCR looks up a SecurityGroup CR in the hub cluster by its fulfillment UUID label.
func (t *task) getSecurityGroupCR(ctx context.Context, securityGroupID string) (*osacv1alpha1.SecurityGroup, error) {
	list := &osacv1alpha1.SecurityGroupList{}
	err := t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.SecurityGroupUuid: securityGroupID,
		},
	)
	if err != nil {
		return nil, err
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		return nil, fmt.Errorf(
			"expected at most one security group with identifier '%s' but found %d",
			securityGroupID, count,
		)
	}
	if count == 0 {
		return nil, nil
	}
	return &items[0], nil
}

// addFinalizer adds the controller finalizer if it is not already present. Returns true if the finalizer was added,
// false if it was already present.
func (t *task) addFinalizer() bool {
	if !t.computeInstance.HasMetadata() {
		t.computeInstance.SetMetadata(&privatev1.Metadata{})
	}
	list := t.computeInstance.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.computeInstance.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.computeInstance.HasMetadata() {
		return
	}
	list := t.computeInstance.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.computeInstance.GetMetadata().SetFinalizers(list)
	}
}

func (t *task) setFailed(err error) {
	if !t.computeInstance.HasStatus() {
		t.computeInstance.SetStatus(&privatev1.ComputeInstanceStatus{})
	}
	t.computeInstance.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED)
	t.updateCondition(
		privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED,
		privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
		"ValidationFailed",
		err.Error(),
	)
}

// updateCondition updates or creates a condition with the specified type, status, reason, and message.
func (t *task) updateCondition(conditionType privatev1.ComputeInstanceConditionType, status privatev1.ConditionStatus,
	reason string, message string) {
	conditions := t.computeInstance.GetStatus().GetConditions()
	updated := false
	for i, condition := range conditions {
		if condition.GetType() == conditionType {
			conditions[i] = privatev1.ComputeInstanceCondition_builder{
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
		conditions = append(conditions, privatev1.ComputeInstanceCondition_builder{
			Type:    conditionType,
			Status:  status,
			Reason:  &reason,
			Message: &message,
		}.Build())
	}
	t.computeInstance.GetStatus().SetConditions(conditions)
}

func (t *task) setReconciliationFailed(err error) {
	if !t.computeInstance.HasStatus() {
		t.computeInstance.SetStatus(&privatev1.ComputeInstanceStatus{})
	}
	if t.computeInstance.GetStatus().GetState() != privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED ||
		t.computeInstance.GetStatus().GetStateTransitionTime() == nil {
		t.computeInstance.GetStatus().SetStateTransitionTime(timestamppb.Now())
	}
	t.computeInstance.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED)
	t.updateCondition(
		privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED,
		privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
		"ReconciliationFailed",
		err.Error(),
	)
}

// buildSpec constructs the spec for the Kubernetes ComputeInstance object based on the
// compute instance from the database.
func (t *task) buildSpec(ctx context.Context) (osacv1alpha1.ComputeInstanceSpec, error) {
	templateParameters, err := utils.ConvertTemplateParametersToJSON(t.computeInstance.GetSpec().GetTemplateParameters())
	if err != nil {
		return osacv1alpha1.ComputeInstanceSpec{}, err
	}
	spec := osacv1alpha1.ComputeInstanceSpec{
		TemplateID:         controllers.RefKeyStr(t.computeInstance.GetSpec().GetTemplate()),
		TemplateParameters: templateParameters,
	}

	// Add restartRequestedAt if present:
	if t.computeInstance.GetSpec().HasRestartRequestedAt() {
		restartTime := metav1.NewTime(t.computeInstance.GetSpec().GetRestartRequestedAt().AsTime())
		spec.RestartRequestedAt = &restartTime
	}

	// Add explicit spec fields if present:
	if err := t.addExplicitFields(ctx, &spec); err != nil {
		return osacv1alpha1.ComputeInstanceSpec{}, err
	}

	// Handle network_attachments (required)
	err = t.buildSpecNetworkAttachments(ctx, &spec)
	if err != nil {
		return osacv1alpha1.ComputeInstanceSpec{}, err
	}

	return spec, nil
}

// buildSpecNetworkAttachments handles the network_attachments field.
func (t *task) buildSpecNetworkAttachments(ctx context.Context, spec *osacv1alpha1.ComputeInstanceSpec) error {
	ciSpec := t.computeInstance.GetSpec()

	// Validate network_attachments structure before processing
	err := computeinstancespec.ValidateNetworkAttachments(ciSpec.GetNetworkAttachments())
	if err != nil {
		return fmt.Errorf(
			"invalid network_attachments in database: %w", err)
	}

	networkAttachments := make([]osacv1alpha1.NetworkAttachment, 0, len(ciSpec.GetNetworkAttachments()))
	for i, att := range ciSpec.GetNetworkAttachments() {
		subnetID := att.GetSubnet()
		// subnetID is guaranteed to be non-empty by ValidateNetworkAttachments

		subnetCR, err := t.getSubnetCR(ctx, subnetID.GetId())
		if err != nil {
			return fmt.Errorf(
				"failed to look up Subnet CR for network_attachments[%d] subnet %s: %w",
				i, subnetID, err)
		}
		if subnetCR == nil {
			return fmt.Errorf( //nolint:staticcheck // ST1005: Subnet is an API resource name
				"Subnet CR not found for network_attachments[%d] subnet %s",
				i, subnetID)
		}
		subnetRef := subnetCR.GetName()
		t.r.logger.DebugContext(
			ctx,
			"Resolved subnetRef from Subnet CR",
			slog.String("subnet_id", subnetID.GetId()),
			slog.String("subnet_ref", subnetRef),
		)

		sgRefs := make([]string, 0, len(att.GetSecurityGroups()))
		for _, sgID := range att.GetSecurityGroups() {
			if sgID == nil {
				continue
			}
			sgCR, sgErr := t.getSecurityGroupCR(ctx, sgID.GetId())
			if sgErr != nil {
				return fmt.Errorf(
					"failed to look up SecurityGroup CR for network_attachments[%d] security group %s: %w",
					i, sgID, sgErr)
			}
			if sgCR == nil {
				return fmt.Errorf(
					"SecurityGroup CR not found for network_attachments[%d] security group %s",
					i, sgID)
			}
			sgRefs = append(sgRefs, sgCR.GetName())
			t.r.logger.DebugContext(
				ctx,
				"Resolved securityGroupRef from SecurityGroup CR",
				slog.String("security_group_id", sgID.GetId()),
				slog.String("security_group_ref", sgCR.GetName()),
			)
		}

		networkAttachments = append(networkAttachments, osacv1alpha1.NetworkAttachment{
			SubnetRef:         subnetRef,
			SecurityGroupRefs: sgRefs,
		})
	}

	if len(networkAttachments) > 0 {
		spec.NetworkAttachments = networkAttachments
	}

	return nil
}

func (t *task) addExplicitFields(ctx context.Context, spec *osacv1alpha1.ComputeInstanceSpec) error {
	ciSpec := t.computeInstance.GetSpec()

	instanceTypeRef := ciSpec.GetInstanceType()
	if instanceTypeRef == nil {
		return fmt.Errorf(
			"compute instance '%s' has no instance_type set; cannot resolve compute resources",
			t.computeInstance.GetId(),
		)
	}
	instanceTypeKey := controllers.RefKeyStr(instanceTypeRef)
	response, err := t.r.instanceTypesClient.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
		Id: instanceTypeKey,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to resolve instance type '%s': %w", instanceTypeKey, err)
	}
	itSpec := response.GetObject().GetSpec()
	spec.Cores = itSpec.GetCores()
	spec.MemoryGiB = itSpec.GetMemoryGib()
	if ciSpec.HasRunStrategy() {
		spec.RunStrategy = osacv1alpha1.RunStrategyType(ciSpec.GetRunStrategy())
	}
	if ciSpec.HasSshPublicKey() {
		spec.SSHKey = ciSpec.GetSshPublicKey()
	}
	if t.userDataSecretName != "" {
		spec.UserDataSecretRef = &corev1.LocalObjectReference{
			Name: t.userDataSecretName,
		}
	}
	if ciSpec.HasImage() {
		spec.Image = osacv1alpha1.ImageSpec{
			SourceType: osacv1alpha1.ImageSourceType(ciSpec.GetImage().GetSourceType()),
			SourceRef:  ciSpec.GetImage().GetSourceRef(),
		}
	}
	if ciSpec.HasBootDisk() {
		spec.BootDisk = osacv1alpha1.DiskSpec{
			SizeGiB: ciSpec.GetBootDisk().GetSizeGib(),
		}
	}
	if len(ciSpec.GetAdditionalDisks()) > 0 {
		disks := make([]osacv1alpha1.DiskSpec, 0, len(ciSpec.GetAdditionalDisks()))
		for _, disk := range ciSpec.GetAdditionalDisks() {
			disks = append(disks, osacv1alpha1.DiskSpec{
				SizeGiB: disk.GetSizeGib(),
			})
		}
		spec.AdditionalDisks = disks
	}

	// Map is_windows boolean to guestOSFamily string
	if ciSpec.HasIsWindows() && ciSpec.GetIsWindows() {
		spec.GuestOSFamily = "windows"
	} else {
		spec.GuestOSFamily = "linux"
	}

	return nil
}

// ensureUserDataSecret creates a Kubernetes Secret containing the cloud-init user data
// provided via the fulfillment API. The Secret is owned by the ComputeInstance CR so that
// Kubernetes garbage collection handles cleanup automatically on deletion.
// The user data field is immutable, so the Secret is only created once.
func (t *task) ensureUserDataSecret(ctx context.Context, owner *osacv1alpha1.ComputeInstance) error {
	if t.userDataSecretName == "" {
		return nil
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.hubNamespace,
			Name:      t.userDataSecretName,
			Labels: map[string]string{
				labels.ComputeInstanceUuid: t.computeInstance.GetId(),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: osacv1alpha1.GroupVersion.String(),
					Kind:       "ComputeInstance",
					Name:       owner.GetName(),
					UID:        owner.GetUID(),
				},
			},
		},
		StringData: map[string]string{
			userDataSecretKey: t.computeInstance.GetSpec().GetUserData(),
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
