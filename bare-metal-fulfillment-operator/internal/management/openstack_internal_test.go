package management

import (
	"context"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/gophercloud/gophercloud/v2"
)

const (
	oldEndpoint = "http://old:6385/v1/"
	newEndpoint = "http://new:6385/v1/"
)

// SetServiceClientForTest sets the service client for testing purposes
func (c *OpenStackClient) SetServiceClientForTest(client *gophercloud.ServiceClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.client = client
	c.newServiceClient = func(ctx context.Context) (*gophercloud.ServiceClient, error) {
		return client, nil
	}
}

// NewServiceClientForTest overrides the newServiceClient function for testing purposes
func (c *OpenStackClient) NewServiceClientForTest(fn func(ctx context.Context) (*gophercloud.ServiceClient, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.newServiceClient = fn
}

var _ = Describe("isAuthError", func() {
	It("should return false for nil error", func() {
		Expect(isAuthError(nil)).To(BeFalse())
	})

	It("should return false for generic errors", func() {
		Expect(isAuthError(fmt.Errorf("some random error"))).To(BeFalse())
	})

	It("should return true for 401 ErrUnexpectedResponseCode", func() {
		err := gophercloud.ErrUnexpectedResponseCode{
			Actual:   http.StatusUnauthorized,
			Expected: []int{http.StatusOK},
		}
		Expect(isAuthError(err)).To(BeTrue())
	})

	It("should return false for 404 ErrUnexpectedResponseCode", func() {
		err := gophercloud.ErrUnexpectedResponseCode{
			Actual:   http.StatusNotFound,
			Expected: []int{http.StatusOK},
		}
		Expect(isAuthError(err)).To(BeFalse())
	})

	It("should return true for *ErrUnableToReauthenticate", func() {
		err := &gophercloud.ErrUnableToReauthenticate{
			ErrOriginal: fmt.Errorf("original"),
			ErrReauth:   fmt.Errorf("reauth failed"),
		}
		Expect(isAuthError(err)).To(BeTrue())
	})

	It("should return true for *ErrErrorAfterReauthentication", func() {
		err := &gophercloud.ErrErrorAfterReauthentication{
			ErrOriginal: fmt.Errorf("still failing"),
		}
		Expect(isAuthError(err)).To(BeTrue())
	})

	It("should return true for wrapped 401 error", func() {
		inner := gophercloud.ErrUnexpectedResponseCode{
			Actual:   http.StatusUnauthorized,
			Expected: []int{http.StatusOK},
		}
		wrapped := fmt.Errorf("operation failed: %w", inner)
		Expect(isAuthError(wrapped)).To(BeTrue())
	})

	It("should return false for wrapped non-auth error", func() {
		inner := gophercloud.ErrUnexpectedResponseCode{
			Actual:   http.StatusNotFound,
			Expected: []int{http.StatusOK},
		}
		wrapped := fmt.Errorf("operation failed: %w", inner)
		Expect(isAuthError(wrapped)).To(BeFalse())
	})
})

var _ = Describe("reconnect", func() {
	It("should swap the service client on success", func() {
		oldSC := &gophercloud.ServiceClient{Endpoint: oldEndpoint}
		newSC := &gophercloud.ServiceClient{Endpoint: newEndpoint}

		c := &OpenStackClient{
			client: oldSC,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) {
				return newSC, nil
			},
		}

		Expect(c.reconnect(context.Background())).To(Succeed())
		Expect(c.client).To(Equal(newSC))
		Expect(c.client.Endpoint).To(Equal(newEndpoint))
	})

	It("should return error when factory fails", func() {
		oldSC := &gophercloud.ServiceClient{Endpoint: oldEndpoint}

		c := &OpenStackClient{
			client: oldSC,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) {
				return nil, fmt.Errorf("keystone is down")
			},
		}

		err := c.reconnect(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("keystone is down"))
		Expect(c.client).To(Equal(oldSC), "should keep old client on failure")
	})

	It("should return error when factory returns nil client without error", func() {
		oldSC := &gophercloud.ServiceClient{Endpoint: oldEndpoint}

		c := &OpenStackClient{
			client: oldSC,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) {
				return nil, nil
			},
		}

		err := c.reconnect(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nil service client"))
		Expect(c.client).To(Equal(oldSC), "should keep old client on nil return")
	})

	It("should return error when new client has empty endpoint", func() {
		oldSC := &gophercloud.ServiceClient{Endpoint: oldEndpoint}

		c := &OpenStackClient{
			client: oldSC,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) {
				return &gophercloud.ServiceClient{Endpoint: ""}, nil
			},
		}

		err := c.reconnect(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no endpoint configured"))
		Expect(c.client).To(Equal(oldSC), "should keep old client on empty endpoint")
	})
})
