package tests

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)


func TestTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tests Suite")
}

var _ = BeforeSuite(func() {
	var err error
	testClient, err = NewTestClient()
	Expect(err).ToNot(HaveOccurred())

	err = createTestNamespaces()
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	removeTestNamespaces()
})
