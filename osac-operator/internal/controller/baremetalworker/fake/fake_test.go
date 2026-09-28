/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fake

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestFake(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BareMetalWorker Fake Suite")
}

func bmiNamed(name string) *privatev1.BareMetalInstance {
	return privatev1.BareMetalInstance_builder{
		Metadata: privatev1.Metadata_builder{Name: name}.Build(),
	}.Build()
}

var _ = Describe("Fake FulfillmentClient", func() {
	var (
		fc  *FulfillmentClient
		ctx context.Context
	)

	BeforeEach(func() {
		fc = NewFulfillmentClient()
		ctx = context.Background()
	})

	It("records Create and stores the object retrievable by id", func() {
		created, err := fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))
		Expect(err).ToNot(HaveOccurred())
		Expect(created.GetId()).To(Equal("bm-worker-0"))

		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(fc.CreateCalls()[0].GetMetadata().GetName()).To(Equal("bm-worker-0"))

		got, err := fc.GetBareMetalInstance(ctx, "bm-worker-0")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetMetadata().GetName()).To(Equal("bm-worker-0"))
		Expect(fc.GetCalls()).To(Equal([]string{"bm-worker-0"}))
	})

	It("requires metadata.name on Create (InvalidArgument)", func() {
		_, err := fc.CreateBareMetalInstance(ctx, &privatev1.BareMetalInstance{})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("rejects a duplicate Create with AlreadyExists (real API uniqueness)", func() {
		_, err := fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))
		Expect(err).ToNot(HaveOccurred())
		_, err = fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))
		Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
	})

	It("does not leak the Create input into the store", func() {
		in := bmiNamed("bm-worker-0")
		_, err := fc.CreateBareMetalInstance(ctx, in)
		Expect(err).ToNot(HaveOccurred())
		in.GetMetadata().SetName("tampered")

		got, err := fc.GetBareMetalInstance(ctx, "bm-worker-0")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetMetadata().GetName()).To(Equal("bm-worker-0"))
	})

	It("returns clones so callers cannot mutate the store", func() {
		_, err := fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))
		Expect(err).ToNot(HaveOccurred())

		got, err := fc.GetBareMetalInstance(ctx, "bm-worker-0")
		Expect(err).ToNot(HaveOccurred())
		got.GetMetadata().SetName("tampered")

		again, err := fc.GetBareMetalInstance(ctx, "bm-worker-0")
		Expect(err).ToNot(HaveOccurred())
		Expect(again.GetMetadata().GetName()).To(Equal("bm-worker-0"))
	})

	It("records Delete and removes the object", func() {
		_, err := fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))
		Expect(err).ToNot(HaveOccurred())

		Expect(fc.DeleteBareMetalInstance(ctx, "bm-worker-0")).To(Succeed())
		Expect(fc.DeleteCalls()).To(Equal([]string{"bm-worker-0"}))

		_, err = fc.GetBareMetalInstance(ctx, "bm-worker-0")
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})

	It("lists all stored instances ordered by id and records the filter", func() {
		_, _ = fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-1"))
		_, _ = fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))

		list, err := fc.ListBareMetalInstances(ctx, "labels['x']=='y'")
		Expect(err).ToNot(HaveOccurred())
		Expect(list).To(HaveLen(2))
		Expect(list[0].GetId()).To(Equal("bm-worker-0"))
		Expect(list[1].GetId()).To(Equal("bm-worker-1"))
		Expect(fc.ListCalls()).To(Equal([]string{"labels['x']=='y'"}))
	})

	It("injects Create errors and recovers when cleared (retry path)", func() {
		boom := errors.New("create boom")
		fc.SetCreateError(boom)
		_, err := fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))
		Expect(err).To(MatchError(boom))
		Expect(fc.CreateCalls()).To(HaveLen(1)) // still recorded

		fc.SetCreateError(nil)
		_, err = fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))
		Expect(err).ToNot(HaveOccurred())
	})

	It("injects Delete errors and recovers when cleared", func() {
		fc.SetDeleteError(errors.New("delete boom"))
		Expect(fc.DeleteBareMetalInstance(ctx, "bm-worker-0")).To(MatchError(ContainSubstring("delete boom")))
		fc.SetDeleteError(nil)
		Expect(fc.DeleteBareMetalInstance(ctx, "bm-worker-0")).To(Succeed())
	})

	It("purges the host MAC when a BMI is deleted", func() {
		_, err := fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))
		Expect(err).ToNot(HaveOccurred())
		fc.SetHostMAC("bm-worker-0", "aa:bb:cc:dd:ee:ff")
		Expect(fc.DeleteBareMetalInstance(ctx, "bm-worker-0")).To(Succeed())
		Expect(fc.HostMAC("bm-worker-0")).To(BeEmpty())
	})

	It("preloads and returns ClusterVersions", func() {
		cv := privatev1.ClusterVersion_builder{
			Id:       "4.18.0",
			Metadata: privatev1.Metadata_builder{Name: "4-18-0"}.Build(),
		}.Build()
		fc.AddClusterVersion(cv)

		got, err := fc.GetClusterVersion(ctx, "4.18.0")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetMetadata().GetName()).To(Equal("4-18-0"))
		Expect(fc.GetClusterVersionCalls()).To(Equal([]string{"4.18.0"}))

		_, err = fc.GetClusterVersion(ctx, "nope")
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})

	It("preloads and returns DiskImages", func() {
		di := privatev1.DiskImage_builder{
			Id:       "rhcos-4.18",
			Metadata: privatev1.Metadata_builder{Name: "rhcos-4-18"}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  "oci://registry.example.com/rhcos:4.18",
			}.Build(),
		}.Build()
		fc.AddDiskImage(di)

		got, err := fc.GetDiskImage(ctx, "rhcos-4.18")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetSpec().GetSourceType()).To(Equal(privatev1.SourceType_SOURCE_TYPE_REGISTRY))
		Expect(got.GetSpec().GetSourceRef()).To(Equal("oci://registry.example.com/rhcos:4.18"))
		Expect(fc.GetDiskImageCalls()).To(Equal([]string{"rhcos-4.18"}))

		_, err = fc.GetDiskImage(ctx, "nope")
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})

	It("preloads and returns Clusters", func() {
		cl := privatev1.Cluster_builder{
			Id:       "cluster-uuid",
			Metadata: privatev1.Metadata_builder{Name: "my-cluster"}.Build(),
		}.Build()
		fc.AddCluster(cl)

		got, err := fc.GetCluster(ctx, "cluster-uuid")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetMetadata().GetName()).To(Equal("my-cluster"))
		Expect(fc.GetClusterCalls()).To(Equal([]string{"cluster-uuid"}))

		_, err = fc.GetCluster(ctx, "nope")
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})

	It("stores a settable host MAC per BMI for correlation", func() {
		fc.SetHostMAC("bm-worker-0", "aa:bb:cc:dd:ee:ff")
		Expect(fc.HostMAC("bm-worker-0")).To(Equal("aa:bb:cc:dd:ee:ff"))
		Expect(fc.HostMAC("other")).To(BeEmpty())
	})

	It("surfaces the host MAC through GetBareMetalInstance status.hardware.nics", func() {
		_, err := fc.CreateBareMetalInstance(ctx, bmiNamed("bm-worker-0"))
		Expect(err).ToNot(HaveOccurred())
		fc.SetHostMAC("bm-worker-0", "aa:bb:cc:dd:ee:ff")

		got, err := fc.GetBareMetalInstance(ctx, "bm-worker-0")
		Expect(err).ToNot(HaveOccurred())
		nics := got.GetStatus().GetHardware().GetNics()
		macs := make([]string, 0, len(nics))
		for _, nic := range nics {
			macs = append(macs, nic.GetMac())
		}
		Expect(macs).To(ContainElement("aa:bb:cc:dd:ee:ff"))
	})

	It("preloads and returns BareMetalInstanceTypes", func() {
		fc.AddBareMetalInstanceType(privatev1.BareMetalInstanceType_builder{
			Metadata: privatev1.Metadata_builder{Name: "bm-standard"}.Build(),
			Spec: privatev1.BareMetalInstanceTypeSpec_builder{
				Hardware: privatev1.BareMetalHardwareSpec_builder{
					Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 64, Architecture: "x86_64", ThreadsPerCore: 2}.Build(),
					Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 256}.Build(),
				}.Build(),
				HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
					MatchLabels: map[string]string{"type": "bm-standard"},
				}.Build(),
			}.Build(),
		}.Build())

		got, err := fc.GetBareMetalInstanceType(ctx, "bm-standard")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetMetadata().GetName()).To(Equal("bm-standard"))
		Expect(fc.GetInstanceTypeCalls()).To(Equal([]string{"bm-standard"}))

		_, err = fc.GetBareMetalInstanceType(ctx, "nope")
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})

	Context("BareMetalInstanceCatalogItem operations", func() {
		ciNamed := func(name string) *privatev1.BareMetalInstanceCatalogItem {
			return privatev1.BareMetalInstanceCatalogItem_builder{
				Metadata: privatev1.Metadata_builder{Name: name}.Build(),
			}.Build()
		}

		It("records Create and stores the catalog item retrievable by List", func() {
			created, err := fc.CreateBareMetalInstanceCatalogItem(ctx, ciNamed("system-bmi-passthrough"))
			Expect(err).ToNot(HaveOccurred())
			Expect(created.GetId()).To(Equal("system-bmi-passthrough"))

			Expect(fc.CreateCatalogItemCalls()).To(HaveLen(1))
			Expect(fc.CreateCatalogItemCalls()[0].GetMetadata().GetName()).To(Equal("system-bmi-passthrough"))

			items, err := fc.ListBareMetalInstanceCatalogItems(ctx, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("system-bmi-passthrough"))
		})

		It("requires metadata.name on Create (InvalidArgument)", func() {
			_, err := fc.CreateBareMetalInstanceCatalogItem(ctx, &privatev1.BareMetalInstanceCatalogItem{})
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("rejects a duplicate Create with AlreadyExists", func() {
			_, err := fc.CreateBareMetalInstanceCatalogItem(ctx, ciNamed("system-bmi-passthrough"))
			Expect(err).ToNot(HaveOccurred())
			_, err = fc.CreateBareMetalInstanceCatalogItem(ctx, ciNamed("system-bmi-passthrough"))
			Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		})

		It("lists all stored catalog items ordered by id and records the filter", func() {
			_, _ = fc.CreateBareMetalInstanceCatalogItem(ctx, ciNamed("b-item"))
			_, _ = fc.CreateBareMetalInstanceCatalogItem(ctx, ciNamed("a-item"))

			items, err := fc.ListBareMetalInstanceCatalogItems(ctx, "metadata.name=='a-item'")
			Expect(err).ToNot(HaveOccurred())
			Expect(items).To(HaveLen(2))
			Expect(items[0].GetId()).To(Equal("a-item"))
			Expect(items[1].GetId()).To(Equal("b-item"))
			Expect(fc.ListCatalogItemCalls()).To(Equal([]string{"metadata.name=='a-item'"}))
		})
	})
})

var _ = Describe("Fake ignition endpoint", func() {
	ctx := context.Background()
	fetcher := baremetalworker.NewIgnitionFetcher(nil)

	It("serves default content fetched by the real IgnitionFetcher", func() {
		srv := NewIgnitionServer()
		defer srv.Close()

		body, err := fetcher.FetchIgnition(ctx, srv.URL())
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(ContainSubstring(`"version":"3.2.0"`))
	})

	It("serves configured content", func() {
		srv := NewIgnitionServer()
		defer srv.Close()
		srv.SetContent([]byte("custom-ignition"))

		body, err := fetcher.FetchIgnition(ctx, srv.URL())
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal("custom-ignition"))
	})

	It("serves a body of exactly the configured size", func() {
		// The fake serves an exact-size body; the reconciler suite asserts the size-warning
		// behavior itself (this only proves the size is configurable and round-trips).
		srv := NewIgnitionServer()
		defer srv.Close()
		const size = 50 * 1024
		srv.SetSize(size)

		body, err := fetcher.FetchIgnition(ctx, srv.URL())
		Expect(err).ToNot(HaveOccurred())
		Expect(body).To(HaveLen(size))
	})
})
