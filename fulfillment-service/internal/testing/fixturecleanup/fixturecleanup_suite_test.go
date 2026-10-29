package fixturecleanup

import (
	"testing"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

func TestFixtureCleanup(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fixture cleanup")
}
