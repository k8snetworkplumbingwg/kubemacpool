package tests

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

const (
	updatedRangeStart = "0A:00:00:00:00:00"
	updatedRangeEnd   = "0A:00:00:00:00:FF"

	// Test timeouts
	rangeUpdateTimeout  = 30 * time.Second
	rangeUpdateInterval = 2 * time.Second

	// Test VM names
	vmBeforeUpdateName = "vm-before-update"
	vmAfterUpdateName  = "vm-after-update"
)

var _ = Describe("Day2 MAC Range Changes", Ordered, Label("mac-range-day2-update"), func() {
	var originalRangeStart, originalRangeEnd string

	BeforeAll(func() {
		Expect(initKubemacpoolParams()).To(Succeed())

		for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
			Expect(addLabelsToNamespace(namespace, map[string]string{vmNamespaceOptInLabel: "allocate"})).To(Succeed())
		}
		configMap, err := testClient.K8sClient.CoreV1().ConfigMaps(managerNamespace).Get(context.Background(),
			names.MAC_RANGE_CONFIGMAP, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "ConfigMap should always exist in deployment")

		originalRangeStart = configMap.Data["RANGE_START"]
		originalRangeEnd = configMap.Data["RANGE_END"]
	})

	AfterEach(func() {
		cleanupTestVMs()

		Expect(updateMacRangeConfigMap(originalRangeStart, originalRangeEnd)).To(Succeed())

		for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
			Expect(cleanNamespaceLabels(namespace)).To(Succeed())
		}
	})

	Context("When updating MAC ranges via ConfigMap", func() {
		It("should update ranges and allocate new MACs from updated range", func() {
			By("Creating a VM before range update to get MAC from original range")
			vmBefore, err := createTestVM(vmBeforeUpdateName)
			Expect(err).ToNot(HaveOccurred())
			macBefore, err := getMacAddressFromVM(vmBefore)
			Expect(err).ToNot(HaveOccurred())
			Expect(macBefore).ToNot(BeEmpty(), "VM should have MAC address allocated")
			inRange, err := isMacInRange(macBefore, originalRangeStart, originalRangeEnd)
			Expect(err).ToNot(HaveOccurred(), "MAC range check should not fail")
			Expect(inRange).To(BeTrue(), "MAC should be from original range")

			By("Updating MAC range ConfigMap")
			Expect(updateMacRangeConfigMap(updatedRangeStart, updatedRangeEnd)).To(Succeed())

			By("Creating a VM after range update to get MAC from new range")
			Eventually(func() string {
				vmAfter, vmErr := createTestVM(vmAfterUpdateName)
				if vmErr != nil {
					return ""
				}
				defer func() {
					_ = testClient.CRClient.Delete(context.Background(), vmAfter)
				}()
				macAfter, macErr := getMacAddressFromVM(vmAfter)
				if macErr != nil {
					return ""
				}
				return macAfter
			}, rangeUpdateTimeout, rangeUpdateInterval).Should(SatisfyAll(
				Not(BeEmpty()),
				BeInMACRange(updatedRangeStart, updatedRangeEnd),
			))

			By("Verifying original VM MAC is unchanged")
			unchangedMac, err := getMacAddressFromVM(vmBefore)
			Expect(err).ToNot(HaveOccurred())
			Expect(unchangedMac).To(Equal(macBefore), "existing VM MAC should remain unchanged")
		})

		It("should reject invalid MAC range updates", func() {
			By("Attempting to update with invalid MAC range")
			Expect(updateMacRangeConfigMap("invalid-mac", originalRangeEnd)).To(Succeed())

			By("Verifying range update was rejected (new allocations still use original range)")
			Eventually(func() string {
				vm, err := createTestVM("test-invalid-range")
				if err != nil {
					return ""
				}
				defer func() {
					_ = testClient.CRClient.Delete(context.Background(), vm)
				}()
				mac, err := getMacAddressFromVM(vm)
				if err != nil {
					return ""
				}
				return mac
			}, rangeUpdateTimeout, rangeUpdateInterval).Should(SatisfyAll(
				Not(BeEmpty()),
				BeInMACRange(originalRangeStart, originalRangeEnd),
			))
		})

		Context("When ConfigMap is deleted", func() {
			var deletionTestRangeStart, deletionTestRangeEnd string

			BeforeEach(func() {
				configMap, err := testClient.K8sClient.CoreV1().ConfigMaps(managerNamespace).Get(context.Background(),
					names.MAC_RANGE_CONFIGMAP, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				deletionTestRangeStart = configMap.Data["RANGE_START"]
				deletionTestRangeEnd = configMap.Data["RANGE_END"]
			})

			AfterEach(func() {
				Expect(createMacRangeConfigMap(deletionTestRangeStart, deletionTestRangeEnd)).To(Succeed())
			})

			It("should handle ConfigMap deletion gracefully", func() {
				By("Creating initial VM to verify current range before deletion")
				vm, err := createTestVM("test-deletion")
				Expect(err).ToNot(HaveOccurred())
				macBefore, err := getMacAddressFromVM(vm)
				Expect(err).ToNot(HaveOccurred())
				Expect(macBefore).ToNot(BeEmpty(), "VM should have MAC address allocated")
				inRange, err := isMacInRange(macBefore, deletionTestRangeStart, deletionTestRangeEnd)
				Expect(err).ToNot(HaveOccurred(), "MAC range check should not fail")
				Expect(inRange).To(BeTrue(), "Initial VM MAC should be from current range")

				By("Deleting the MAC range ConfigMap")
				err = testClient.K8sClient.CoreV1().ConfigMaps(managerNamespace).Delete(context.Background(),
					names.MAC_RANGE_CONFIGMAP, metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())

				By("Verifying MAC allocation continues from same range after ConfigMap deletion")
				Eventually(func() string {
					newVM, err := createTestVM("test-after-deletion")
					if err != nil {
						return ""
					}
					defer func() {
						_ = testClient.CRClient.Delete(context.Background(), newVM)
					}()
					mac, err := getMacAddressFromVM(newVM)
					if err != nil {
						return ""
					}
					return mac
				}, rangeUpdateTimeout, rangeUpdateInterval).Should(SatisfyAll(
					Not(BeEmpty()),
					BeInMACRange(deletionTestRangeStart, deletionTestRangeEnd),
				), "New VM should continue getting MAC from the same range that was active before ConfigMap deletion")
			})
		})

		It("should work with multiple range updates", func() {
			const thirdRangeStart = "0E:00:00:00:00:00"
			const thirdRangeEnd = "0E:00:00:00:00:FF"

			By("Performing first range update")
			Expect(updateMacRangeConfigMap(updatedRangeStart, updatedRangeEnd)).To(Succeed())

			By("Verifying first update works")
			Eventually(func() string {
				vm, err := createTestVM("test-first-update")
				if err != nil {
					return ""
				}
				defer func() {
					_ = testClient.CRClient.Delete(context.Background(), vm)
				}()
				mac, err := getMacAddressFromVM(vm)
				if err != nil {
					return ""
				}
				return mac
			}, rangeUpdateTimeout, rangeUpdateInterval).Should(SatisfyAll(
				Not(BeEmpty()),
				BeInMACRange(updatedRangeStart, updatedRangeEnd),
			))

			By("Performing second range update")
			Expect(updateMacRangeConfigMap(thirdRangeStart, thirdRangeEnd)).To(Succeed())

			By("Verifying second update works")
			Eventually(func() string {
				vm2, err := createTestVM("test-second-update")
				if err != nil {
					return ""
				}
				defer func() {
					_ = testClient.CRClient.Delete(context.Background(), vm2)
				}()
				mac, err := getMacAddressFromVM(vm2)
				if err != nil {
					return ""
				}
				return mac
			}, rangeUpdateTimeout, rangeUpdateInterval).Should(SatisfyAll(
				Not(BeEmpty()),
				BeInMACRange(thirdRangeStart, thirdRangeEnd),
			))
		})
	})
})

