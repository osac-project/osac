package consoleproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("API Discovery", func() {
	It("serves API group list at /apis", func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/apis", nil)
		req.Header.Set("Accept", "application/json")

		handleAPIGroupList(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))

		var list metav1.APIGroupList
		Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
		Expect(list.Groups).To(HaveLen(1))
		Expect(list.Groups[0].Name).To(Equal(apiGroup))
		Expect(list.Groups[0].PreferredVersion.Version).To(Equal(apiVersion))
	})

	It("serves API group", func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/apis/"+apiGroup, nil)
		req.Header.Set("Accept", "application/json")

		handleAPIGroup(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))

		var group metav1.APIGroup
		Expect(json.Unmarshal(rec.Body.Bytes(), &group)).To(Succeed())
		Expect(group.Name).To(Equal(apiGroup))
		Expect(group.Versions).To(HaveLen(1))
		Expect(group.Versions[0].Version).To(Equal(apiVersion))
	})

	It("serves API resource list", func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/apis/"+apiGroup+"/"+apiVersion, nil)
		req.Header.Set("Accept", "application/json")

		handleAPIResourceList([]metav1.APIResource{
			{Name: "computeinstances/console", Kind: "ComputeInstance", Namespaced: true, Verbs: metav1.Verbs{"get"}},
			{Name: "computeinstances/vnc", Kind: "ComputeInstance", Namespaced: true, Verbs: metav1.Verbs{"get"}},
		})(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))

		var list metav1.APIResourceList
		Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
		Expect(list.GroupVersion).To(Equal(apiGroup + "/" + apiVersion))
		Expect(list.APIResources).To(HaveLen(2))

		Expect(list.APIResources[0].Name).To(Equal("computeinstances/console"))
		Expect(list.APIResources[0].Kind).To(Equal("ComputeInstance"))
		Expect(list.APIResources[0].Namespaced).To(BeTrue())
		Expect(list.APIResources[0].Verbs).To(Equal(metav1.Verbs{"get"}))

		Expect(list.APIResources[1].Name).To(Equal("computeinstances/vnc"))
		Expect(list.APIResources[1].Kind).To(Equal("ComputeInstance"))
		Expect(list.APIResources[1].Namespaced).To(BeTrue())
		Expect(list.APIResources[1].Verbs).To(Equal(metav1.Verbs{"get"}))
	})
})
