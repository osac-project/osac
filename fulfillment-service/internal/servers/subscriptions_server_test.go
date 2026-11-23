package servers

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/gitops"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func newTestSubscriptionsServer(t *testing.T, writer gitops.Writer) *SubscriptionsServer {
	t.Helper()
	server, err := NewSubscriptionsServer().SetLogger(slog.New(slog.DiscardHandler)).
		SetTenantsServer(subscriptionTestTenantsServer{}).
		SetWriter(writer).SetNamespace("models-as-a-service").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return server
}

type subscriptionTestTenantsServer struct {
	privatev1.UnimplementedTenantsServer
}

func (subscriptionTestTenantsServer) Get(_ context.Context, request *privatev1.TenantsGetRequest) (*privatev1.TenantsGetResponse, error) {
	return privatev1.TenantsGetResponse_builder{
		Object: privatev1.Tenant_builder{Id: request.GetId()}.Build(),
	}.Build(), nil
}

func testSubscription(id string) *publicv1.Subscription {
	return &publicv1.Subscription{
		Id:       id,
		Metadata: &publicv1.Metadata{Name: id},
		Spec: &publicv1.SubscriptionSpec{
			Organization: &publicv1.SubscriptionOrganization{Id: "tenant-a"},
			Owner:        &publicv1.SubscriptionOwner{Groups: []string{"developers"}, Users: []string{"sa-user"}},
			ModelRefs:    []*publicv1.SubscriptionModelRef{{Name: "granite-3b", TokenRateLimits: []*publicv1.TokenRateLimit{{Limit: 1000, Window: "1m"}}}},
		},
	}
}

