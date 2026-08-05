package consoleproxy

import (
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

func TestConsoleProxy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Console Proxy Suite")
}
