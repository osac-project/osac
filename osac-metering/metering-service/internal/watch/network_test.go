package watch_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/internal/watch"
)

var _ = Describe("Watch registration", func() {
	It("registers all supported resource payloads", func() {
		filter := watch.BuildFilter()

		Expect(filter).To(Equal("has(event.compute_instance) || has(event.cluster) || has(event.external_ip) || has(event.nat_gateway) || has(event.volume) || has(event.bare_metal_instance)"))
		Expect(filter).NotTo(ContainSubstring("virtual_network"))
		Expect(filter).NotTo(ContainSubstring("subnet"))
		Expect(filter).NotTo(ContainSubstring("security_group"))
	})
})
