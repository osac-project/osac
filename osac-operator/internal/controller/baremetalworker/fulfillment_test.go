/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestBareMetalWorker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BareMetalWorker Suite")
}

// errBackend is the canned error returned by the fakes when configured to fail.
var errBackend = errors.New("backend boom")

// expectCallDeadline asserts the given context carries a deadline within (25s, 30s].
func expectCallDeadline(ctx context.Context) {
	GinkgoHelper()
	deadline, ok := ctx.Deadline()
	Expect(ok).To(BeTrue(), "call context must carry a deadline")
	remaining := time.Until(deadline)
	Expect(remaining).To(BeNumerically(">", 25*time.Second))
	Expect(remaining).To(BeNumerically("<=", 30*time.Second))
}

// fakeBMIClient is a hand-written stub of privatev1.BareMetalInstancesClient. It captures the
// context of the last call and returns configurable objects/errors.
type fakeBMIClient struct {
	lastCtx context.Context
	err     error
	object  *privatev1.BareMetalInstance
	items   []*privatev1.BareMetalInstance
}

func (f *fakeBMIClient) List(
	ctx context.Context, _ *privatev1.BareMetalInstancesListRequest, _ ...grpc.CallOption,
) (*privatev1.BareMetalInstancesListResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.BareMetalInstancesListResponse_builder{Items: f.items}.Build(), nil
}

func (f *fakeBMIClient) Get(
	ctx context.Context, _ *privatev1.BareMetalInstancesGetRequest, _ ...grpc.CallOption,
) (*privatev1.BareMetalInstancesGetResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.BareMetalInstancesGetResponse_builder{Object: f.object}.Build(), nil
}

func (f *fakeBMIClient) Create(
	ctx context.Context, _ *privatev1.BareMetalInstancesCreateRequest, _ ...grpc.CallOption,
) (*privatev1.BareMetalInstancesCreateResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.BareMetalInstancesCreateResponse_builder{Object: f.object}.Build(), nil
}

func (f *fakeBMIClient) Delete(
	ctx context.Context, _ *privatev1.BareMetalInstancesDeleteRequest, _ ...grpc.CallOption,
) (*privatev1.BareMetalInstancesDeleteResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return &privatev1.BareMetalInstancesDeleteResponse{}, nil
}

func (f *fakeBMIClient) Update(
	context.Context, *privatev1.BareMetalInstancesUpdateRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstancesUpdateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBMIClient) Signal(
	context.Context, *privatev1.BareMetalInstancesSignalRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstancesSignalResponse, error) {
	return nil, errors.New("not implemented")
}

// fakeClustersClient is a hand-written stub of privatev1.ClustersClient.
type fakeClustersClient struct {
	lastCtx context.Context
	err     error
	object  *privatev1.Cluster
}

func (f *fakeClustersClient) Get(
	ctx context.Context, _ *privatev1.ClustersGetRequest, _ ...grpc.CallOption,
) (*privatev1.ClustersGetResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.ClustersGetResponse_builder{Object: f.object}.Build(), nil
}

func (f *fakeClustersClient) List(
	context.Context, *privatev1.ClustersListRequest, ...grpc.CallOption,
) (*privatev1.ClustersListResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClustersClient) Create(
	context.Context, *privatev1.ClustersCreateRequest, ...grpc.CallOption,
) (*privatev1.ClustersCreateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClustersClient) Delete(
	context.Context, *privatev1.ClustersDeleteRequest, ...grpc.CallOption,
) (*privatev1.ClustersDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClustersClient) Update(
	context.Context, *privatev1.ClustersUpdateRequest, ...grpc.CallOption,
) (*privatev1.ClustersUpdateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClustersClient) Signal(
	context.Context, *privatev1.ClustersSignalRequest, ...grpc.CallOption,
) (*privatev1.ClustersSignalResponse, error) {
	return nil, errors.New("not implemented")
}

// fakeCVClient is a hand-written stub of privatev1.ClusterVersionsClient.
type fakeCVClient struct {
	lastCtx context.Context
	err     error
	object  *privatev1.ClusterVersion
}

