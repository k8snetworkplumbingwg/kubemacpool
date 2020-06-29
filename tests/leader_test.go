package tests

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/kubemacpool/tests/cmd"
)

var _ = Describe("Leader election mechanism", func() {
	Context("when kubemacpool pod is re-started with 1 replica", func() {
		BeforeEach(func() {
			By("Changing replicas to 0 to start from the beginning")
			err := changeManagerReplicas(0)
			Expect(err).To(Succeed(), "should succeed changing replicas to 0")

			By("Changing replicast to 1 to have just on pod with leader election")
			err = changeManagerReplicas(1)
			Expect(err).To(Succeed(), "should succeed changing replicas to 1")

			By("Retrieving leader pod")
			leaderPod, err := getLeaderPod()
			Expect(err).To(Succeed(), "should succeed getting leader kubemacpool pod")

			By("Sending SIGTERM signal to restart leader pod")
			killCmd := []string{"exec", "-n", leaderPod.Namespace, leaderPod.Name, "--", "/bin/bash", "-c", "kill -s SIGTERM 1"}
			output, err := cmd.Kubectl(killCmd...)
			Expect(err).To(Succeed(), fmt.Sprintf("should succeed killing leader kubemacpool pod: %s", output))
			waitManagerDeploymentReady()

		})
		AfterEach(func() {
			err := changeManagerReplicas(0)
			Expect(err).To(Succeed(), "should succeed changing replicas to 0")

			err = changeManagerReplicas(2)
			Expect(err).To(Succeed(), "should succeed changing replicas to 2")
		})
		It("should preserver leader label", func() {
			Consistently(func() error {
				_, err := getLeaderPod()
				return err
			}, 1*time.Minute, 1*time.Second).Should(Succeed(), "should succeed getting leader kubemacpool pod")
		})
	})
})
