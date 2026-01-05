/*
Copyright 2025 The KubeMacPool Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/kubemacpool/tests/kubectl"
)

const (
	CollisionAlertsLabel = "collision-alerts"
	collisionAlertName   = "KubemacpoolMACCollisionDetected"

	alertTimeout         = 6 * time.Minute
	alertPollingInterval = 5 * time.Second

	portForwardTimeout  = 30 * time.Second
	portForwardInterval = 1 * time.Second
)

var portForwardCmd *exec.Cmd

var _ = Describe("MAC Collision Alerts", Label(CollisionAlertsLabel), Serial, Ordered, func() {
	var (
		nadName1, nadName2 string
		prometheusClient   *PromClient
	)

	BeforeAll(func() {
		By("Patching Prometheus and restarting to pick up kubemacpool configuration")
		Expect(patchPrometheusForKubemacpool()).To(Succeed())

		nadName1 = randName("alert-br1")
		nadName2 = randName("alert-br2")
		By(fmt.Sprintf("Creating network attachment definitions: %s, %s", nadName1, nadName2))
		Expect(createNetworkAttachmentDefinition(TestNamespace, nadName1)).To(Succeed())
		Expect(createNetworkAttachmentDefinition(TestNamespace, nadName2)).To(Succeed())

		// Setup port forwarding to Prometheus
		sourcePort := 4321 + rand.Intn(6000) // #nosec G404 -- weak random is fine for test port selection
		targetPort := 9090
		By(fmt.Sprintf("Setting up port forwarding to Prometheus API on port %d", sourcePort))

		var err error
		portForwardCmd, err = kubectl.StartPortForwardCommand(prometheusMonitoringNamespace, "prometheus-k8s-0", sourcePort, targetPort)
		Expect(err).ToNot(HaveOccurred())

		waitForPortForwardReady(sourcePort)

		prometheusClient = NewPromClient(sourcePort, prometheusMonitoringNamespace)
	})

	AfterAll(func() {
		By("Removing port-forwarding command")
		Expect(kubectl.KillPortForwardCommand(portForwardCmd)).To(Succeed())

		By("Deleting network attachment definitions")
		Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName1)).To(Succeed())
		Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName2)).To(Succeed())
	})

	AfterEach(func() {
		vmiList := &kubevirtv1.VirtualMachineInstanceList{}
		Expect(testClient.CRClient.List(context.TODO(), vmiList)).To(Succeed())

		for i := range vmiList.Items {
			vmiObject := &vmiList.Items[i]
			err := testClient.CRClient.Delete(context.TODO(), vmiObject)
			if err != nil && !apierrors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}
		}

		Eventually(func() []kubevirtv1.VirtualMachineInstance {
			vmiList := &kubevirtv1.VirtualMachineInstanceList{}
			Expect(testClient.CRClient.List(context.TODO(), vmiList)).To(Succeed())
			return vmiList.Items
		}).WithTimeout(timeout).WithPolling(pollingInterval).Should(HaveLen(0), "failed to remove all VMI objects")
	})

	It("should trigger alert when collisions exist and clear when resolved", func() {
		const (
			mac1 = "02:00:00:00:aa:01"
			mac2 = "02:00:00:00:aa:02"
		)

		By("Step 1: Verifying alert is not firing initially")
		expectAlertNotFiring(prometheusClient, collisionAlertName)

		By("Step 2: Creating 2 sets of VMIs colliding on 2 different MACs")
		// Set 1: vmi1a and vmi1b collide on mac1
		vmi1a := NewVMI(TestNamespace, "test-alert-1a",
			WithInterface(newInterface(nadName1, mac1)),
			WithNetwork(newNetwork(nadName1)))
		Expect(testClient.CRClient.Create(context.TODO(), vmi1a)).To(Succeed())

		vmi1b := NewVMI(TestNamespace, "test-alert-1b",
			WithInterface(newInterface(nadName2, mac1)),
			WithNetwork(newNetwork(nadName2)))
		Expect(testClient.CRClient.Create(context.TODO(), vmi1b)).To(Succeed())

		// Set 2: vmi2a and vmi2b collide on mac2
		vmi2a := NewVMI(TestNamespace, "test-alert-2a",
			WithInterface(newInterface(nadName1, mac2)),
			WithNetwork(newNetwork(nadName1)))
		Expect(testClient.CRClient.Create(context.TODO(), vmi2a)).To(Succeed())

		vmi2b := NewVMI(TestNamespace, "test-alert-2b",
			WithInterface(newInterface(nadName2, mac2)),
			WithNetwork(newNetwork(nadName2)))
		Expect(testClient.CRClient.Create(context.TODO(), vmi2b)).To(Succeed())

		waitForVMIsRunning([]vmiReference{
			{vmi1a.Namespace, vmi1a.Name},
			{vmi1b.Namespace, vmi1b.Name},
			{vmi2a.Namespace, vmi2a.Name},
			{vmi2b.Namespace, vmi2b.Name},
		})

		By("Step 3: Verifying alert is firing (2 collisions exist)")
		expectAlertFiring(prometheusClient, collisionAlertName)

		By("Step 4: Removing 1 VMI from first collision set (mac1 collision cleared)")
		Expect(testClient.CRClient.Delete(context.TODO(), vmi1a)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(testClient.CRClient.Get(context.TODO(), client.ObjectKey{
				Namespace: vmi1a.Namespace,
				Name:      vmi1a.Name,
			}, &kubevirtv1.VirtualMachineInstance{}))
		}).WithTimeout(timeout).WithPolling(pollingInterval).Should(BeTrue())

		By("Step 5: Verifying alert is still firing (mac2 collision still exists)")
		expectAlertFiring(prometheusClient, collisionAlertName)

		By("Step 6: Removing 1 VMI from second collision set (all collisions cleared)")
		Expect(testClient.CRClient.Delete(context.TODO(), vmi2a)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(testClient.CRClient.Get(context.TODO(), client.ObjectKey{
				Namespace: vmi2a.Namespace,
				Name:      vmi2a.Name,
			}, &kubevirtv1.VirtualMachineInstance{}))
		}).WithTimeout(timeout).WithPolling(pollingInterval).Should(BeTrue())

		By("Step 7: Verifying alert is no longer firing")
		expectAlertNotFiring(prometheusClient, collisionAlertName)
	})
})

// expectAlertFiring waits for the specified alert to be firing
func expectAlertFiring(p *PromClient, alertName string) {
	By(fmt.Sprintf("Waiting for alert %s to fire", alertName))
	Eventually(func() bool { return p.IsAlertFiring(alertName) }).
		WithTimeout(alertTimeout).WithPolling(alertPollingInterval).Should(BeTrue(),
		fmt.Sprintf("alert %s should be firing", alertName))
}

// expectAlertNotFiring waits for the specified alert to stop firing
func expectAlertNotFiring(p *PromClient, alertName string) {
	By(fmt.Sprintf("Waiting for alert %s to stop firing", alertName))
	Eventually(func() bool { return p.IsAlertFiring(alertName) }).
		WithTimeout(alertTimeout).WithPolling(alertPollingInterval).Should(BeFalse(),
		fmt.Sprintf("alert %s should not be firing", alertName))
}

func waitForPortForwardReady(port int) {
	By("Waiting for Prometheus port-forward to be ready")
	Eventually(func() bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}).WithTimeout(portForwardTimeout).WithPolling(portForwardInterval).Should(BeTrue(),
		"port-forward to Prometheus should be ready")
}