func (f *fakeCVClient) Get(
	ctx context.Context, _ *privatev1.ClusterVersionsGetRequest, _ ...grpc.CallOption,
) (*privatev1.ClusterVersionsGetResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.ClusterVersionsGetResponse_builder{Object: f.object}.Build(), nil
}

func (f *fakeCVClient) List(
	context.Context, *privatev1.ClusterVersionsListRequest, ...grpc.CallOption,
) (*privatev1.ClusterVersionsListResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeCVClient) Create(
	context.Context, *privatev1.ClusterVersionsCreateRequest, ...grpc.CallOption,
) (*privatev1.ClusterVersionsCreateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeCVClient) Update(
	context.Context, *privatev1.ClusterVersionsUpdateRequest, ...grpc.CallOption,
) (*privatev1.ClusterVersionsUpdateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeCVClient) Delete(
	context.Context, *privatev1.ClusterVersionsDeleteRequest, ...grpc.CallOption,
) (*privatev1.ClusterVersionsDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeCVClient) Signal(
	context.Context, *privatev1.ClusterVersionsSignalRequest, ...grpc.CallOption,
) (*privatev1.ClusterVersionsSignalResponse, error) {
	return nil, errors.New("not implemented")
}

// fakeDiskImagesClient is a hand-written stub of privatev1.DiskImagesClient.
type fakeDiskImagesClient struct {
	lastCtx context.Context
	err     error
	object  *privatev1.DiskImage
}

func (f *fakeDiskImagesClient) Get(
	ctx context.Context, _ *privatev1.DiskImagesGetRequest, _ ...grpc.CallOption,
) (*privatev1.DiskImagesGetResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.DiskImagesGetResponse_builder{Object: f.object}.Build(), nil
}

func (f *fakeDiskImagesClient) List(
	context.Context, *privatev1.DiskImagesListRequest, ...grpc.CallOption,
) (*privatev1.DiskImagesListResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDiskImagesClient) Create(
	context.Context, *privatev1.DiskImagesCreateRequest, ...grpc.CallOption,
) (*privatev1.DiskImagesCreateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDiskImagesClient) Update(
	context.Context, *privatev1.DiskImagesUpdateRequest, ...grpc.CallOption,
) (*privatev1.DiskImagesUpdateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDiskImagesClient) Delete(
	context.Context, *privatev1.DiskImagesDeleteRequest, ...grpc.CallOption,
) (*privatev1.DiskImagesDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDiskImagesClient) Signal(
	context.Context, *privatev1.DiskImagesSignalRequest, ...grpc.CallOption,
) (*privatev1.DiskImagesSignalResponse, error) {
	return nil, errors.New("not implemented")
}

// fakeBMICatalogItemsClient is a hand-written stub of privatev1.BareMetalInstanceCatalogItemsClient.
type fakeBMICatalogItemsClient struct {
	lastCtx context.Context
	err     error
	object  *privatev1.BareMetalInstanceCatalogItem
	items   []*privatev1.BareMetalInstanceCatalogItem
}

func (f *fakeBMICatalogItemsClient) List(
	ctx context.Context, _ *privatev1.BareMetalInstanceCatalogItemsListRequest, _ ...grpc.CallOption,
) (*privatev1.BareMetalInstanceCatalogItemsListResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.BareMetalInstanceCatalogItemsListResponse_builder{Items: f.items}.Build(), nil
}

func (f *fakeBMICatalogItemsClient) Get(
	context.Context, *privatev1.BareMetalInstanceCatalogItemsGetRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstanceCatalogItemsGetResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBMICatalogItemsClient) Create(
	ctx context.Context, _ *privatev1.BareMetalInstanceCatalogItemsCreateRequest, _ ...grpc.CallOption,
) (*privatev1.BareMetalInstanceCatalogItemsCreateResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.BareMetalInstanceCatalogItemsCreateResponse_builder{Object: f.object}.Build(), nil
}

