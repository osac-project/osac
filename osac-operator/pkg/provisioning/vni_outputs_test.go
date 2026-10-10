package provisioning

import (
	"encoding/json"
	"math"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("ParseFabricVNIs", func() {
	ginkgo.It("parses integer values returned from AAP job artifacts", func() {
		vnis, err := ParseFabricVNIs(map[string]any{"l2_vni": float64(4096), "l3_vni": json.Number("16777215")})

		Expect(err).NotTo(HaveOccurred())
		Expect(*vnis.L2VNI).To(Equal(int32(4096)))
		Expect(*vnis.L3VNI).To(Equal(int32(16777215)))
	})

	ginkgo.It("accepts a missing optional VNI", func() {
		vnis, err := ParseFabricVNIs(map[string]any{"l2_vni": float64(4096)})

		Expect(err).NotTo(HaveOccurred())
		Expect(vnis.L2VNI).NotTo(BeNil())
		Expect(vnis.L3VNI).To(BeNil())
	})

	ginkgo.It("treats nil outputs as absent optional VNIs", func() {
		vnis, err := ParseFabricVNIs(nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(vnis.L2VNI).To(BeNil())
		Expect(vnis.L3VNI).To(BeNil())
	})

	ginkgo.DescribeTable("accepts supported integer representations",
		func(value any) {
			vnis, err := ParseFabricVNIs(map[string]any{"l2_vni": value})

			Expect(err).NotTo(HaveOccurred())
			Expect(vnis.L2VNI).NotTo(BeNil())
			Expect(*vnis.L2VNI).To(Equal(int32(4096)))
		},
		ginkgo.Entry("float32", float32(4096)),
		ginkgo.Entry("string", "4096"),
		ginkgo.Entry("int", int(4096)),
		ginkgo.Entry("int32", int32(4096)),
		ginkgo.Entry("int64", int64(4096)),
		ginkgo.Entry("uint", uint(4096)),
		ginkgo.Entry("uint32", uint32(4096)),
		ginkgo.Entry("uint64", uint64(4096)),
	)

	ginkgo.DescribeTable("rejects invalid supplied values",
		func(value any) {
			_, err := ParseFabricVNIs(map[string]any{"l2_vni": value})
			Expect(err).To(HaveOccurred())
		},
		ginkgo.Entry("zero", float64(0)),
		ginkgo.Entry("negative", float64(-1)),
		ginkgo.Entry("fractional", float64(1.5)),
		ginkgo.Entry("fractional float32", float32(1.5)),
		ginkgo.Entry("above the 24-bit maximum", float64(1<<24)),
		ginkgo.Entry("malformed string", "not-a-vni"),
		ginkgo.Entry("fractional JSON number", json.Number("1.5")),
		ginkgo.Entry("unsupported value type", true),
		ginkgo.Entry("non-finite number", math.NaN()),
		ginkgo.Entry("uint above MaxInt64", ^uint(0)),
		ginkgo.Entry("uint64 above MaxInt64", uint64(1)<<63),
	)

	ginkgo.It("rejects an invalid L3 VNI", func() {
		_, err := ParseFabricVNIs(map[string]any{"l3_vni": float64(0)})
		Expect(err).To(HaveOccurred())
	})
})

var _ = ginkgo.Describe("ParseFabricOutputConfigMap", func() {
	ginkgo.It("returns typed VNIs and the reserved range from ConfigMap data", func() {
		outputs, err := ParseFabricOutputConfigMap(map[string]string{
			"l2_vni":                "4096",
			"l3_vni":                "8192",
			"fabric_reserved_range": "192.0.2.0/26",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(outputs).To(Equal(map[string]any{
			"l2_vni":                int32(4096),
			"l3_vni":                int32(8192),
			"fabric_reserved_range": "192.0.2.0/26",
		}))
	})

	ginkgo.It("rejects missing required output keys", func() {
		_, err := ParseFabricOutputConfigMap(map[string]string{"l2_vni": "4096", "l3_vni": "8192"})

		Expect(err).To(MatchError(ContainSubstring("fabric_reserved_range")))
	})

	ginkgo.It("rejects invalid VNI values", func() {
		_, err := ParseFabricOutputConfigMap(map[string]string{
			"l2_vni":                "0",
			"l3_vni":                "8192",
			"fabric_reserved_range": "192.0.2.0/26",
		})

		Expect(err).To(MatchError(ContainSubstring("l2_vni")))
	})
})
