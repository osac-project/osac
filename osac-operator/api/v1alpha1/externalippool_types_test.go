package v1alpha1_test

import (
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac-operator/api/v1alpha1"
)

var _ = Describe("ExternalIPPoolSpec", func() {
	It("should accept a valid spec with all required fields", func() {
		spec := v1alpha1.ExternalIPPoolSpec{
			CIDRs:                  []string{"192.168.1.0/24", "192.168.2.0/24"},
			IPFamily:               "IPv4",
			ImplementationStrategy: "metallb-l2",
		}

		Expect(spec.CIDRs).To(Equal([]string{"192.168.1.0/24", "192.168.2.0/24"}))
		Expect(spec.IPFamily).To(Equal("IPv4"))
		Expect(spec.ImplementationStrategy).To(Equal("metallb-l2"))
	})

	It("should accept netris as a valid implementation strategy", func() {
		spec := v1alpha1.ExternalIPPoolSpec{
			CIDRs:                  []string{"192.168.1.0/24"},
			IPFamily:               "IPv4",
			ImplementationStrategy: "netris",
		}

		Expect(spec.ImplementationStrategy).To(Equal("netris"))
	})

	It("should accept a minimal spec without optional fields", func() {
		spec := v1alpha1.ExternalIPPoolSpec{
			CIDRs:    []string{"10.0.0.0/16"},
			IPFamily: "IPv4",
		}

		Expect(spec.CIDRs).To(HaveLen(1))
		Expect(spec.IPFamily).To(Equal("IPv4"))
		Expect(spec.ImplementationStrategy).To(BeEmpty())
	})
})

var _ = Describe("ExternalIPPoolPhaseType", func() {
	DescribeTable("should have correct string values",
		func(phase v1alpha1.ExternalIPPoolPhaseType, expected string) {
			Expect(string(phase)).To(Equal(expected))
		},
		Entry("Progressing phase", v1alpha1.ExternalIPPoolPhaseProgressing, "Progressing"),
		Entry("Failed phase", v1alpha1.ExternalIPPoolPhaseFailed, "Failed"),
		Entry("Ready phase", v1alpha1.ExternalIPPoolPhaseReady, "Ready"),
		Entry("Deleting phase", v1alpha1.ExternalIPPoolPhaseDeleting, "Deleting"),
	)
})

var _ = Describe("ExternalIPPoolConditionType", func() {
	It("should have ConfigurationApplied condition type", func() {
		Expect(string(v1alpha1.ExternalIPPoolConditionConfigurationApplied)).To(Equal("ConfigurationApplied"))
	})
})

var _ = Describe("ExternalIPPool", func() {
	Describe("GetName", func() {
		It("should return the name", func() {
			pool := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool",
				},
			}

			Expect(pool.GetName()).To(Equal("test-pool"))
		})
	})
})

var _ = Describe("ExternalIPPool Condition Helpers", func() {
	It("should set and get a condition", func() {
		pool := &v1alpha1.ExternalIPPool{}

		condition := metav1.Condition{
			Type:               string(v1alpha1.ExternalIPPoolConditionConfigurationApplied),
			Status:             metav1.ConditionTrue,
			Reason:             "ConfigurationApplied",
			Message:            "applied",
			LastTransitionTime: metav1.Now(),
		}

		v1alpha1.SetExternalIPPoolStatusCondition(pool, condition)

		got := v1alpha1.GetExternalIPPoolStatusCondition(pool, v1alpha1.ExternalIPPoolConditionConfigurationApplied)
		Expect(got).ToNot(BeNil())
		Expect(got.Status).To(Equal(metav1.ConditionTrue))
	})

	It("should return nil for missing condition", func() {
		pool := &v1alpha1.ExternalIPPool{}

		got := v1alpha1.GetExternalIPPoolStatusCondition(pool, v1alpha1.ExternalIPPoolConditionConfigurationApplied)
		Expect(got).To(BeNil())
	})
})

var _ = Describe("ExternalIPPoolStatus", func() {
	It("should accept capacity fields", func() {
		status := v1alpha1.ExternalIPPoolStatus{
			Total:     256,
			Allocated: 10,
			Available: 246,
		}

		Expect(status.Total).To(Equal(int64(256)))
		Expect(status.Allocated).To(Equal(int64(10)))
		Expect(status.Available).To(Equal(int64(246)))
	})
})