func (f *fakeBMICatalogItemsClient) Update(
	context.Context, *privatev1.BareMetalInstanceCatalogItemsUpdateRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstanceCatalogItemsUpdateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBMICatalogItemsClient) Delete(
	context.Context, *privatev1.BareMetalInstanceCatalogItemsDeleteRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstanceCatalogItemsDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBMICatalogItemsClient) Signal(
	context.Context, *privatev1.BareMetalInstanceCatalogItemsSignalRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstanceCatalogItemsSignalResponse, error) {
	return nil, errors.New("not implemented")
}

// fakeBMITypesClient is a hand-written stub of privatev1.BareMetalInstanceTypesClient.
type fakeBMITypesClient struct {
	lastCtx context.Context
	err     error
	object  *privatev1.BareMetalInstanceType
}

func (f *fakeBMITypesClient) Get(
	ctx context.Context, _ *privatev1.BareMetalInstanceTypesGetRequest, _ ...grpc.CallOption,
) (*privatev1.BareMetalInstanceTypesGetResponse, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.BareMetalInstanceTypesGetResponse_builder{Object: f.object}.Build(), nil
}

func (f *fakeBMITypesClient) List(
	context.Context, *privatev1.BareMetalInstanceTypesListRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstanceTypesListResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBMITypesClient) Create(
	context.Context, *privatev1.BareMetalInstanceTypesCreateRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstanceTypesCreateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBMITypesClient) Update(
	context.Context, *privatev1.BareMetalInstanceTypesUpdateRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstanceTypesUpdateResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBMITypesClient) Delete(
	context.Context, *privatev1.BareMetalInstanceTypesDeleteRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstanceTypesDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBMITypesClient) Signal(
	context.Context, *privatev1.BareMetalInstanceTypesSignalRequest, ...grpc.CallOption,
) (*privatev1.BareMetalInstanceTypesSignalResponse, error) {
	return nil, errors.New("not implemented")
}

