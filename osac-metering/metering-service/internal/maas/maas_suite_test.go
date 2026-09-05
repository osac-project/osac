package maas_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMaaS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MaaS Suite")
}
