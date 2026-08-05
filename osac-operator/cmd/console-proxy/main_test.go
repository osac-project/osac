package main

import (
	"io"
	"log/slog"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/internal/consoleproxy"
)

func TestConsoleProxy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Console Proxy Suite")
}

var _ = Describe("buildConfigResolver", func() {
	var (
		fakeClient client.Client
		hubConfig  *rest.Config
		logger     *slog.Logger
	)

	BeforeEach(func() {
		scheme := runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		hubConfig = &rest.Config{Host: "https://fake:6443"}
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	})

	DescribeTable("with a valid mode returns the correct resolver type",
		func(mode string, expectedType any) {
			resolver, err := buildConfigResolver(mode, fakeClient, hubConfig, logger)
			Expect(err).NotTo(HaveOccurred())
			Expect(resolver).To(BeAssignableToTypeOf(expectedType))
		},
		Entry("local mode", consoleproxy.VMClusterModeLocal, &consoleproxy.LocalConfigResolver{}),
		Entry("remote mode", consoleproxy.VMClusterModeRemote, &consoleproxy.RemoteConfigResolver{}),
		Entry("auto mode", consoleproxy.VMClusterModeAuto, &consoleproxy.AutoConfigResolver{}),
	)

	Context("with an invalid mode", func() {
		It("returns an error", func() {
			resolver, err := buildConfigResolver("bogus", fakeClient, hubConfig, logger)
			Expect(err).To(HaveOccurred())
			Expect(resolver).To(BeNil())
		})
	})
})
