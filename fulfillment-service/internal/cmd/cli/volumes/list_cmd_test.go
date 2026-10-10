/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package volumes

import (
	"bytes"
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// fakeVolumesClient implements publicv1.VolumesClient with a configurable List that serves
// pages of volumes at a fixed page size. It records every requested offset so tests can verify
// that the pagination loop advances correctly.
type fakeVolumesClient struct {
	publicv1.VolumesClient

	// allVolumes is the full corpus returned across pages.
	allVolumes []*publicv1.Volume

	// pageSize controls how many items a single List response contains.
	pageSize int32

	// requestedOffsets records the offset value from each List call in order.
	requestedOffsets []int32
}

// List returns a page of volumes starting at the requested offset.
func (f *fakeVolumesClient) List(_ context.Context, req *publicv1.VolumesListRequest, _ ...grpc.CallOption) (*publicv1.VolumesListResponse, error) {
	offset := req.GetOffset()
	f.requestedOffsets = append(f.requestedOffsets, offset)

	total := int32(len(f.allVolumes))
	end := offset + f.pageSize
	if end > total {
		end = total
	}

	page := f.allVolumes[offset:end]
	return publicv1.VolumesListResponse_builder{
		Size:  int32(len(page)),
		Total: total,
		Items: page,
	}.Build(), nil
}

// formatVolumeTable renders a volume table and returns the output as a string.
func formatVolumeTable(volumes []*publicv1.Volume) string {
	buffer := &bytes.Buffer{}
	console, err := terminal.NewConsole().
		SetLogger(logger).
		SetStdout(buffer).
		Build()
	Expect(err).ToNot(HaveOccurred())
	Expect(renderVolumeTable(console, volumes)).To(Succeed())
	return buffer.String()
}

// makeVolumes creates n test volumes with sequential IDs and names.
func makeVolumes(n int) []*publicv1.Volume {
	volumes := make([]*publicv1.Volume, n)
	for i := range volumes {
		volumes[i] = publicv1.Volume_builder{
			Id: fmt.Sprintf("vol-%03d", i),
			Metadata: publicv1.Metadata_builder{
				Name: fmt.Sprintf("volume-%03d", i),
			}.Build(),
			Spec: publicv1.VolumeSpec_builder{
				StorageTier: "gold",
				SizeGib:     10,
				AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
			}.Build(),
			Status: publicv1.VolumeStatus_builder{
				State: publicv1.VolumeState_VOLUME_STATE_AVAILABLE,
			}.Build(),
		}.Build()
	}
	return volumes
}

var _ = Describe("volumes list", func() {
	Context("command structure", func() {
		It("should create command without error", func() {
			cmd := listCmd()
			Expect(cmd).NotTo(BeNil())
			Expect(cmd.Use).To(Equal("list"))
		})

		It("should register --project flag", func() {
			cmd := listCmd()
			flag := cmd.Flags().Lookup("project")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("PROJECT"))
		})

		It("should have proper short help", func() {
			cmd := listCmd()
			Expect(cmd.Short).To(Equal("List volumes"))
		})
	})

	Context("listAllVolumes pagination", func() {
		It("should collect all items across multiple pages", func() {
			allVolumes := makeVolumes(150)
			fake := &fakeVolumesClient{allVolumes: allVolumes, pageSize: 50}

			result, err := listAllVolumes(context.Background(), fake, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(150))

			// Verify successive offsets were requested: 0, 50, 100.
			Expect(fake.requestedOffsets).To(Equal([]int32{0, 50, 100}))

			// Verify first and last items are present.
			Expect(result[0].GetId()).To(Equal("vol-000"))
			Expect(result[149].GetId()).To(Equal("vol-149"))
		})

		It("should return all items in a single page when count is small", func() {
			allVolumes := makeVolumes(5)
			fake := &fakeVolumesClient{allVolumes: allVolumes, pageSize: 50}

			result, err := listAllVolumes(context.Background(), fake, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(5))

			// Only one request should have been made.
			Expect(fake.requestedOffsets).To(Equal([]int32{0}))
		})

		It("should return empty when no volumes exist", func() {
			fake := &fakeVolumesClient{allVolumes: nil, pageSize: 50}

			result, err := listAllVolumes(context.Background(), fake, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeEmpty())
		})
	})

	Context("renderVolumeTable", func() {
		It("should render all columns for a fully populated volume", func() {
			volumes := []*publicv1.Volume{
				publicv1.Volume_builder{
					Id: "vol-001",
					Metadata: publicv1.Metadata_builder{
						Name: "my-volume",
					}.Build(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "gold",
						SizeGib:     100,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
					Status: publicv1.VolumeStatus_builder{
						State: publicv1.VolumeState_VOLUME_STATE_AVAILABLE,
					}.Build(),
				}.Build(),
			}

			output := formatVolumeTable(volumes)
			Expect(output).To(ContainSubstring("ID"))
			Expect(output).To(ContainSubstring("NAME"))
			Expect(output).To(ContainSubstring("STORAGE TIER"))
			Expect(output).To(ContainSubstring("SIZE (GiB)"))
			Expect(output).To(ContainSubstring("ACCESS MODE"))
			Expect(output).To(ContainSubstring("STATE"))
			Expect(output).To(MatchRegexp(`vol-001\s+my-volume\s+gold\s+100\s+READ_WRITE_ONCE\s+AVAILABLE`))
			Expect(output).NotTo(ContainSubstring("VOLUME_ACCESS_MODE_"))
			Expect(output).NotTo(ContainSubstring("VOLUME_STATE_"))
		})

		It("should show '-' for name and storage tier when empty", func() {
			volumes := []*publicv1.Volume{
				publicv1.Volume_builder{
					Id: "vol-002",
				}.Build(),
			}

			output := formatVolumeTable(volumes)
			Expect(output).To(MatchRegexp(`vol-002\s+-\s+-`))
		})

		It("should render one row per volume", func() {
			volumes := []*publicv1.Volume{
				publicv1.Volume_builder{
					Id: "vol-001",
					Metadata: publicv1.Metadata_builder{
						Name: "alpha",
					}.Build(),
				}.Build(),
				publicv1.Volume_builder{
					Id: "vol-002",
					Metadata: publicv1.Metadata_builder{
						Name: "beta",
					}.Build(),
				}.Build(),
			}

			output := formatVolumeTable(volumes)
			Expect(output).To(ContainSubstring("vol-001"))
			Expect(output).To(ContainSubstring("alpha"))
			Expect(output).To(ContainSubstring("vol-002"))
			Expect(output).To(ContainSubstring("beta"))
		})
	})
})
