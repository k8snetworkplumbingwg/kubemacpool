package maccollision

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMACCollision(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MAC Collision Controller Suite")
}
