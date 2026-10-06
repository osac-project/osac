package trustadmission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
)

const (
	ControlPath            = "/control/v1/records"
	ControlTokenHeader     = "X-OSAC-Trust-Token"
	ControlServiceName     = "osac-fulfillment-trust-admission"
	publisherNamePrefix    = "osac-trust-publisher-"
	maxControlRequestBytes = 1024 * 1024
	maxRecordLifetime      = 10 * time.Minute
)

// PublisherServiceAccountName binds a publisher identity to exactly one order.
func PublisherServiceAccountName(orderUID string) (string, error) {
	name := publisherNamePrefix + orderUID
	if len(name) > 63 || len(validation.IsDNS1123Label(orderUID)) != 0 {
		return "", fmt.Errorf("invalid ClusterOrder UID for publisher identity")
	}
	return name, nil
}

type ControlPublishRequest struct {
	Record ExpectedBundle `json:"record"`
}

type ControlRevokeRequest struct {
	Key RecordKey `json:"key"`
}

// ControlHandler authenticates a per-order publisher before changing records.
// The token is supplied separately from the Kubernetes API proxy's own auth.
type ControlHandler struct {
	Store     Store
	Kube      kubernetes.Interface
	Namespace string
	Now       func() time.Time
}

func (h *ControlHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h.Store == nil || h.Kube == nil || h.Namespace == "" {
		http.Error(writer, "control channel unavailable", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost && request.Method != http.MethodDelete {
		writer.Header().Set("Allow", "POST, DELETE")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get("Content-Type") != "application/json" {
		http.Error(writer, "application/json required", http.StatusUnsupportedMediaType)
		return
	}

	var key RecordKey
	var record ExpectedBundle
	if request.Method == http.MethodPost {
		var payload ControlPublishRequest
		if err := decodeControlRequest(writer, request, &payload); err != nil {
			http.Error(writer, "invalid control request", http.StatusBadRequest)
			return
		}
		record = payload.Record
		key = record.Key
	} else {
		var payload ControlRevokeRequest
		if err := decodeControlRequest(writer, request, &payload); err != nil {
			http.Error(writer, "invalid control request", http.StatusBadRequest)
			return
		}
		key = payload.Key
	}
	if key.TenantNamespace != h.Namespace || key.ConfigMapName != ConfigMapName || key.BundleSHA256 == "" {
		http.Error(writer, "record outside control scope", http.StatusForbidden)
		return
	}
	name, err := PublisherServiceAccountName(key.ClusterOrderUID)
	if err != nil {
		http.Error(writer, "record outside control scope", http.StatusForbidden)
		return
	}
	if request.Method == http.MethodPost &&
		(record.Tenant == "" || record.OwnerReference != key.ClusterOrderUID) {
		http.Error(writer, "record outside control scope", http.StatusForbidden)
		return
	}
	if !h.authorized(request, name, key.ClusterOrderUID, record.Tenant) {
		http.Error(writer, "publisher not authorized", http.StatusForbidden)
		return
	}
	if request.Method == http.MethodPost {
		now := time.Now()
		if h.Now != nil {
			now = h.Now()
		}
		if !record.ExpiresAt.After(now) || record.ExpiresAt.After(now.Add(maxRecordLifetime)) {
			http.Error(writer, "invalid record lifetime", http.StatusBadRequest)
			return
		}
		if err := h.Store.Publish(request.Context(), record); err != nil {
			http.Error(writer, "expected bundle publication failed", http.StatusServiceUnavailable)
			return
		}
	} else if err := h.Store.Revoke(request.Context(), key); err != nil {
		http.Error(writer, "expected bundle revocation failed", http.StatusServiceUnavailable)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func decodeControlRequest(writer http.ResponseWriter, request *http.Request, result any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxControlRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing control request data")
	}
	return nil
}

func (h *ControlHandler) authorized(request *http.Request, name, orderUID, tenant string) bool {
	token := request.Header.Get(ControlTokenHeader)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return false
	}
	ctx, cancel := context.WithTimeout(request.Context(), storeRequestTimeout)
	defer cancel()

	review, err := h.Kube.AuthenticationV1().TokenReviews().Create(ctx,
		&authenticationv1.TokenReview{Spec: authenticationv1.TokenReviewSpec{Token: token}}, metav1.CreateOptions{})
	if err != nil || !review.Status.Authenticated || review.Status.Error != "" ||
		review.Status.User.Username != "system:serviceaccount:"+h.Namespace+":"+name {
		return false
	}
	account, err := h.Kube.CoreV1().ServiceAccounts(h.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil || review.Status.User.UID == "" || review.Status.User.UID != string(account.UID) ||
		account.Annotations[OwnerReferenceAnnotation] != orderUID || account.Annotations[TenantAnnotation] == "" {
		return false
	}
	return tenant == "" || account.Annotations[TenantAnnotation] == tenant
}
