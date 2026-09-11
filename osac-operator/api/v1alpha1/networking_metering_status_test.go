package v1alpha1_test

import (
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("Networking metering status timestamps", func() {
	It("supports authoritative state and attachment transition times", func() {
		now := metav1.Now()
		externalIP := v1alpha1.ExternalIPStatus{
			StateTransitionTime:      &now,
			AttachmentTransitionTime: &now,
		}
		attachment := v1alpha1.ExternalIPAttachmentStatus{StateTransitionTime: &now}
		natGateway := v1alpha1.NATGatewayStatus{StateTransitionTime: &now}

		Expect(externalIP.StateTransitionTime).To(Equal(&now))
		Expect(externalIP.AttachmentTransitionTime).To(Equal(&now))
		Expect(attachment.StateTransitionTime).To(Equal(&now))
		Expect(natGateway.StateTransitionTime).To(Equal(&now))
	})
})