var _ = Describe("FulfillmentClient wrapper", func() {
	var (
		bmi           *fakeBMIClient
		cv            *fakeCVClient
		clusters      *fakeClustersClient
		diskImages    *fakeDiskImagesClient
		catalogItems  *fakeBMICatalogItemsClient
		instanceTypes *fakeBMITypesClient
		fc            FulfillmentClient
		ctx           context.Context
	)

	BeforeEach(func() {
		bmi = &fakeBMIClient{object: &privatev1.BareMetalInstance{}}
		cv = &fakeCVClient{object: &privatev1.ClusterVersion{}}
		clusters = &fakeClustersClient{object: &privatev1.Cluster{}}
		diskImages = &fakeDiskImagesClient{object: &privatev1.DiskImage{}}
		catalogItems = &fakeBMICatalogItemsClient{object: &privatev1.BareMetalInstanceCatalogItem{}}
		instanceTypes = &fakeBMITypesClient{object: &privatev1.BareMetalInstanceType{}}
		fc = NewFulfillmentClient(bmi, cv, clusters, diskImages, catalogItems, instanceTypes)
		ctx = context.Background()
	})

	Context("applies the per-call deadline to gRPC calls", func() {
		It("on CreateBareMetalInstance", func() {
			_, err := fc.CreateBareMetalInstance(ctx, &privatev1.BareMetalInstance{})
			Expect(err).ToNot(HaveOccurred())
			expectCallDeadline(bmi.lastCtx)
		})
		It("on GetBareMetalInstance", func() {
			_, err := fc.GetBareMetalInstance(ctx, "id")
			Expect(err).ToNot(HaveOccurred())
			expectCallDeadline(bmi.lastCtx)
		})
		It("on ListBareMetalInstances", func() {
			_, err := fc.ListBareMetalInstances(ctx, "")
			Expect(err).ToNot(HaveOccurred())
			expectCallDeadline(bmi.lastCtx)
		})
		It("on DeleteBareMetalInstance", func() {
			Expect(fc.DeleteBareMetalInstance(ctx, "id")).To(Succeed())
			expectCallDeadline(bmi.lastCtx)
		})
		It("on GetClusterVersion", func() {
			_, err := fc.GetClusterVersion(ctx, "4.18.0")
			Expect(err).ToNot(HaveOccurred())
			expectCallDeadline(cv.lastCtx)
		})
		It("on GetCluster", func() {
			_, err := fc.GetCluster(ctx, "cluster-uuid")
			Expect(err).ToNot(HaveOccurred())
			expectCallDeadline(clusters.lastCtx)
		})
		It("on GetDiskImage", func() {
			_, err := fc.GetDiskImage(ctx, "rhcos-4.18")
			Expect(err).ToNot(HaveOccurred())
			expectCallDeadline(diskImages.lastCtx)
		})
		It("on CreateBareMetalInstanceCatalogItem", func() {
			_, err := fc.CreateBareMetalInstanceCatalogItem(ctx, &privatev1.BareMetalInstanceCatalogItem{})
			Expect(err).ToNot(HaveOccurred())
			expectCallDeadline(catalogItems.lastCtx)
		})
		It("on ListBareMetalInstanceCatalogItems", func() {
			_, err := fc.ListBareMetalInstanceCatalogItems(ctx, "")
			Expect(err).ToNot(HaveOccurred())
			expectCallDeadline(catalogItems.lastCtx)
		})
		It("on GetBareMetalInstanceType", func() {
			_, err := fc.GetBareMetalInstanceType(ctx, "bm-standard")
			Expect(err).ToNot(HaveOccurred())
			expectCallDeadline(instanceTypes.lastCtx)
		})
	})

	Context("unwraps responses", func() {
		It("returns the created object", func() {
			want := &privatev1.BareMetalInstance{}
			bmi.object = want
			got, err := fc.CreateBareMetalInstance(ctx, &privatev1.BareMetalInstance{})
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeIdenticalTo(want))
		})
		It("returns the listed items", func() {
			bmi.items = []*privatev1.BareMetalInstance{{}, {}}
			got, err := fc.ListBareMetalInstances(ctx, "labels['x']=='y'")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(HaveLen(2))
		})
		It("returns the cluster version", func() {
			want := &privatev1.ClusterVersion{}
			cv.object = want
			got, err := fc.GetClusterVersion(ctx, "4.18.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeIdenticalTo(want))
		})
		It("returns the cluster", func() {
			want := &privatev1.Cluster{}
			clusters.object = want
			got, err := fc.GetCluster(ctx, "cluster-uuid")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeIdenticalTo(want))
		})
		It("returns the disk image", func() {
			want := &privatev1.DiskImage{}
			diskImages.object = want
			got, err := fc.GetDiskImage(ctx, "rhcos-4.18")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeIdenticalTo(want))
		})
		It("returns the created catalog item", func() {
			want := &privatev1.BareMetalInstanceCatalogItem{}
			catalogItems.object = want
			got, err := fc.CreateBareMetalInstanceCatalogItem(ctx, &privatev1.BareMetalInstanceCatalogItem{})
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeIdenticalTo(want))
		})
		It("returns the listed catalog items", func() {
			catalogItems.items = []*privatev1.BareMetalInstanceCatalogItem{{}, {}}
			got, err := fc.ListBareMetalInstanceCatalogItems(ctx, "metadata.name=='x'")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(HaveLen(2))
		})
		It("returns the instance type", func() {
			want := &privatev1.BareMetalInstanceType{}
			instanceTypes.object = want
			got, err := fc.GetBareMetalInstanceType(ctx, "bm-standard")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeIdenticalTo(want))
		})
	})

	Context("consecutive-failure signalling", func() {
		It("surfaces ErrFulfillmentServiceUnavailable only after 3 consecutive failures", func() {
			bmi.err = errBackend

			_, err1 := fc.GetBareMetalInstance(ctx, "id")
			Expect(err1).To(MatchError(errBackend))
			Expect(errors.Is(err1, ErrFulfillmentServiceUnavailable)).To(BeFalse())

			_, err2 := fc.GetBareMetalInstance(ctx, "id")
			Expect(errors.Is(err2, ErrFulfillmentServiceUnavailable)).To(BeFalse())

			_, err3 := fc.GetBareMetalInstance(ctx, "id")
			Expect(errors.Is(err3, ErrFulfillmentServiceUnavailable)).To(BeTrue())
			Expect(errors.Is(err3, errBackend)).To(BeTrue())
		})

		It("resets the counter after a success", func() {
			bmi.err = errBackend
			_, _ = fc.GetBareMetalInstance(ctx, "id")
			_, _ = fc.GetBareMetalInstance(ctx, "id")

			bmi.err = nil
			_, err := fc.GetBareMetalInstance(ctx, "id")
			Expect(err).ToNot(HaveOccurred())

			bmi.err = errBackend
			_, errAfter := fc.GetBareMetalInstance(ctx, "id")
			Expect(errors.Is(errAfter, ErrFulfillmentServiceUnavailable)).To(BeFalse())
		})

		DescribeTable("surfaces unavailable on the 3rd failure for every gRPC method",
			func(invoke func(FulfillmentClient) error) {
				b := &fakeBMIClient{err: errBackend, object: &privatev1.BareMetalInstance{}}
				v := &fakeCVClient{err: errBackend, object: &privatev1.ClusterVersion{}}
				cl := &fakeClustersClient{err: errBackend, object: &privatev1.Cluster{}}
				di := &fakeDiskImagesClient{err: errBackend, object: &privatev1.DiskImage{}}
				ci := &fakeBMICatalogItemsClient{err: errBackend, object: &privatev1.BareMetalInstanceCatalogItem{}}
				it := &fakeBMITypesClient{err: errBackend, object: &privatev1.BareMetalInstanceType{}}
				c := NewFulfillmentClient(b, v, cl, di, ci, it)
				Expect(errors.Is(invoke(c), ErrFulfillmentServiceUnavailable)).To(BeFalse())
				Expect(errors.Is(invoke(c), ErrFulfillmentServiceUnavailable)).To(BeFalse())
				Expect(errors.Is(invoke(c), ErrFulfillmentServiceUnavailable)).To(BeTrue())
			},
			Entry("Create", func(c FulfillmentClient) error {
				_, e := c.CreateBareMetalInstance(context.Background(), &privatev1.BareMetalInstance{})
				return e
			}),
			Entry("Delete", func(c FulfillmentClient) error {
				return c.DeleteBareMetalInstance(context.Background(), "id")
			}),
			Entry("Get", func(c FulfillmentClient) error {
				_, e := c.GetBareMetalInstance(context.Background(), "id")
				return e
			}),
			Entry("List", func(c FulfillmentClient) error {
				_, e := c.ListBareMetalInstances(context.Background(), "")
				return e
			}),
			Entry("GetClusterVersion", func(c FulfillmentClient) error {
				_, e := c.GetClusterVersion(context.Background(), "4.18.0")
				return e
			}),
			Entry("GetCluster", func(c FulfillmentClient) error {
				_, e := c.GetCluster(context.Background(), "cluster-uuid")
				return e
			}),
			Entry("GetDiskImage", func(c FulfillmentClient) error {
				_, e := c.GetDiskImage(context.Background(), "rhcos-4.18")
				return e
			}),
			Entry("CreateBareMetalInstanceCatalogItem", func(c FulfillmentClient) error {
				_, e := c.CreateBareMetalInstanceCatalogItem(context.Background(), &privatev1.BareMetalInstanceCatalogItem{})
				return e
			}),
			Entry("ListBareMetalInstanceCatalogItems", func(c FulfillmentClient) error {
				_, e := c.ListBareMetalInstanceCatalogItems(context.Background(), "")
				return e
			}),
			Entry("GetBareMetalInstanceType", func(c FulfillmentClient) error {
				_, e := c.GetBareMetalInstanceType(context.Background(), "bm-standard")
				return e
			}),
		)

		It("shares the counter across gRPC methods", func() {
			bmi.err = errBackend
			cv.err = errBackend

			_, e1 := fc.GetBareMetalInstance(ctx, "id") // failure 1
			Expect(errors.Is(e1, ErrFulfillmentServiceUnavailable)).To(BeFalse())
			_, e2 := fc.GetClusterVersion(ctx, "4.18.0") // failure 2
			Expect(errors.Is(e2, ErrFulfillmentServiceUnavailable)).To(BeFalse())
			_, e3 := fc.GetBareMetalInstance(ctx, "id") // failure 3
			Expect(errors.Is(e3, ErrFulfillmentServiceUnavailable)).To(BeTrue())
		})
	})
})
