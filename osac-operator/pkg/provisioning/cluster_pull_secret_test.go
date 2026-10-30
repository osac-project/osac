package provisioning_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	"github.com/osac-project/osac/osac-operator/pkg/pullsecret"
)

const fixtureLaunchMarker = "OSAC7034_SYNTHETIC_LAUNCH"
const fixtureAAPToken = "fixture-aap-auth"

var _ = Describe("ClusterOrder final serialized launch", func() {
	for _, workflow := range []bool{false, true} {
		variant := "job"
		if workflow {
			variant = "workflow"
		}
		for _, deprovision := range []bool{false, true} {
			for _, mode := range []string{"reference", "missing-reference", "missing-revision", "inline", "parameters", "inherited-override", "context-override", "inherited-runtime-parameters", "context-runtime-parameters", "runtime-scalar", "target-name", "settings-target-name", "nested-scalar", "nested-parameters", "nested-settings", "nested-target-name", "nested-resource-override", "unrelated-parameters"} {
				action := "create"
				if deprovision {
					action = "delete"
				}
				It("protects "+variant+" "+action+" launches with "+mode, func() {
					launched := false
					var body []byte
					var readErr error
					var launchPath string
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
						body, readErr = io.ReadAll(request.Body)
						launchPath = request.URL.Path
						_, _ = io.WriteString(writer, `{"id":42}`)
					}))
					DeferCleanup(server.Close)
					order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "fixture", Namespace: "hub", UID: "fixture-uid",
						Annotations: map[string]string{pullsecret.LastAppliedAnnotation: fixtureLaunchMarker + base64.StdEncoding.EncodeToString([]byte(fixtureLaunchMarker))}},
						Spec: v1alpha1.ClusterOrderSpec{TemplateParameters: `{}`, PullSecretRef: &corev1.LocalObjectReference{Name: "owned-secret"}, PullSecretRevision: "revision"}}
					ctx := context.Background()
					var inherited map[string]any
					switch mode {
					case "missing-reference":
						order.Spec.PullSecretRef = nil
					case "missing-revision":
						order.Spec.PullSecretRevision = ""
					case "inline":
						order.Spec.PullSecret = fixtureLaunchMarker
					case "parameters":
						order.Spec.TemplateParameters = `{"pull_secret":"` + fixtureLaunchMarker + `"}`
					case "inherited-override":
						inherited = map[string]any{"osac_job_vars": map[string]any{"resource": fixtureLaunchMarker}}
					case "context-override":
						ctx = provisioning.WithAAPExtraVars(ctx, map[string]any{"cluster_order": fixtureLaunchMarker})
					case "inherited-runtime-parameters":
						inherited = map[string]any{"template_parameters": map[string]any{"pull_secret": fixtureLaunchMarker}}
					case "context-runtime-parameters":
						ctx = provisioning.WithAAPExtraVars(ctx, map[string]any{"cluster_settings_template_parameters": map[string]any{"pull_secret": fixtureLaunchMarker}})
					case "runtime-scalar":
						inherited = map[string]any{"cluster_settings_pull_secret": fixtureLaunchMarker}
					case "target-name":
						inherited = map[string]any{"cluster_settings_pull_secret_name": fixtureLaunchMarker}
					case "settings-target-name":
						inherited = map[string]any{"hosted_cluster_settings": map[string]any{"pull_secret_name": fixtureLaunchMarker}}
					case "nested-scalar", "nested-parameters", "nested-settings", "nested-target-name", "nested-resource-override":
						encoded, err := json.Marshal(order)
						Expect(err).NotTo(HaveOccurred())
						var resource map[string]any
						Expect(json.Unmarshal(encoded, &resource)).To(Succeed())
						jobVars := map[string]any{"resource": resource}
						switch mode {
						case "nested-scalar":
							jobVars["pull_secret"] = fixtureLaunchMarker
						case "nested-parameters":
							jobVars["template_parameters"] = map[string]any{"pull_secret": fixtureLaunchMarker}
						case "nested-target-name":
							jobVars["cluster_settings_pull_secret_name"] = fixtureLaunchMarker
						case "nested-resource-override":
							jobVars["resource"] = map[string]any{"spec": map[string]any{"pullSecret": fixtureLaunchMarker}}
						default:
							jobVars["hosted_cluster_settings"] = map[string]any{"pull_secret": fixtureLaunchMarker}
						}
						inherited = map[string]any{"osac_job_vars": jobVars}
					case "unrelated-parameters":
						inherited = map[string]any{"template_parameters": map[string]any{"unrelated": map[string]any{"keep": true}}}
					}
					provider := provisioning.NewAAPProvider(aap.NewClient(server.URL, fixtureAAPToken, false), "fixture", "fixture")
					var err error
					if deprovision {
						if inherited != nil {
							ctx = provisioning.WithAAPExtraVars(ctx, inherited)
						}
						_, err = provider.TriggerDeprovision(ctx, order, nil)
					} else {
						_, err = provider.TriggerProvisionWithExtraVars(ctx, order, inherited)
					}
					ignoredContextOverride := deprovision && (mode == "inherited-override" || strings.HasPrefix(mode, "nested-"))
					if mode == "reference" || mode == "unrelated-parameters" || ignoredContextOverride || deprovision && (mode == "missing-reference" || mode == "missing-revision") {
						Expect(err).NotTo(HaveOccurred())
						Expect(launched).To(BeTrue())
						Expect(readErr).NotTo(HaveOccurred())
						if mode != "missing-reference" {
							Expect(string(body)).To(ContainSubstring("pullSecretRef"))
						}
						Expect(string(body)).NotTo(ContainSubstring(fixtureLaunchMarker))
						Expect(string(body)).NotTo(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(fixtureLaunchMarker))))
						Expect(strings.Contains(launchPath, "/workflow_job_templates/")).To(Equal(workflow))
					} else {
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).NotTo(ContainSubstring(fixtureLaunchMarker))
						Expect(launched).To(BeFalse())
					}
				})
			}
		}
	}
})