func updateMacRangeConfigMap(rangeStart, rangeEnd string) error {
	ctx := context.Background()

	configMap, err := testClient.K8sClient.CoreV1().ConfigMaps(managerNamespace).Get(ctx,
		names.MAC_RANGE_CONFIGMAP, metav1.GetOptions{})
	if err != nil {
		return err
	}

	configMap.Data["RANGE_START"] = rangeStart
	configMap.Data["RANGE_END"] = rangeEnd

	_, err = testClient.K8sClient.CoreV1().ConfigMaps(managerNamespace).Update(ctx,
		configMap, metav1.UpdateOptions{})
	return err
}

func createMacRangeConfigMap(rangeStart, rangeEnd string) error {
	ctx := context.Background()

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.MAC_RANGE_CONFIGMAP,
			Namespace: managerNamespace,
		},
		Data: map[string]string{
			"RANGE_START": rangeStart,
			"RANGE_END":   rangeEnd,
		},
	}

	_, err := testClient.K8sClient.CoreV1().ConfigMaps(managerNamespace).Create(ctx,
		configMap, metav1.CreateOptions{})
	return err
}

func createTestVM(name string) (*kubevirtv1.VirtualMachine, error) {
	vm := CreateVMObject(TestNamespace, []kubevirtv1.Interface{newInterface("default", "")}, []kubevirtv1.Network{newNetwork("default")})
	vm.Name = name

	err := testClient.CRClient.Create(context.Background(), vm)
	return vm, err
}

