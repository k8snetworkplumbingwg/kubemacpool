package pool_manager

import (
	"context"
	"net"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

var _ = Describe("migrate legacy vm configMap", func() {
	waitTimeSeconds := 10

	createPoolManager := func(startMacAddr, endMacAddr string, fakeObjectsForClient ...runtime.Object) *PoolManager {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(fakeObjectsForClient...).Build()
		startPoolRangeEnv, err := net.ParseMAC(startMacAddr)
		Expect(err).ToNot(HaveOccurred(), "should successfully parse starting mac address range")
		endPoolRangeEnv, err := net.ParseMAC(endMacAddr)
		Expect(err).ToNot(HaveOccurred(), "should successfully parse ending mac address range")
		poolManager, err := NewPoolManager(fakeClient, startPoolRangeEnv, endPoolRangeEnv, testManagerNamespace, false, waitTimeSeconds)
		Expect(err).ToNot(HaveOccurred(), "should successfully initialize poolManager")
		err = poolManager.Start()
		Expect(err).ToNot(HaveOccurred(), "should successfully start poolManager routines")

		return poolManager
	}
	var poolManager *PoolManager
	// Freeze time
	now := time.Now()
	timestamp := now.Format(time.RFC3339)
	BeforeEach(func() {
		poolManager = createPoolManager("02:00:00:00:00:00", "02:FF:FF:FF:FF:FF")
		Expect(poolManager).ToNot(BeNil())
	})
	Context("check migrate of legacy vm configMap to macPoolMap entries", func() {
		Context("and legacy vm configMap does not exist", func() {
			It("should not fail to run initMacMapFromLegacyConfigMap", func() {
				By("initiating the macPoolMap")
				Expect(poolManager.initMacMapFromLegacyConfigMap()).To(Succeed(), "should not fail migration if configMap does not exist")
				By("checking the configMap successfully deleted")
				_, err := poolManager.getVmMacWaitMap()
				Expect(apierrors.IsNotFound(err)).To(BeTrue(), "configMap should be removed after macPoolMap initialization")
				By("checking macPoolMap is empty")
				Expect(poolManager.macPoolMap).To(BeEmpty(), "migrate should not add any entries to macPoolMap")
			})
		})

		Context("and legacy vm configMap exists", func() {
			type initMacMapFromLegacyConfigMapParams struct {
				configMapEntries         map[string]string
				expectedMacsInMacPoolMap []string
			}
			table.DescribeTable("and running initMacMapFromLegacyConfigMapParams",
				func(i *initMacMapFromLegacyConfigMapParams) {
					By("updating configMap entries")
					legacyVmConfigMap := v1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{Namespace: testManagerNamespace, Name: names.WAITING_VMS_CONFIGMAP},
						Data:       i.configMapEntries,
					}
					err := poolManager.kubeClient.Create(context.Background(), &legacyVmConfigMap)
					Expect(err).ToNot(HaveOccurred(), "should succeed updating the configMap")
					By("initiating the macPoolMap")
					Expect(poolManager.initMacMapFromLegacyConfigMap()).To(Succeed(), "should not fail migration if configMap does not exist")

					By("checking the configMap successfully deleted")
					_, err = poolManager.getVmMacWaitMap()
					Expect(apierrors.IsNotFound(err)).To(BeTrue(), "configMap should be removed after macPoolMap initialization")
					By("checking entries migrated to macPoolMap")
					Expect(poolManager.macPoolMap).To(HaveLen(len(i.expectedMacsInMacPoolMap)))
					for _, macAddress := range i.expectedMacsInMacPoolMap {
						macEntry, exist := poolManager.macPoolMap[macAddress]
						Expect(exist).To(BeTrue(), "mac should be migrated to macPoolMap")
						Expect(macEntry.isDummyEntry()).To(BeTrue(), "mac entry should be marked as Dummy")
						expectedTimestamp, err := time.Parse(time.RFC3339Nano, timestamp)
						Expect(err).ToNot(HaveOccurred(), "should succeed to parse the migrated timestamp")
						Expect(*macEntry.transactionTimestamp).To(Equal(expectedTimestamp), "mac entry should be marked as Dummy")
					}
				},
				table.Entry("Should successfully migrate entries from legacy configMap when configMap is empty",
					&initMacMapFromLegacyConfigMapParams{
						configMapEntries:         map[string]string{},
						expectedMacsInMacPoolMap: []string{},
					}),
				table.Entry("Should successfully migrate entries from legacy configMap when configMap is not empty",
					&initMacMapFromLegacyConfigMapParams{
						configMapEntries:         map[string]string{"02:00:00:00:00:00": timestamp, "02:00:00:FF:00:00": timestamp, "02:00:00:00:00:FF": timestamp},
						expectedMacsInMacPoolMap: []string{"02:00:00:00:00:00", "02:00:00:FF:00:00", "02:00:00:00:00:FF"},
					}),
			)
		})
	})
})
