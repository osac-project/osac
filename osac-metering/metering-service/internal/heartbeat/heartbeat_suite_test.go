package heartbeat_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHeartbeat(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Heartbeat Suite")
}