func getMacAddressFromVM(vm *kubevirtv1.VirtualMachine) (string, error) {
	updatedVM := &kubevirtv1.VirtualMachine{}
	err := testClient.CRClient.Get(context.Background(),
		client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, updatedVM)
	if err != nil {
		return "", err
	}

	if len(updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces) > 0 {
		return updatedVM.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress, nil
	}
	return "", nil
}

func isMacInRange(macStr, rangeStart, rangeEnd string) (bool, error) {
	if macStr == "" {
		return false, fmt.Errorf("MAC address cannot be empty")
	}

	mac, err := net.ParseMAC(macStr)
	if err != nil {
		return false, fmt.Errorf("invalid MAC address %q: %w", macStr, err)
	}

	start, err := net.ParseMAC(rangeStart)
	if err != nil {
		return false, fmt.Errorf("invalid range start MAC %q: %w", rangeStart, err)
	}

	end, err := net.ParseMAC(rangeEnd)
	if err != nil {
		return false, fmt.Errorf("invalid range end MAC %q: %w", rangeEnd, err)
	}

	inRange := bytes.Compare(mac, start) >= 0 && bytes.Compare(mac, end) <= 0
	return inRange, nil
}

// BeInMACRange is a custom Gomega matcher that provides descriptive error messages
// when MAC address range checking fails
func BeInMACRange(rangeStart, rangeEnd string) types.GomegaMatcher {
	return &macRangeMatcher{
		expectedStart: rangeStart,
		expectedEnd:   rangeEnd,
	}
}

type macRangeMatcher struct {
	expectedStart string
	expectedEnd   string
}

func (m *macRangeMatcher) Match(actual interface{}) (success bool, err error) {
	mac, ok := actual.(string)
	if !ok {
		return false, fmt.Errorf("BeInMACRange matcher expects a string, got %T", actual)
	}
	return isMacInRange(mac, m.expectedStart, m.expectedEnd)
}

func (m *macRangeMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected MAC address %s to be in range %s - %s", actual, m.expectedStart, m.expectedEnd)
}

func (m *macRangeMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected MAC address %s NOT to be in range %s - %s", actual, m.expectedStart, m.expectedEnd)
}

func cleanupTestVMs() {
	for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
		vmList := &kubevirtv1.VirtualMachineList{}
		err := testClient.CRClient.List(context.Background(), vmList, client.InNamespace(namespace))
		if err != nil {
			continue
		}

		for i := range vmList.Items {
			vm := &vmList.Items[i]
			_ = testClient.CRClient.Delete(context.Background(), vm)
		}
	}
}