func TestSubscriptionsCRUDWritesManagedPolicyAndRevokesOnDelete(t *testing.T) {
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(map[string][]byte{
		modelDeploymentPath("granite-3b"): []byte("apiVersion: v1\nkind: ConfigMap\n"),
		modelCatalogPath("granite-3b"):    []byte("apiVersion: maas.opendatahub.io/v1alpha1\nkind: MaaSModelRef\nmetadata:\n  name: granite-3b\n  namespace: models-as-a-service\n  annotations:\n    openshift.io/display-name: Granite 3B\n    openshift.io/description: Granite model\n"),
	})
	server := newTestSubscriptionsServer(t, writer)
	created, err := server.Create(ctx, &publicv1.SubscriptionsCreateRequest{Object: testSubscription("tenant-a-access")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.GetObject().GetMetadata().GetVersion() != 1 {
		t.Fatalf("created version = %d, want 1", created.GetObject().GetMetadata().GetVersion())
	}
	if created.GetAuthPolicy().GetId() != "tenant-a-access-policy" {
		t.Fatalf("managed policy ID = %q", created.GetAuthPolicy().GetId())
	}

	head, err := writer.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, err := head.Read(subscriptionGitPath("tenant-a-access"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tenant-a/developers") {
		t.Fatalf("manifest does not contain Tenant-qualified group: %s", data)
	}
	if strings.Contains(string(data), apiKeyRetainOnDeleteAnnotation) {
		t.Fatalf("manifest disables the five-minute revocation target: %s", data)
	}
	if !strings.Contains(string(data), "kind: MaaSAuthPolicy") {
		t.Fatalf("manifest does not include the managed auth policy: %s", data)
	}

	got, err := server.Get(ctx, &publicv1.SubscriptionsGetRequest{Id: "tenant-a-access"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.GetAuthPolicies()) != 1 || len(got.GetModelRegistrations()) != 1 {
		t.Fatalf("Get related objects = %d policies, %d registrations", len(got.GetAuthPolicies()), len(got.GetModelRegistrations()))
	}
	if got.GetObject().GetSpec().GetOwner().GetGroups()[0] != "developers" {
		t.Fatalf("read group is not de-qualified: %v", got.GetObject().GetSpec().GetOwner().GetGroups())
	}
	modelRef := got.GetObject().GetSpec().GetModelRefs()[0]
	if modelRef.GetModelId() != "granite-3b" || modelRef.GetDisplayName() != "Granite 3B" || modelRef.GetDescription() != "Granite model" {
		t.Fatalf("model catalog enrichment = id %q, display name %q, description %q", modelRef.GetModelId(), modelRef.GetDisplayName(), modelRef.GetDescription())
	}

	mask, err := fieldmaskpb.New(&publicv1.Subscription{}, "spec.priority")
	if err != nil {
		t.Fatal(err)
	}
	updated := testSubscription("tenant-a-access")
	updated.Metadata.Version = got.GetObject().GetMetadata().GetVersion()
	updated.Spec.Priority = 8
	response, err := server.Update(ctx, &publicv1.SubscriptionsUpdateRequest{Object: updated, UpdateMask: mask, Lock: true})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if response.GetObject().GetSpec().GetPriority() != 8 || response.GetObject().GetMetadata().GetVersion() != 2 {
		t.Fatalf("updated priority=%d version=%d", response.GetObject().GetSpec().GetPriority(), response.GetObject().GetMetadata().GetVersion())
	}

	if _, err := server.Delete(ctx, &publicv1.SubscriptionsDeleteRequest{Id: "tenant-a-access", ResourceVersion: 1}); status.Code(err) != codes.Aborted {
		t.Fatalf("stale Delete error = %v, want Aborted", err)
	}
	if _, err := server.Delete(ctx, &publicv1.SubscriptionsDeleteRequest{Id: "tenant-a-access", ResourceVersion: 2}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := server.Get(ctx, &publicv1.SubscriptionsGetRequest{Id: "tenant-a-access"}); status.Code(err) != codes.NotFound {
		t.Fatalf("Get after Delete error = %v, want NotFound", err)
	}
}

func TestSubscriptionsRequireExistingModelAndPreventModelDeletion(t *testing.T) {
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(nil)
	server := newTestSubscriptionsServer(t, writer)
	if _, err := server.Create(ctx, &publicv1.SubscriptionsCreateRequest{Object: testSubscription("tenant-a-access")}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Create missing model error = %v, want FailedPrecondition", err)
	}

	writer = gitops.NewMemoryWriter(map[string][]byte{modelDeploymentPath("granite-3b"): []byte("kind: ConfigMap\n")})
	server = newTestSubscriptionsServer(t, writer)
	if _, err := server.Create(ctx, &publicv1.SubscriptionsCreateRequest{Object: testSubscription("tenant-a-access")}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	head, err := writer.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = ensureModelNotSubscribed(head, "granite-3b", defaultSubscriptionNamespace)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ensureModelNotSubscribed error = %v, want FailedPrecondition", err)
	}
}

func TestSubscriptionsListFiltersOrdersAndPaginates(t *testing.T) {
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(map[string][]byte{modelDeploymentPath("granite-3b"): []byte("kind: ConfigMap\n")})
	server := newTestSubscriptionsServer(t, writer)
	for _, test := range []struct {
		id       string
		priority int32
	}{{"tenant-a-low", 1}, {"tenant-a-high", 9}} {
		object := testSubscription(test.id)
		object.Spec.Priority = test.priority
		if _, err := server.Create(ctx, &publicv1.SubscriptionsCreateRequest{Object: object}); err != nil {
			t.Fatal(err)
		}
	}
	filter, order := "this.spec.priority >= 5", "-priority"
	limit := int32(1)
	page, err := server.List(ctx, &publicv1.SubscriptionsListRequest{Filter: &filter, Order: &order, Limit: &limit})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.GetTotal() != 1 || page.GetSize() != 1 || len(page.GetItems()) == 0 || page.GetItems()[0].GetId() != "tenant-a-high" {
		var ids []string
		for _, item := range page.GetItems() {
			ids = append(ids, item.GetId())
		}
		t.Fatalf("List response total=%d size=%d IDs=%v", page.GetTotal(), page.GetSize(), ids)
	}
}
