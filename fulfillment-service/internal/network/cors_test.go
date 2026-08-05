package network

import (
	"net/http"
	"net/http/httptest"

	"github.com/spf13/pflag"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CORS middleware", func() {
	// verifyAllowedOrigin verifies that the given middleware allows the given origin.
	verifyAllowedOrigin := func(middleware func(http.Handler) http.Handler, origin, expected string) {
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Origin", origin)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		origins := recorder.Header().Get("Access-Control-Allow-Origin")
		ExpectWithOffset(1, origins).To(Equal(expected))
	}

	Context("Using the builder", func() {
		It("Fails if the logger is not set", func() {
			// Create the middleware without setting the logger:
			_, err := NewCorsMiddleware().
				Build()
			Expect(err).To(HaveOccurred())

			// Verify the result:
			Expect(err.Error()).To(ContainSubstring("logger"))
			Expect(err.Error()).To(ContainSubstring("mandatory"))
		})

		It("Uses default allowed origins", func() {
			// Create the middleware with the default allowed origins:
			middleware, err := NewCorsMiddleware().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the result:
			verifyAllowedOrigin(middleware, "http://my.com", "*")
		})

		It("Uses custom allowed origin", func() {
			// Create the middleware with the default allowed origins:
			middleware, err := NewCorsMiddleware().
				SetLogger(logger).
				AddAllowedOrigins("http://my.com").
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the result:
			verifyAllowedOrigin(middleware, "http://my.com", "http://my.com")
		})

		It("Uses multiple custom allowed origins from one call", func() {
			// Create the middleware with the default allowed origins:
			middleware, err := NewCorsMiddleware().
				SetLogger(logger).
				AddAllowedOrigins("http://my.com", "http://your.com").
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the result:
			verifyAllowedOrigin(middleware, "http://my.com", "http://my.com")
			verifyAllowedOrigin(middleware, "http://your.com", "http://your.com")
		})

		It("Uses multiple custom allowed origins from multiple calls", func() {
			// Create the middleware with the default allowed origins:
			middleware, err := NewCorsMiddleware().
				SetLogger(logger).
				AddAllowedOrigins("http://my.com").
				AddAllowedOrigins("http://your.com").
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the result:
			verifyAllowedOrigin(middleware, "http://my.com", "http://my.com")
			verifyAllowedOrigin(middleware, "http://your.com", "http://your.com")
		})
	})

	Context("Using flags", func() {
		var flags *pflag.FlagSet

		BeforeEach(func() {
			// Prepare the flag set:
			flags = pflag.NewFlagSet("", pflag.ContinueOnError)
			AddCorsFlags(flags, "my")
		})

		It("Uses default allowed origins", func() {
			// Prepare the flags:
			err := flags.Parse([]string{})
			Expect(err).ToNot(HaveOccurred())

			// Create the middleware with the default allowed origins:
			middleware, err := NewCorsMiddleware().
				SetLogger(logger).
				SetFlags(flags, "my").
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the result:
			verifyAllowedOrigin(middleware, "http://my.com", "*")
		})

		It("Uses custom allowed origins", func() {
			// Prepare the flags:
			err := flags.Parse([]string{
				"--my-cors-allowed-origins=http://my.com",
			})
			Expect(err).ToNot(HaveOccurred())

			// Create the middleware with the default allowed origins:
			middleware, err := NewCorsMiddleware().
				SetLogger(logger).
				SetFlags(flags, "my").
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the result:
			verifyAllowedOrigin(middleware, "http://my.com", "http://my.com")
		})

		It("Uses multiple custom allowed origins in one flag", func() {
			// Prepare the flags:
			err := flags.Parse([]string{
				"--my-cors-allowed-origins=http://my.com,http://your.com",
			})
			Expect(err).ToNot(HaveOccurred())

			// Create the middleware with the default allowed origins:
			middleware, err := NewCorsMiddleware().
				SetLogger(logger).
				SetFlags(flags, "my").
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the result:
			verifyAllowedOrigin(middleware, "http://my.com", "http://my.com")
			verifyAllowedOrigin(middleware, "http://your.com", "http://your.com")
		})

		It("Uses multiple custom allowed origins in multiple flags", func() {
			// Prepare the flags:
			err := flags.Parse([]string{
				"--my-cors-allowed-origins=http://my.com",
				"--my-cors-allowed-origins=http://your.com",
			})
			Expect(err).ToNot(HaveOccurred())

			// Create the middleware with the default allowed origins:
			middleware, err := NewCorsMiddleware().
				SetLogger(logger).
				SetFlags(flags, "my").
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the result:
			verifyAllowedOrigin(middleware, "http://my.com", "http://my.com")
			verifyAllowedOrigin(middleware, "http://your.com", "http://your.com")
		})
	})
})
