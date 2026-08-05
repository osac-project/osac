package consoleproxy

import (
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func handleAPIGroupList(w http.ResponseWriter, r *http.Request) {
	writeObject(w, r, http.StatusOK, &metav1.APIGroupList{
		TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"},
		Groups: []metav1.APIGroup{{
			Name: apiGroup,
			Versions: []metav1.GroupVersionForDiscovery{{
				GroupVersion: apiGroup + "/" + apiVersion,
				Version:      apiVersion,
			}},
			PreferredVersion: metav1.GroupVersionForDiscovery{
				GroupVersion: apiGroup + "/" + apiVersion,
				Version:      apiVersion,
			},
		}},
	})
}

func handleAPIGroup(w http.ResponseWriter, r *http.Request) {
	writeObject(w, r, http.StatusOK, &metav1.APIGroup{
		TypeMeta: metav1.TypeMeta{Kind: "APIGroup", APIVersion: "v1"},
		Name:     apiGroup,
		Versions: []metav1.GroupVersionForDiscovery{{
			GroupVersion: apiGroup + "/" + apiVersion,
			Version:      apiVersion,
		}},
		PreferredVersion: metav1.GroupVersionForDiscovery{
			GroupVersion: apiGroup + "/" + apiVersion,
			Version:      apiVersion,
		},
	})
}

func handleAPIResourceList(resources []metav1.APIResource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeObject(w, r, http.StatusOK, &metav1.APIResourceList{
			TypeMeta:     metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"},
			GroupVersion: apiGroup + "/" + apiVersion,
			APIResources: resources,
		})
	}
}
