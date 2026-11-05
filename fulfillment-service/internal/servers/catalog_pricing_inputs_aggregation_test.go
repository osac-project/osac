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
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Catalog pricing inputs aggregation", func() {
	computeItem := func(id, name string, published bool, fields *privatev1.ComputeInstanceCatalogItemFields) *privatev1.ComputeInstanceCatalogItem {
		return privatev1.ComputeInstanceCatalogItem_builder{
			Id:        id,
			Metadata:  privatev1.Metadata_builder{Name: name}.Build(),
			Title:     "title-" + name,
			Published: published,
			Fields:    fields,
		}.Build()
	}
	clusterItem := func(id, name string, published bool, fields *privatev1.ClusterCatalogItemFields) *privatev1.ClusterCatalogItem {
		return privatev1.ClusterCatalogItem_builder{
			Id:        id,
			Metadata:  privatev1.Metadata_builder{Name: name}.Build(),
			Title:     "title-" + name,
			Published: published,
			Fields:    fields,
		}.Build()
	}
	findRow := func(rows []*privatev1.CatalogPricingInput, service, dimension, value string) *privatev1.CatalogPricingInput {
		for _, row := range rows {
			if row.GetService() == service && row.GetDimension() == dimension && row.GetValue() == value {
				return row
			}
		}
		return nil
	}

	It("contributes nothing for an item with no billable fields", func() {
		item := computeItem("c1", "item-1", true, nil)
		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		Expect(rows).To(BeEmpty())
	})

	It("emits locked and default image and boot-disk condition dimensions", func() {
		item := computeItem("c1", "vm-defaults", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{
				Editable: privatev1.EditableDiskImageReferenceField_builder{
					DefaultValue: privatev1.DiskImageReference_builder{Id: "image-id", Name: "fedora"}.Build(),
				}.Build(),
			}.Build(),
			BootDisk: privatev1.ComputeInstanceBootDiskFieldPolicies_builder{
				SizeGib: privatev1.Int32FieldPolicy_builder{
					Editable: privatev1.EditableInt32Field_builder{DefaultValue: int32Ptr(80)}.Build(),
				}.Build(),
				StorageTier: privatev1.StorageTierReferenceFieldPolicy_builder{
					Locked: privatev1.StorageTierReference_builder{Id: "tier-id", Name: "fast"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())

		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		Expect(rows).To(HaveLen(5))
		Expect(findRow(rows, "compute_instance", "image", "").GetAcceptsAnyValue()).To(BeTrue())
		Expect(findRow(rows, "compute_instance", "image", "fedora").GetAcceptsAnyValue()).To(BeFalse())
		Expect(findRow(rows, "compute_instance", "boot_disk_size_gib", "").GetAcceptsAnyValue()).To(BeTrue())
		Expect(findRow(rows, "compute_instance", "boot_disk_size_gib", "80").GetAcceptsAnyValue()).To(BeFalse())
		Expect(findRow(rows, "compute_instance", "boot_disk_storage_tier", "fast").GetAcceptsAnyValue()).To(BeFalse())
	})

	It("emits open image and boot-disk dimensions when editable policies have no defaults", func() {
		item := computeItem("c1", "vm-open", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{
				Editable: privatev1.EditableDiskImageReferenceField_builder{}.Build(),
			}.Build(),
			BootDisk: privatev1.ComputeInstanceBootDiskFieldPolicies_builder{
				SizeGib: privatev1.Int32FieldPolicy_builder{
					Editable: privatev1.EditableInt32Field_builder{}.Build(),
				}.Build(),
				StorageTier: privatev1.StorageTierReferenceFieldPolicy_builder{
					Editable: privatev1.EditableStorageTierReferenceField_builder{}.Build(),
				}.Build(),
			}.Build(),
		}.Build())

		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		Expect(rows).To(HaveLen(3))
		for _, dimension := range []string{"image", "boot_disk_size_gib", "boot_disk_storage_tier"} {
			row := findRow(rows, "compute_instance", dimension, "")
			Expect(row).ToNot(BeNil(), dimension)
			Expect(row.GetAcceptsAnyValue()).To(BeTrue(), dimension)
			Expect(row.GetCatalogItems()).To(HaveLen(1), dimension)
		}
	})

	It("uses the canonical reference ID when a dimension reference has no name", func() {
		item := computeItem("c1", "vm-id-reference", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{
				Locked: privatev1.DiskImageReference_builder{Id: "image-id"}.Build(),
			}.Build(),
		}.Build())

		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		Expect(rows).To(HaveLen(1))
		Expect(findRow(rows, "compute_instance", "image", "image-id")).ToNot(BeNil())
	})

	It("emits a fixed-value row for a locked instance type", func() {
		item := computeItem("c1", "item-1", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
				Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "m1.small"}.Build(),
			}.Build(),
		}.Build())
		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		row := findRow(rows, "compute_instance", "instance_type", "m1.small")
		Expect(row).ToNot(BeNil())
		Expect(row.GetAcceptsAnyValue()).To(BeFalse())
		Expect(row.GetCatalogItems()).To(HaveLen(1))
		Expect(row.GetCatalogItems()[0].GetId()).To(Equal("c1"))
		Expect(row.GetCatalogItems()[0].GetPublished()).To(BeTrue())
	})

	It("includes a draft item with published=false", func() {
		item := computeItem("c1", "item-1", false, privatev1.ComputeInstanceCatalogItemFields_builder{
			InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
				Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "m1.small"}.Build(),
			}.Build(),
		}.Build())
		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		row := findRow(rows, "compute_instance", "instance_type", "m1.small")
		Expect(row).ToNot(BeNil())
		Expect(row.GetCatalogItems()[0].GetPublished()).To(BeFalse())
	})

	It("emits an open row with no default and no duplicate concrete row", func() {
		item := computeItem("c1", "item-1", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
				Editable: privatev1.EditableInstanceTypeReferenceField_builder{}.Build(),
			}.Build(),
		}.Build())
		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		Expect(rows).To(HaveLen(1))
		row := findRow(rows, "compute_instance", "instance_type", "")
		Expect(row).ToNot(BeNil())
		Expect(row.GetAcceptsAnyValue()).To(BeTrue())
	})

	It("emits both the open row and the concrete default row when editable has a default", func() {
		item := computeItem("c1", "item-1", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
				Editable: privatev1.EditableInstanceTypeReferenceField_builder{
					DefaultValue: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "m1.small"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		Expect(rows).To(HaveLen(2))
		open := findRow(rows, "compute_instance", "instance_type", "")
		Expect(open.GetAcceptsAnyValue()).To(BeTrue())
		concrete := findRow(rows, "compute_instance", "instance_type", "m1.small")
		Expect(concrete.GetAcceptsAnyValue()).To(BeFalse())
	})

	It("covers additional disk size and storage tier for a locked disk list", func() {
		item := computeItem("c1", "item-1", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			AdditionalDisks: privatev1.ComputeInstanceDiskListFieldPolicy_builder{
				Locked: privatev1.ComputeInstanceDiskList_builder{
					Items: []*privatev1.ComputeInstanceDisk{
						privatev1.ComputeInstanceDisk_builder{
							SizeGib:     int32Ptr(100),
							StorageTier: privatev1.StorageTierReference_builder{Id: "st1", Name: "fast"}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		Expect(findRow(rows, "compute_instance", "additional_disk_size_gib", "100")).ToNot(BeNil())
		Expect(findRow(rows, "compute_instance", "additional_storage_tier", "fast")).ToNot(BeNil())
	})

	It("emits open rows for editable additional disks with no default items", func() {
		item := computeItem("c1", "item-1", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			AdditionalDisks: privatev1.ComputeInstanceDiskListFieldPolicy_builder{
				Editable: privatev1.EditableComputeInstanceDiskList_builder{}.Build(),
			}.Build(),
		}.Build())
		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item}, nil)
		Expect(findRow(rows, "compute_instance", "additional_disk_size_gib", "")).ToNot(BeNil())
		Expect(findRow(rows, "compute_instance", "additional_storage_tier", "")).ToNot(BeNil())
	})

	It("covers node count and baremetal instance type for a locked node set", func() {
		item := clusterItem("k1", "cluster-1", true, privatev1.ClusterCatalogItemFields_builder{
			NodeSets: privatev1.ClusterNodeSetMapPolicy_builder{
				Locked: privatev1.ClusterNodeSetMap_builder{
					Items: map[string]*privatev1.ClusterCatalogNodeSet{
						"workers": privatev1.ClusterCatalogNodeSet_builder{
							Size:                  3,
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "bt1", Name: "large"}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		rows := aggregateCatalogPricingInputs(nil, []*privatev1.ClusterCatalogItem{item})
		Expect(findRow(rows, "cluster", "node_count", "3")).ToNot(BeNil())
		Expect(findRow(rows, "cluster", "baremetal_instance_type", "large")).ToNot(BeNil())
	})

	It("merges two items sharing the same (service, dimension, value) into one row", func() {
		item1 := computeItem("c1", "item-1", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
				Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "m1.small"}.Build(),
			}.Build(),
		}.Build())
		item2 := computeItem("c2", "item-2", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
				Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "m1.small"}.Build(),
			}.Build(),
		}.Build())
		rows := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{item1, item2}, nil)
		row := findRow(rows, "compute_instance", "instance_type", "m1.small")
		Expect(row.GetCatalogItems()).To(HaveLen(2))
		ids := []string{row.GetCatalogItems()[0].GetId(), row.GetCatalogItems()[1].GetId()}
		Expect(ids).To(Equal([]string{"c1", "c2"})) // sorted by id
	})

	It("orders rows deterministically by (service, dimension, value)", func() {
		a := computeItem("c1", "item-1", true, privatev1.ComputeInstanceCatalogItemFields_builder{
			InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
				Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "zzz"}.Build(),
			}.Build(),
			AdditionalDisks: privatev1.ComputeInstanceDiskListFieldPolicy_builder{
				Locked: privatev1.ComputeInstanceDiskList_builder{
					Items: []*privatev1.ComputeInstanceDisk{
						privatev1.ComputeInstanceDisk_builder{SizeGib: int32Ptr(50)}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		rows1 := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{a}, nil)
		rows2 := aggregateCatalogPricingInputs([]*privatev1.ComputeInstanceCatalogItem{a}, nil)
		Expect(reflect.DeepEqual(
			[]string{rows1[0].GetDimension(), rows1[1].GetDimension()},
			[]string{rows2[0].GetDimension(), rows2[1].GetDimension()},
		)).To(BeTrue())
		Expect(rows1[0].GetDimension()).To(Equal("additional_disk_size_gib"))
		Expect(rows1[1].GetDimension()).To(Equal("instance_type"))
	})

	It("takes no billing or provider dependency: aggregateCatalogPricingInputs only accepts catalog item slices", func() {
		fn := reflect.TypeOf(aggregateCatalogPricingInputs)
		Expect(fn.NumIn()).To(Equal(2))
		Expect(fn.In(0)).To(Equal(reflect.TypeOf([]*privatev1.ComputeInstanceCatalogItem{})))
		Expect(fn.In(1)).To(Equal(reflect.TypeOf([]*privatev1.ClusterCatalogItem{})))
	})
})
