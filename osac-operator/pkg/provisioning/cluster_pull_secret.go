package provisioning

import (
	"encoding/json"
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/pullsecret"
)

// protectClusterOrderLaunch validates the final job/workflow input after all merges.
func protectClusterOrderLaunch(resource client.Object, extraVars map[string]any) error {
	order, ok := resource.(*v1alpha1.ClusterOrder)
	if !ok {
		return nil
	}
	if pullsecret.ValidateOrder(order) != nil {
		return pullsecret.ErrInvalid
	}
	if order.Spec.PullSecretRef == nil || order.Spec.PullSecretRef.Name == "" || order.Spec.PullSecretRevision == "" {
		return pullsecret.ErrInvalid
	}
	clean := order.DeepCopy()
	delete(clean.Annotations, pullsecret.LastAppliedAnnotation)
	expected, err := serializeResource(clean)
	if err != nil {
		return pullsecret.ErrInvalid
	}
	if err := validateClusterCredentialVars(extraVars, expected); err != nil {
		return err
	}
	encoded, err := json.Marshal(extraVars["osac_job_vars"])
	if err != nil {
		return pullsecret.ErrInvalid
	}
	var jobVars map[string]any
	if json.Unmarshal(encoded, &jobVars) != nil || jobVars == nil {
		return pullsecret.ErrInvalid
	}
	if err := validateClusterCredentialVars(jobVars, expected); err != nil {
		return err
	}
	original, err := serializeResource(order)
	if err != nil || !reflect.DeepEqual(jobVars["resource"], original) {
		return pullsecret.ErrInvalid
	}
	originalVars, ok := extraVars["osac_job_vars"].(map[string]any)
	if !ok {
		return pullsecret.ErrInvalid
	}
	protected := make(map[string]any, len(originalVars))
	for key, value := range originalVars {
		protected[key] = value
	}
	protected["resource"] = expected
	extraVars["osac_job_vars"] = protected
	return nil
}

// validateClusterCredentialVars checks recognized credential slots, not unrelated resources.
func validateClusterCredentialVars(vars map[string]any, expected map[string]any) error {
	metadata, ok := expected["metadata"].(map[string]any)
	if !ok {
		return pullsecret.ErrInvalid
	}
	orderName, ok := metadata["name"].(string)
	if !ok || orderName == "" {
		return pullsecret.ErrInvalid
	}
	targetName := "pullsecret-cluster-" + orderName
	if name, exists := vars["cluster_settings_pull_secret_name"]; exists && name != targetName {
		return pullsecret.ErrInvalid
	}
	for _, name := range []string{"pull_secret", "pullSecret", "cluster_settings_pull_secret", "cluster_settings_referenced_secret", "default_cluster_credentials"} {
		if _, exists := vars[name]; exists {
			return pullsecret.ErrInvalid
		}
	}
	for _, name := range []string{"template_parameters", "cluster_settings_template_parameters", "cluster_order", "hosted_cluster_settings"} {
		value, exists := vars[name]
		if !exists {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return pullsecret.ErrInvalid
		}
		var fields map[string]any
		if json.Unmarshal(encoded, &fields) != nil || fields == nil {
			return pullsecret.ErrInvalid
		}
		if name == "cluster_order" && !reflect.DeepEqual(fields, expected) {
			return pullsecret.ErrInvalid
		}
		if name == "hosted_cluster_settings" {
			if value, exists := fields["pull_secret_name"]; exists && value != targetName {
				return pullsecret.ErrInvalid
			}
		}
		for _, key := range []string{"pull_secret", "pullSecret"} {
			if _, exists := fields[key]; exists {
				return pullsecret.ErrInvalid
			}
		}
	}
	return nil
}
