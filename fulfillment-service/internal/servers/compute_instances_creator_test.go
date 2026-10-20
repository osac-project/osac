package servers

import (
	"context"
	"log/slog"
	"testing"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type creatorTestComputeServer struct {
	privatev1.ComputeInstancesServer
	object        *privatev1.ComputeInstance
	receivedWrite *privatev1.ComputeInstance
}

func (s *creatorTestComputeServer) Get(context.Context, *privatev1.ComputeInstancesGetRequest) (*privatev1.ComputeInstancesGetResponse, error) {
	response := &privatev1.ComputeInstancesGetResponse{}
	response.SetObject(proto.Clone(s.object).(*privatev1.ComputeInstance))
	return response, nil
}

func (s *creatorTestComputeServer) List(context.Context, *privatev1.ComputeInstancesListRequest) (*privatev1.ComputeInstancesListResponse, error) {
	response := &privatev1.ComputeInstancesListResponse{}
	response.SetItems([]*privatev1.ComputeInstance{s.object, s.object})
	return response, nil
}

func (s *creatorTestComputeServer) Create(_ context.Context, request *privatev1.ComputeInstancesCreateRequest) (*privatev1.ComputeInstancesCreateResponse, error) {
	s.receivedWrite = proto.Clone(request.GetObject()).(*privatev1.ComputeInstance)
	response := &privatev1.ComputeInstancesCreateResponse{}
	response.SetObject(proto.Clone(s.object).(*privatev1.ComputeInstance))
	return response, nil
}

func (s *creatorTestComputeServer) Update(_ context.Context, request *privatev1.ComputeInstancesUpdateRequest) (*privatev1.ComputeInstancesUpdateResponse, error) {
	s.receivedWrite = proto.Clone(request.GetObject()).(*privatev1.ComputeInstance)
	response := &privatev1.ComputeInstancesUpdateResponse{}
	response.SetObject(proto.Clone(s.object).(*privatev1.ComputeInstance))
	return response, nil
}

type creatorTestUsersServer struct {
	privatev1.UsersServer
	user  *privatev1.User
	calls int
}

func (s *creatorTestUsersServer) Get(_ context.Context, request *privatev1.UsersGetRequest) (*privatev1.UsersGetResponse, error) {
	s.calls++
	if s.user == nil || request.GetId() != s.user.GetId() {
		return nil, grpcstatus.Error(grpccodes.NotFound, "user not found")
	}
	response := &privatev1.UsersGetResponse{}
	response.SetObject(s.user)
	return response, nil
}

func TestComputeInstanceCreatorProjection(t *testing.T) {
	const creatorID = "08c318fc-b83f-4c37-9d2f-2e4854a2db34"
	const spoofedName = "spoofed"

	for _, tc := range []struct {
		name         string
		creator      string
		vmTenant     string
		userTenant   string
		username     string
		expectedName string
	}{
		{name: "resolved user", creator: creatorID, vmTenant: "team-a", userTenant: "team-a", username: "alice", expectedName: "alice"},
		{name: "foreign user", creator: creatorID, vmTenant: "team-a", userTenant: "team-b", username: "bob"},
		{name: "deleted user", creator: creatorID, vmTenant: "team-a"},
		{name: "missing tenant", creator: creatorID, userTenant: "team-a", username: "alice"},
		{name: "service account", creator: "automation", vmTenant: "team-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := privatev1.ComputeInstance_builder{
				Id: "vm-1",
				Metadata: privatev1.Metadata_builder{
					Name:        "vm-1",
					Tenant:      tc.vmTenant,
					Creator:     tc.creator,
					Annotations: map[string]string{creatorUsernameAnnotation: spoofedName},
				}.Build(),
			}.Build()
			var user *privatev1.User
			if tc.username != "" {
				user = privatev1.User_builder{
					Id: creatorID,
					Metadata: privatev1.Metadata_builder{
						Name:   tc.username,
						Tenant: tc.userTenant,
					}.Build(),
					Spec: privatev1.UserSpec_builder{Username: tc.username}.Build(),
				}.Build()
			}
			mapper, err := NewGenericMapper[*privatev1.ComputeInstance, *publicv1.ComputeInstance]().
				SetLogger(slog.Default()).SetStrict(false).Build()
			if err != nil {
				t.Fatal(err)
			}
			users := &creatorTestUsersServer{user: user}
			server := &ComputeInstancesServer{
				logger:    slog.Default(),
				delegate:  &creatorTestComputeServer{object: object},
				users:     users,
				outMapper: mapper,
			}
			getResponse, err := server.Get(context.Background(), &publicv1.ComputeInstancesGetRequest{Id: "vm-1"})
			if err != nil {
				t.Fatal(err)
			}
			listResponse, err := server.List(context.Background(), &publicv1.ComputeInstancesListRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if len(listResponse.GetItems()) != 2 {
				t.Fatalf("list returned %d items, want 2", len(listResponse.GetItems()))
			}
			result := getResponse.GetObject()
			if got := result.GetMetadata().GetCreator(); got != tc.creator {
				t.Errorf("Get creator = %q, want %q", got, tc.creator)
			}
			if got := result.GetMetadata().GetAnnotations()[creatorUsernameAnnotation]; got != tc.expectedName {
				t.Errorf("Get creator username = %q, want %q", got, tc.expectedName)
			}
			if _, present := result.GetMetadata().GetAnnotations()[creatorUsernameAnnotation]; present != (tc.expectedName != "") {
				t.Errorf("Get creator username annotation present = %t, want %t", present, tc.expectedName != "")
			}
			for _, result := range listResponse.GetItems() {
				if got := result.GetMetadata().GetCreator(); got != tc.creator {
					t.Errorf("List creator = %q, want %q", got, tc.creator)
				}
				if _, present := result.GetMetadata().GetAnnotations()[creatorUsernameAnnotation]; present {
					t.Error("List exposed a creator username annotation")
				}
			}
			if got := object.GetMetadata().GetAnnotations()[creatorUsernameAnnotation]; got != spoofedName {
				t.Errorf("stored annotation changed to %q", got)
			}
			expectedCalls := 1 // Only Get resolves a creator.
			if tc.creator == "automation" || tc.vmTenant == "" {
				expectedCalls = 0
			}
			if users.calls != expectedCalls {
				t.Errorf("user lookups = %d, want %d", users.calls, expectedCalls)
			}
		})
	}
}

