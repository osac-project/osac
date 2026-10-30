package provisioning

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/pullsecret"
)

func TestClusterOrderSerializedLaunch(test *testing.T) {
	for _, workflow := range []bool{false, true} {
		name := "job"
		if workflow {
			name = "workflow"
		}
		test.Run(name, func(test *testing.T) {
			for _, mode := range []string{"reference", "inline", "parameters", "inherited-override", "context-override", "inherited-runtime-parameters", "context-runtime-parameters", "inherited-order-override", "context-order-override", "runtime-scalar", "unrelated-parameters"} {
				test.Run(mode, func(test *testing.T) {
					launched := false
					server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
						writer.Header().Set("Content-Type", "application/json")
						if request.Method == http.MethodGet {
							if workflow && strings.Contains(request.URL.Path, "/job_templates/") {
								_, _ = io.WriteString(writer, `{"count":0,"results":[]}`)
							} else {
								_, _ = io.WriteString(writer, `{"count":1,"results":[{"id":1,"name":"fixture"}]}`)
							}
							return
						}
						launched = true
						body, err := io.ReadAll(request.Body)
						if err != nil || strings.Contains(string(body), "OSAC7034_SYNTHETIC") ||
							strings.Contains(string(body), base64.StdEncoding.EncodeToString([]byte("OSAC7034_SYNTHETIC_OLD_ANNOTATION"))) {
							test.Error("serialized launch contains credential material or is unreadable")
						}
						var payload map[string]any
						if json.Unmarshal(body, &payload) != nil || !strings.Contains(string(body), "pullSecretRef") {
							test.Error("launch lost its reference")
						}
						if workflow != strings.Contains(request.URL.Path, "/workflow_job_templates/") {
							test.Error("wrong launch variant")
						}
						_, _ = io.WriteString(writer, `{"id":42}`)
					}))
					defer server.Close()
					order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
						Name: "fixture", Namespace: "hub", UID: "fixture-uid",
						Annotations: map[string]string{pullsecret.LastAppliedAnnotation: base64.StdEncoding.EncodeToString([]byte("OSAC7034_SYNTHETIC_OLD_ANNOTATION"))},
					}, Spec: v1alpha1.ClusterOrderSpec{
						TemplateParameters: `{}`, PullSecretRef: &corev1.LocalObjectReference{Name: "owned-secret"}, PullSecretRevision: "revision",
					}}
					ctx := context.Background()
					var inherited map[string]any
					switch mode {
					case "inline":
						order.Spec.PullSecret = "OSAC7034_SYNTHETIC_INLINE"
					case "parameters":
						order.Spec.TemplateParameters = `{"pull_secret":"OSAC7034_SYNTHETIC_PARAMETERS"}`
					case "inherited-override":
						inherited = map[string]any{"osac_job_vars": map[string]any{"resource": map[string]any{"spec": map[string]any{"pullSecret": "OSAC7034_SYNTHETIC_OVERRIDE"}}}}
					case "context-override":
						ctx = WithAAPExtraVars(ctx, map[string]any{"osac_job_vars": map[string]any{"resource": "OSAC7034_SYNTHETIC_CONTEXT"}})
					case "inherited-runtime-parameters":
						inherited = map[string]any{"template_parameters": map[string]any{"pull_secret": "OSAC7034_SYNTHETIC_PARAMETERS"}}
					case "context-runtime-parameters":
						ctx = WithAAPExtraVars(ctx, map[string]any{"cluster_settings_template_parameters": map[string]any{"pull_secret": "OSAC7034_SYNTHETIC_PARAMETERS"}})
					case "inherited-order-override":
						inherited = map[string]any{"cluster_order": map[string]any{"spec": map[string]any{"pullSecret": "OSAC7034_SYNTHETIC_OVERRIDE"}}}
					case "context-order-override":
						ctx = WithAAPExtraVars(ctx, map[string]any{"cluster_order": map[string]any{"spec": map[string]any{"pullSecret": "OSAC7034_SYNTHETIC_OVERRIDE"}}})
					case "runtime-scalar":
						inherited = map[string]any{"cluster_settings_pull_secret": "OSAC7034_SYNTHETIC_PARAMETERS"}
					case "unrelated-parameters":
						inherited = map[string]any{"template_parameters": map[string]any{"unrelated": "keep"}}
					}
					provider := NewAAPProvider(aap.NewClient(server.URL, "synthetic-test-token", false), "fixture", "fixture")
					_, err := provider.TriggerProvisionWithExtraVars(ctx, order, inherited)
					switch mode {
					case "reference", "context-override", "unrelated-parameters":
						if err != nil || !launched {
							test.Fatalf("reference launch failed: %v", err)
						}
					default:
						if err == nil || launched || strings.Contains(err.Error(), "OSAC7034_SYNTHETIC") {
							test.Fatal("unsafe input was not rejected before launch")
						}
					}
				})
			}
		})
	}
}
