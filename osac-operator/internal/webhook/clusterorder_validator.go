/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var clusterOrderSpecKnownFields = map[string]bool{
	"templateID":         true,
	"templateParameters": true,
	"nodeRequests":       true,
	"pullSecret":         true,
	"sshPublicKey":       true,
	"releaseImage":       true,
	"network":            true,
	"networkAttachment":  true,
}

// ClusterOrderValidator rejects ClusterOrder create/update requests that
// contain unknown spec fields. Kubernetes silently prunes fields not declared
// in the CRD structural schema, which causes confusing downstream failures
// when users supply credentials under the wrong key (e.g. spec.parameters
// instead of spec.templateParameters / spec.pullSecret).
type ClusterOrderValidator struct{}

func (v *ClusterOrderValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return admission.Allowed("")
	}

	unknown, err := detectUnknownSpecFields(req.Object.Raw)
	if err != nil || len(unknown) == 0 {
		return admission.Allowed("")
	}

	sort.Strings(unknown)
	return admission.Denied(fmt.Sprintf(
		"ClusterOrder spec contains unknown fields that would be silently dropped: %s. "+
			"Valid spec fields are: templateID, templateParameters, pullSecret, sshPublicKey, "+
			"releaseImage, network, networkAttachment, nodeRequests",
		strings.Join(unknown, ", "),
	))
}

func detectUnknownSpecFields(raw []byte) ([]string, error) {
	var obj struct {
		Spec map[string]json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj.Spec == nil {
		return nil, nil
	}

	var unknown []string
	for field := range obj.Spec {
		if !clusterOrderSpecKnownFields[field] {
			unknown = append(unknown, field)
		}
	}
	return unknown, nil
}

// NewClusterOrderValidatingWebhook returns an admission.Webhook ready to be
// registered with the manager's webhook server.
func NewClusterOrderValidatingWebhook() *admission.Webhook {
	return &admission.Webhook{
		Handler: &ClusterOrderValidator{},
	}
}

// ClusterOrderValidatingWebhookPath is the URL path for the validating webhook.
const ClusterOrderValidatingWebhookPath = "/validate-osac-openshift-io-v1alpha1-clusterorder"
