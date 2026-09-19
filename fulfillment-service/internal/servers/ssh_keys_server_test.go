/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type mockPrivateSshKeysServer struct {
	privatev1.UnimplementedSshKeysServer

	createFunc func(context.Context, *privatev1.SshKeysCreateRequest) (*privatev1.SshKeysCreateResponse, error)
	listFunc   func(context.Context, *privatev1.SshKeysListRequest) (*privatev1.SshKeysListResponse, error)
	getFunc    func(context.Context, *privatev1.SshKeysGetRequest) (*privatev1.SshKeysGetResponse, error)
	deleteFunc func(context.Context, *privatev1.SshKeysDeleteRequest) (*privatev1.SshKeysDeleteResponse, error)
}

func (m *mockPrivateSshKeysServer) Create(ctx context.Context, req *privatev1.SshKeysCreateRequest) (*privatev1.SshKeysCreateResponse, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}
	return m.UnimplementedSshKeysServer.Create(ctx, req)
}

func (m *mockPrivateSshKeysServer) List(ctx context.Context, req *privatev1.SshKeysListRequest) (*privatev1.SshKeysListResponse, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, req)
	}
	return m.UnimplementedSshKeysServer.List(ctx, req)
}

func (m *mockPrivateSshKeysServer) Get(ctx context.Context, req *privatev1.SshKeysGetRequest) (*privatev1.SshKeysGetResponse, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, req)
	}
	return m.UnimplementedSshKeysServer.Get(ctx, req)
}

func (m *mockPrivateSshKeysServer) Delete(ctx context.Context, req *privatev1.SshKeysDeleteRequest) (*privatev1.SshKeysDeleteResponse, error) {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, req)
	}
	return m.UnimplementedSshKeysServer.Delete(ctx, req)
}

func newTestSshKeysServer(mock privatev1.SshKeysServer) *SshKeysServer {
	inMapper, err := NewGenericMapper[*publicv1.SshKey, *privatev1.SshKey]().
		SetLogger(logger).
		SetStrict(true).
		Build()
	Expect(err).ToNot(HaveOccurred())

	outMapper, err := NewGenericMapper[*privatev1.SshKey, *publicv1.SshKey]().
		SetLogger(logger).
		SetStrict(false).
		Build()
	Expect(err).ToNot(HaveOccurred())

	return &SshKeysServer{
		logger:    logger,
		private:   mock,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
}

var _ = Describe("SSH keys server", func() {
	It("implements the public service", func() {
		var _ publicv1.SshKeysServer = (*SshKeysServer)(nil)
	})

	It("maps public Create requests to the private service", func() {
		var captured *privatev1.SshKeysCreateRequest
		mock := &mockPrivateSshKeysServer{
			createFunc: func(_ context.Context, req *privatev1.SshKeysCreateRequest) (*privatev1.SshKeysCreateResponse, error) {
				captured = req
				return privatev1.SshKeysCreateResponse_builder{
					Object: privatev1.SshKey_builder{
						Id: "key-1",
						Metadata: privatev1.Metadata_builder{
							Name:   "my-key",
							Tenant: "tenant-a",
						}.Build(),
						Spec: privatev1.SshKeySpec_builder{PublicKey: validSshKeyForTest}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		server := newTestSshKeysServer(mock)
		response, err := server.Create(ctx, publicv1.SshKeysCreateRequest_builder{
			Object: publicv1.SshKey_builder{
				Metadata: publicv1.Metadata_builder{Name: "my-key"}.Build(),
				Spec:     publicv1.SshKeySpec_builder{PublicKey: validSshKeyForTest}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(captured.GetObject().GetSpec().GetPublicKey()).To(Equal(validSshKeyForTest))
		Expect(response.GetObject().GetId()).To(Equal("key-1"))
		Expect(response.GetObject().GetMetadata().GetTenant()).To(Equal("tenant-a"))
	})

	It("forwards List parameters and maps returned objects", func() {
		var captured *privatev1.SshKeysListRequest
		mock := &mockPrivateSshKeysServer{
			listFunc: func(_ context.Context, req *privatev1.SshKeysListRequest) (*privatev1.SshKeysListResponse, error) {
				captured = req
				return privatev1.SshKeysListResponse_builder{
					Size:  1,
					Total: 1,
					Items: []*privatev1.SshKey{{
						Id:       "key-1",
						Metadata: privatev1.Metadata_builder{Name: "my-key"}.Build(),
						Spec:     privatev1.SshKeySpec_builder{PublicKey: validSshKeyForTest}.Build(),
					}},
				}.Build(), nil
			},
		}

		server := newTestSshKeysServer(mock)
		response, err := server.List(ctx, publicv1.SshKeysListRequest_builder{
			Offset: new(int32(2)),
			Limit:  new(int32(5)),
			Filter: new("this.metadata.name == 'my-key'"),
			Order:  new("metadata.name asc"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(captured.GetOffset()).To(Equal(int32(2)))
		Expect(captured.GetLimit()).To(Equal(int32(5)))
		Expect(captured.GetFilter()).To(Equal("this.metadata.name == 'my-key'"))
		Expect(captured.GetOrder()).To(Equal("metadata.name asc"))
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetSpec().GetPublicKey()).To(Equal(validSshKeyForTest))
	})

	It("delegates Get and Delete", func() {
		var gotID, deletedID string
		mock := &mockPrivateSshKeysServer{
			getFunc: func(_ context.Context, req *privatev1.SshKeysGetRequest) (*privatev1.SshKeysGetResponse, error) {
				gotID = req.GetId()
				return privatev1.SshKeysGetResponse_builder{
					Object: privatev1.SshKey_builder{Id: req.GetId()}.Build(),
				}.Build(), nil
			},
			deleteFunc: func(_ context.Context, req *privatev1.SshKeysDeleteRequest) (*privatev1.SshKeysDeleteResponse, error) {
				deletedID = req.GetId()
				return &privatev1.SshKeysDeleteResponse{}, nil
			},
		}

		server := newTestSshKeysServer(mock)
		_, err := server.Get(ctx, publicv1.SshKeysGetRequest_builder{Id: "key-1"}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = server.Delete(ctx, publicv1.SshKeysDeleteRequest_builder{Id: "key-1"}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(gotID).To(Equal("key-1"))
		Expect(deletedID).To(Equal("key-1"))
	})
})
