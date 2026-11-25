package authgate

import (
	"context"
	"errors"
	"fmt"

	authv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// GroupResource is the OpenShift group membership API.
var GroupResource = schema.GroupVersionResource{Group: "user.openshift.io", Version: "v1", Resource: "groups"}

// OpenShiftAuthenticator validates an OpenShift OAuth token with a
// TokenReview, then checks membership of an existing group. OSAC never creates
// the group.
type OpenShiftAuthenticator struct {
	Client  kubernetes.Interface
	Dynamic dynamic.Interface
	Group   string
}

// Authenticate returns nil for a member of the configured group, ErrForbidden
// for an authenticated non-member, and ErrUnauthenticated for a token the API
// server does not accept.
func (a *OpenShiftAuthenticator) Authenticate(ctx context.Context, token string) error {
	if a.Group == "" {
		return errors.New("OPENSHIFT_GROUP is required")
	}
	review, err := a.Client.AuthenticationV1().TokenReviews().Create(ctx,
		&authv1.TokenReview{Spec: authv1.TokenReviewSpec{Token: token}},
		metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("token review: %w", err)
	}
	if !review.Status.Authenticated {
		return ErrUnauthenticated
	}
	username := review.Status.User.Username
	// The API server may already resolve OpenShift group membership into the
	// TokenReview response; trust that before a second API call.
	if contains(review.Status.User.Groups, a.Group) {
		return nil
	}
	group, err := a.Dynamic.Resource(GroupResource).Get(ctx, a.Group, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("read group %s: %w", a.Group, err)
	}
	users, _, err := unstructured.NestedStringSlice(group.Object, "users")
	if err != nil {
		return fmt.Errorf("read group %s users: %w", a.Group, err)
	}
	if contains(users, username) {
		return nil
	}
	return ErrForbidden
}