func TestComputeInstanceCreatorAnnotationIsServerControlled(t *testing.T) {
	const otherAnnotation = "example.com/note"
	annotations := map[string]string{
		creatorUsernameAnnotation: "forged",
		otherAnnotation:           "keep",
	}
	stored := privatev1.ComputeInstance_builder{
		Id: "vm-1",
		Metadata: privatev1.Metadata_builder{
			Name:        "vm-1",
			Tenant:      "team-a",
			Annotations: annotations,
		}.Build(),
	}.Build()
	input := publicv1.ComputeInstance_builder{
		Id: "vm-1",
		Metadata: publicv1.Metadata_builder{
			Name:        "vm-1",
			Tenant:      "team-a",
			Annotations: annotations,
		}.Build(),
	}.Build()
	for _, tc := range []struct {
		name       string
		create     bool
		updateMask *fieldmaskpb.FieldMask
	}{
		{name: "create", create: true},
		{name: "masked update", updateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.annotations"}}},
		{name: "maskless update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inMapper, err := NewGenericMapper[*publicv1.ComputeInstance, *privatev1.ComputeInstance]().
				SetLogger(slog.Default()).SetStrict(true).Build()
			if err != nil {
				t.Fatal(err)
			}
			outMapper, err := NewGenericMapper[*privatev1.ComputeInstance, *publicv1.ComputeInstance]().
				SetLogger(slog.Default()).SetStrict(false).Build()
			if err != nil {
				t.Fatal(err)
			}
			delegate := &creatorTestComputeServer{object: stored}
			server := &ComputeInstancesServer{
				logger:    slog.Default(),
				delegate:  delegate,
				inMapper:  inMapper,
				outMapper: outMapper,
			}
			var result *publicv1.ComputeInstance
			if tc.create {
				response, err := server.Create(context.Background(), publicv1.ComputeInstancesCreateRequest_builder{Object: input}.Build())
				if err != nil {
					t.Fatal(err)
				}
				result = response.GetObject()
			} else {
				response, err := server.Update(context.Background(), publicv1.ComputeInstancesUpdateRequest_builder{
					Object: input, UpdateMask: tc.updateMask,
				}.Build())
				if err != nil {
					t.Fatal(err)
				}
				result = response.GetObject()
			}
			if _, present := delegate.receivedWrite.GetMetadata().GetAnnotations()[creatorUsernameAnnotation]; present {
				t.Error("write request persisted a caller-supplied creator username")
			}
			if got := delegate.receivedWrite.GetMetadata().GetAnnotations()[otherAnnotation]; got != "keep" {
				t.Errorf("write request lost unrelated annotation: %q", got)
			}
			if _, present := result.GetMetadata().GetAnnotations()[creatorUsernameAnnotation]; present {
				t.Error("write response exposed a stored creator username")
			}
			if got := result.GetMetadata().GetAnnotations()[otherAnnotation]; got != "keep" {
				t.Errorf("write response lost unrelated annotation: %q", got)
			}
		})
	}
}
