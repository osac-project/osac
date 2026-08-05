package management_test

import (
	"context"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/bare-metal-fulfillment-operator/internal/management"
)

const testBackendType = "openstack"

func TestManagement(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Management Suite")
}

var _ = Describe("PowerState", func() {
	It("should have correct PowerOn value", func() {
		Expect(string(management.PowerOn)).To(Equal("power on"))
	})

	It("should have correct PowerOff value", func() {
		Expect(string(management.PowerOff)).To(Equal("power off"))
	})

	It("should have distinct values", func() {
		Expect(management.PowerOn).NotTo(Equal(management.PowerOff))
	})
})

var _ = Describe("PowerStatus", func() {
	It("should represent a powered on stable state", func() {
		status := &management.PowerStatus{
			State:           management.PowerOn,
			IsTransitioning: false,
		}
		Expect(status.State).To(Equal(management.PowerOn))
		Expect(status.IsTransitioning).To(BeFalse())
	})

	It("should represent a transitioning state", func() {
		status := &management.PowerStatus{
			State:           management.PowerOff,
			IsTransitioning: true,
		}
		Expect(status.State).To(Equal(management.PowerOff))
		Expect(status.IsTransitioning).To(BeTrue())
	})
})

var _ = Describe("NewClient factory", func() {
	It("should return error for unknown type", func() {
		client, err := management.NewClient(context.Background(), &management.Config{
			Type: "unknown",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported management backend type"))
		Expect(client).To(BeNil())
	})

	Context("when OpenStack credentials are configured", func() {
		BeforeEach(func() {
			if !hasOpenStackCredentials() {
				Skip("Skipping: set MANAGEMENT_TEST_OPENSTACK=1 with valid OpenStack credentials to run")
			}
		})

		It("should return a client for openstack type", func() {
			client, err := management.NewClient(context.Background(), &management.Config{
				Type: testBackendType,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(client).NotTo(BeNil())
		})
	})
})

var _ = Describe("OpenStackClient", func() {
	Context("when OpenStack credentials are not configured", func() {
		var originalOSCloud, originalAuthURL string

		BeforeEach(func() {
			originalOSCloud = os.Getenv("OS_CLOUD")
			originalAuthURL = os.Getenv("OS_AUTH_URL")
			Expect(os.Unsetenv("OS_CLOUD")).To(Succeed())
			Expect(os.Unsetenv("OS_AUTH_URL")).To(Succeed())
		})

		AfterEach(func() {
			if originalOSCloud != "" {
				Expect(os.Setenv("OS_CLOUD", originalOSCloud)).To(Succeed())
			}
			if originalAuthURL != "" {
				Expect(os.Setenv("OS_AUTH_URL", originalAuthURL)).To(Succeed())
			}
		})

		It("should return an error when no credentials are available", func() {
			client, err := management.NewOpenStackClient(context.Background(), &management.Config{
				Type: testBackendType,
			})
			Expect(err).To(HaveOccurred())
			Expect(client).To(BeNil())
		})
	})

	Context("when OpenStack credentials are configured", func() {
		BeforeEach(func() {
			if !hasOpenStackCredentials() {
				Skip("Skipping: set MANAGEMENT_TEST_OPENSTACK=1 with valid OpenStack credentials to run")
			}
		})

		It("should create a client successfully", func() {
			client, err := management.NewOpenStackClient(context.Background(), &management.Config{
				Type: testBackendType,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(client).NotTo(BeNil())
		})
	})
})

func hasOpenStackCredentials() bool {
	if os.Getenv("MANAGEMENT_TEST_OPENSTACK") == "" {
		return false
	}
	return os.Getenv("OS_CLOUD") != "" || os.Getenv("OS_AUTH_URL") != ""
}
