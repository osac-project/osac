package provisioning

import (
	"encoding/json"
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/pullsecret"
)

func protectClusterOrderLaunch(resource client.Object, extraVars map[string]any) error {
	order, ok := resource.(*v1alpha1.ClusterOrder)
	if !ok {
		return nil
	}
	parameters, err := pullsecret.Parameters(order)
	if err != nil || order.Spec.PullSecret != "" {
		return pullsecret.ErrInvalid
	}
	if _, exists := parameters["pull_secret"]; exists {
		return pullsecret.ErrInvalid
	}
	if order.Spec.PullSecretRef != nil && (order.Spec.PullSecretRef.Name == "" || order.Spec.PullSecretRevision == "") {
		return pullsecret.ErrInvalid
	}
	clean := order.DeepCopy()
	delete(clean.Annotations, pullsecret.LastAppliedAnnotation)
	expected, err := serializeResource(clean)
	if err != nil {
		return pullsecret.ErrInvalid
	}
	for _, name := range []string{"pull_secret", "cluster_settings_pull_secret", "cluster_settings_referenced_secret", "default_cluster_credentials"} {
		if _, exists := extraVars[name]; exists {
			return pullsecret.ErrInvalid
		}
	}
	for _, name := range []string{"template_parameters", "cluster_settings_template_parameters", "cluster_order"} {
		value, exists := extraVars[name]
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
		if name == "cluster_order" {
			if !reflect.DeepEqual(fields, expected) {
				return pullsecret.ErrInvalid
			}
		} else if _, exists := fields["pull_secret"]; exists {
			return pullsecret.ErrInvalid
		}
	}
	encoded, err := json.Marshal(extraVars["osac_job_vars"])
	if err != nil {
		return pullsecret.ErrInvalid
	}
	var jobVars map[string]any
	if json.Unmarshal(encoded, &jobVars) != nil || jobVars == nil {
		return pullsecret.ErrInvalid
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
