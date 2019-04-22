package tests

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

)

var _ = Describe("Pods", func() {
	Context("Check the client", func() {
		It("should not fail", func() {
			_,err := testClient.KubeClient.CoreV1().Pods("").List(v1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
