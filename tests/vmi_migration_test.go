package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

const migrationTimeout = 8 * time.Minute

// TODO: the rfe_id was taken from kubernetes-nmstate we have to discover the right parameters here
var _ = Describe("[rfe_id:3503][crit:medium][vendor:cnv-qe@redhat.com][level:component]VMI Migration Collision Detection",
	Label(MACCollisionDetectionLabel), Ordered, func() {
		BeforeEach(func() {
			err := initKubemacpoolParams()
			Expect(err).ToNot(HaveOccurred())
		})
		BeforeEach(func() {
			By("Verify that there are no VMs left from previous tests")
			currentVMList := &kubevirtv1.VirtualMachineList{}
			err := testClient.CRClient.List(context.TODO(), currentVMList)
			Expect(err).ToNot(HaveOccurred(), "Should successfully list VMs")
			Expect(len(currentVMList.Items)).To(BeZero(), "There should be no VMs in the cluster before a test")

			By("Verify that there are no VMIs left from previous tests")
			currentVMIList := &kubevirtv1.VirtualMachineInstanceList{}
			err = testClient.CRClient.List(context.TODO(), currentVMIList)
			Expect(err).ToNot(HaveOccurred(), "Should successfully list VMIs")
			Expect(len(currentVMIList.Items)).To(BeZero(), "There should be no VMIs in the cluster before a test")

			// remove all the labels from the test namespaces
			for _, namespace := range []string{TestNamespace, OtherTestNamespace} {
				err = cleanNamespaceLabels(namespace)
				Expect(err).ToNot(HaveOccurred(), "should be able to remove the namespace labels")
			}
		})

		Context("When VMIs are part of cross-namespace live migration", func() {
			var nadName string

			BeforeEach(func() {
				By("Enabling DecentralizedLiveMigration feature gate")
				Expect(enableKubeVirtFeatureGate("DecentralizedLiveMigration")).To(Succeed())

				nadName = randName("net-migration")
				By(fmt.Sprintf("Creating network attachment definition %s in both namespaces", nadName))
				Expect(createNetworkAttachmentDefinition(TestNamespace, nadName)).To(Succeed())
				Expect(createNetworkAttachmentDefinition(OtherTestNamespace, nadName)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up VirtualMachineInstanceMigrations")
				migrationList := &kubevirtv1.VirtualMachineInstanceMigrationList{}
				Expect(testClient.CRClient.List(context.TODO(), migrationList)).To(Succeed())

				for i := range migrationList.Items {
					migrationObject := &migrationList.Items[i]
					err := testClient.CRClient.Delete(context.TODO(), migrationObject)
					if err != nil {
						GinkgoWriter.Printf("Error deleting Migration %s/%s: %v\n", migrationObject.Namespace, migrationObject.Name, err)
					}
				}

				By("Cleaning up VMs")
				vmList := &kubevirtv1.VirtualMachineList{}
				Expect(testClient.CRClient.List(context.TODO(), vmList)).To(Succeed())

				for i := range vmList.Items {
					vmObject := &vmList.Items[i]
					err := testClient.CRClient.Delete(context.TODO(), vmObject)
					if err != nil {
						GinkgoWriter.Printf("Error deleting VM %s/%s: %v\n", vmObject.Namespace, vmObject.Name, err)
					}
				}

				Eventually(func() []kubevirtv1.VirtualMachine {
					vmList := &kubevirtv1.VirtualMachineList{}
					Expect(testClient.CRClient.List(context.TODO(), vmList)).To(Succeed())
					return vmList.Items
				}).WithTimeout(timeout).WithPolling(pollingInterval).Should(HaveLen(0), "failed to remove all VM objects")

				By("Deleting network attachment definitions")
				Expect(deleteNetworkAttachmentDefinition(TestNamespace, nadName)).To(Succeed())
				Expect(deleteNetworkAttachmentDefinition(OtherTestNamespace, nadName)).To(Succeed())
			})

			It("should not report collision for VMIs that are part of same cross-namespace migration", func() {
				const sharedMAC = "02:00:00:00:00:50"
				const migrationID = "test-cross-ns-mig"

				By("Creating source VM in TestNamespace")
				sourceVM := CreateVMObject(TestNamespace,
					[]kubevirtv1.Interface{newInterface(nadName, sharedMAC)},
					[]kubevirtv1.Network{newNetwork(nadName)})
				runStrategyAlways := kubevirtv1.RunStrategyAlways
				sourceVM.Spec.RunStrategy = &runStrategyAlways
				Expect(testClient.CRClient.Create(context.TODO(), sourceVM)).To(Succeed())

				sourceVMINamespace := sourceVM.Namespace
				sourceVMIName := sourceVM.Name
				waitForVMIsRunning([]vmiReference{{sourceVMINamespace, sourceVMIName}})

				By("Creating target VM in OtherTestNamespace with same MAC (as receiver)")
				targetVM := CreateVMObject(OtherTestNamespace,
					[]kubevirtv1.Interface{newInterface(nadName, sharedMAC)},
					[]kubevirtv1.Network{newNetwork(nadName)})
				runStrategyWaitAsReceiver := kubevirtv1.RunStrategyWaitAsReceiver
				targetVM.Spec.RunStrategy = &runStrategyWaitAsReceiver
				if targetVM.Annotations == nil {
					targetVM.Annotations = make(map[string]string)
				}
				targetVM.Annotations[kubevirtv1.RestoreRunStrategy] = string(kubevirtv1.RunStrategyAlways)
				Expect(testClient.CRClient.Create(context.TODO(), targetVM)).To(Succeed())

				By("Waiting for target VMI to be in WaitingForSync phase")
				targetVMINamespace := targetVM.Namespace
				targetVMIName := targetVM.Name
				Eventually(func() kubevirtv1.VirtualMachineInstancePhase {
					return getVMIPhase(targetVMINamespace, targetVMIName)
				}).WithTimeout(timeout).WithPolling(pollingInterval).Should(Equal(kubevirtv1.WaitingForSync))

				By("Creating target migration object FIRST to get synchronization address")
				targetMigration := &kubevirtv1.VirtualMachineInstanceMigration{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "target-mig-" + migrationID,
						Namespace: OtherTestNamespace,
					},
					Spec: kubevirtv1.VirtualMachineInstanceMigrationSpec{
						VMIName: targetVM.Name,
						Receive: &kubevirtv1.VirtualMachineInstanceMigrationTarget{
							MigrationID: migrationID,
						},
					},
				}
				Expect(testClient.CRClient.Create(context.TODO(), targetMigration)).To(Succeed())

				By("Waiting for target migration to populate synchronization address")
				var syncAddress string
				Eventually(func(g Gomega) {
					updatedTargetMigration := &kubevirtv1.VirtualMachineInstanceMigration{}
					err := testClient.CRClient.Get(context.TODO(),
						client.ObjectKey{Namespace: targetMigration.Namespace, Name: targetMigration.Name}, updatedTargetMigration)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(updatedTargetMigration.Status.SynchronizationAddresses).ToNot(BeEmpty(),
						"target migration should have synchronization address")
					syncAddress = updatedTargetMigration.Status.SynchronizationAddresses[0]
				}).WithTimeout(timeout).WithPolling(pollingInterval).Should(Succeed(), "Target migration should populate synchronization address")

				By(fmt.Sprintf("Creating source migration object with synchronization address: %s", syncAddress))
				sourceMigration := &kubevirtv1.VirtualMachineInstanceMigration{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "source-mig-" + migrationID,
						Namespace: TestNamespace,
					},
					Spec: kubevirtv1.VirtualMachineInstanceMigrationSpec{
						VMIName: sourceVM.Name,
						SendTo: &kubevirtv1.VirtualMachineInstanceMigrationSource{
							MigrationID: migrationID,
							ConnectURL:  syncAddress,
						},
					},
				}
				Expect(testClient.CRClient.Create(context.TODO(), sourceMigration)).To(Succeed())

				By("Waiting for migrations to complete successfully")
				Eventually(func(g Gomega) {
					updatedTargetMigration := &kubevirtv1.VirtualMachineInstanceMigration{}
					err := testClient.CRClient.Get(context.TODO(),
						client.ObjectKey{Namespace: targetMigration.Namespace, Name: targetMigration.Name}, updatedTargetMigration)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(updatedTargetMigration.Status.Phase).To(Equal(kubevirtv1.MigrationSucceeded))

					// Source migration may be NotFound (GC'd after VMI deletion) - that's expected behavior
					// for decentralized cross-namespace migrations
					updatedSourceMigration := &kubevirtv1.VirtualMachineInstanceMigration{}
					err = testClient.CRClient.Get(context.TODO(),
						client.ObjectKey{Namespace: sourceMigration.Namespace, Name: sourceMigration.Name}, updatedSourceMigration)
					if err != nil {
						// NotFound is acceptable - source migration gets GC'd when source VMI is deleted
						g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
							"source migration should either exist or be NotFound (GC'd)")
						return
					}
					g.Expect(updatedSourceMigration.Status.Phase).To(Equal(kubevirtv1.MigrationSucceeded))
				}).WithTimeout(migrationTimeout).WithPolling(pollingInterval).Should(Succeed(), "Migrations should complete successfully")

				By("Verifying NO collision events on target VMI after migration completes")
				// Note: source VMI no longer exists after cross-namespace migration completes
				expectNoMACCollisionEvents([]vmiReference{{targetVMINamespace, targetVMIName}},
					"VMI is part of cross-namespace migration")
			})
		})
	})
