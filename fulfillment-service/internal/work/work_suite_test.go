package work

import (
	"log/slog"
	"testing"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/logging"
)

func TestWork(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Work package")
}

// Logger used for tests:
var logger *slog.Logger

var _ = BeforeSuite(func() {
	var err error

	// Create a logger that writes to the Ginkgo writer, so that the log messages will be attached to the output of
	// the right test:
	logger, err = logging.NewLogger().
		SetLevel(slog.LevelDebug.String()).
		SetOut(GinkgoWriter).
		Build()
	Expect(err).ToNot(HaveOccurred())
})
