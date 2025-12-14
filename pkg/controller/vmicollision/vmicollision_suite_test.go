package vmicollision

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVMICollision(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VMI Collision Controller Suite")
}
