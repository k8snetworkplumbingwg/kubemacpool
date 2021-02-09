package pool_manager

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	kubevirt "kubevirt.io/client-go/api/v1"
)

var _ = Describe("mac-pool-map", func() {
	waitTimeSeconds := 10

	Context("check operations on macPoolMap entries", func() {
		poolManager := &PoolManager{}
		// Freeze time
		now := time.Now()
		timestampBeforeCurrentTimestamp := now.Add(-time.Second)
		currentTimestamp := now
		staleTimestamp := now.Add(-time.Duration(waitTimeSeconds+1) * time.Second)
		newTimestamp := now.Add(time.Second)
		BeforeEach(func() {
			poolManager.waitTime = waitTimeSeconds
			poolManager.macPoolMap = macMap{
				"02:00:00:00:00:00": macEntry{
					instanceName:         "vm/default/vm0",
					macInstanceKey:       "validInterface",
					transactionTimestamp: &currentTimestamp,
				},
				"02:00:00:00:00:01": macEntry{
					instanceName:         "vm/ns0/vm1",
					macInstanceKey:       "staleInterface",
					transactionTimestamp: &staleTimestamp,
				},
				"02:00:00:00:00:02": macEntry{
					instanceName:         "vm/ns2/vm2",
					macInstanceKey:       "validInterface",
					transactionTimestamp: &currentTimestamp,
				},
				"02:00:00:00:00:03": macEntry{
					instanceName:         "vm/ns3-4/vm3-4",
					macInstanceKey:       "staleInterface",
					transactionTimestamp: &staleTimestamp,
				},
				"02:00:00:00:00:04": macEntry{
					instanceName:         "vm/ns3-4/vm3-4",
					macInstanceKey:       "validInterface",
					transactionTimestamp: &currentTimestamp,
				},
				"02:00:00:00:00:05": macEntry{
					instanceName:         "vm/default/vm0",
					macInstanceKey:       "validInterface",
					transactionTimestamp: nil,
				},
			}
		})

		type filterInByInstanceNameParams struct {
			vmName           string
			expectedVmMacMap *macMap
		}
		table.DescribeTable("and performing filterInByInstanceName on macPoolMap",
			func(i *filterInByInstanceNameParams) {
				vmMacMap, err := poolManager.macPoolMap.filterInByInstanceName(i.vmName)
				Expect(err).ToNot(HaveOccurred(), "filterInByInstanceName should not return error")
				Expect(vmMacMap).To(Equal(i.expectedVmMacMap), "should match expected vm mac map")
			},
			table.Entry("Should return empty map when vm not in macPoolMap",
				&filterInByInstanceNameParams{
					vmName:           "some-random-name",
					expectedVmMacMap: &macMap{},
				}),
			table.Entry("Should return sub map of vm name: vm/default/vm0",
				&filterInByInstanceNameParams{
					vmName: "vm/default/vm0",
					expectedVmMacMap: &macMap{
						"02:00:00:00:00:00": macEntry{
							instanceName:         "vm/default/vm0",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
						"02:00:00:00:00:05": macEntry{
							instanceName:         "vm/default/vm0",
							macInstanceKey:       "validInterface",
							transactionTimestamp: nil,
						},
					},
				}),
			table.Entry("Should return sub map of vm name: vm/ns0/vm1",
				&filterInByInstanceNameParams{
					vmName: "vm/ns0/vm1",
					expectedVmMacMap: &macMap{
						"02:00:00:00:00:01": macEntry{
							instanceName:         "vm/ns0/vm1",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
					},
				}),
			table.Entry("Should return sub map of vm name: vm/ns2/vm2",
				&filterInByInstanceNameParams{
					vmName: "vm/ns2/vm2",
					expectedVmMacMap: &macMap{
						"02:00:00:00:00:02": macEntry{
							instanceName:         "vm/ns2/vm2",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
					},
				}),
			table.Entry("Should return sub map of vm name: vm/ns3-4/vm3-4",
				&filterInByInstanceNameParams{
					vmName: "vm/ns3-4/vm3-4",
					expectedVmMacMap: &macMap{
						"02:00:00:00:00:03": macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						"02:00:00:00:00:04": macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
					},
				}),
		)

		type alignMacEntryAccordingToVmInterfaceParams struct {
			macAddress       string
			vmName           string
			vmInterfaces     []kubevirt.Interface
			expectedExist    bool
			expectedMacEntry macEntry
		}
		table.DescribeTable("and performing alignMacEntryAccordingToVmInterface on macPoolMap entry",
			func(a *alignMacEntryAccordingToVmInterfaceParams) {
				poolManager.macPoolMap.alignMacEntryAccordingToVmInterface(a.macAddress, a.vmName, a.vmInterfaces)
				macEntry, exist := poolManager.macPoolMap[a.macAddress]
				Expect(exist).To(Equal(a.expectedExist))
				Expect(macEntry).To(Equal(a.expectedMacEntry), "should align mac entry according to current interface")
			},
			table.Entry("Should keep the mac entry and remove the transaction timestamp when interface exists in the vm interfaces list",
				&alignMacEntryAccordingToVmInterfaceParams{
					macAddress: "02:00:00:00:00:00",
					vmName:     "vm/default/vm0",
					vmInterfaces: []kubevirt.Interface{
						kubevirt.Interface{
							Name:       "validInterface",
							MacAddress: "02:00:00:00:00:00",
						},
					},
					expectedExist: true,
					expectedMacEntry: macEntry{
						instanceName:         "vm/default/vm0",
						macInstanceKey:       "validInterface",
						transactionTimestamp: nil,
					},
				}),
			table.Entry("Should remove the mac entry when interface does not exist in the vm interfaces list",
				&alignMacEntryAccordingToVmInterfaceParams{
					macAddress: "02:00:00:00:00:00",
					vmName:     "vm/default/vm0",
					vmInterfaces: []kubevirt.Interface{
						kubevirt.Interface{
							Name:       "validInterface",
							MacAddress: "02:00:00:00:00:01",
						},
					},
					expectedExist:    false,
					expectedMacEntry: macEntry{},
				}),
		)

		type updateMacTransactionTimestampForUpdatedMacsParams struct {
			vmName               string
			transactionTimestamp *time.Time
			updatedInterfaceMap  map[string]string
			shouldSucceed        bool
		}
		table.DescribeTable("and performing updateMacTransactionTimestampForUpdatedMacs on macPoolMap",
			func(u *updateMacTransactionTimestampForUpdatedMacsParams) {
				err := poolManager.macPoolMap.updateMacTransactionTimestampForUpdatedMacs(u.vmName, u.transactionTimestamp, u.updatedInterfaceMap)
				if !u.shouldSucceed {
					Expect(err).To(HaveOccurred(), "should fail updating mac transaction timestamp")
				} else {
					Expect(err).ToNot(HaveOccurred(), "should not fail updating macEntry")

					for _, macAddress := range u.updatedInterfaceMap {
						Expect(poolManager.macPoolMap[macAddress].transactionTimestamp).To(Equal(u.transactionTimestamp))
						Expect(poolManager.macPoolMap[macAddress].instanceName).To(Equal(u.vmName))
					}
				}
			},
			table.Entry("Should fail updating updating mac timestamp when the mac belongs to other vm",
				&updateMacTransactionTimestampForUpdatedMacsParams{
					vmName:               "new-vm",
					transactionTimestamp: &newTimestamp,
					updatedInterfaceMap:  map[string]string{"iface1": "02:00:00:00:00:00"},
					shouldSucceed:        false,
				}),
			table.Entry("Should fail updating mac timestamp when the mac does not exist in macPoolMap",
				&updateMacTransactionTimestampForUpdatedMacsParams{
					vmName:               "vm/default/vm0",
					transactionTimestamp: &newTimestamp,
					updatedInterfaceMap:  map[string]string{"iface1": "02:00:00:00:00:FF"},
					shouldSucceed:        false,
				}),
			table.Entry("Should succeed updating mac that already has a timestamp",
				&updateMacTransactionTimestampForUpdatedMacsParams{
					vmName:               "vm/default/vm0",
					transactionTimestamp: &newTimestamp,
					updatedInterfaceMap:  map[string]string{"iface1": "02:00:00:00:00:00"},
					shouldSucceed:        true,
				}),
			table.Entry("Should succeed updating mac that has a no pending transaction timestamp",
				&updateMacTransactionTimestampForUpdatedMacsParams{
					vmName:               "vm/default/vm0",
					transactionTimestamp: &newTimestamp,
					updatedInterfaceMap:  map[string]string{"iface1": "02:00:00:00:00:05"},
					shouldSucceed:        true,
				}),
		)

		type clearMacTransactionFromMacEntryParams struct {
			macAddress string
		}
		table.DescribeTable("and performing clearMacTransactionFromMacEntry on macPoolMap entry",
			func(c *clearMacTransactionFromMacEntryParams) {
				macPoolMapCopy := map[string]macEntry{}
				for macAddress, macEntry := range poolManager.macPoolMap {
					macPoolMapCopy[macAddress] = macEntry
				}

				poolManager.macPoolMap.clearMacTransactionFromMacEntry(c.macAddress)
				for macAddress, originalMacEntry := range macPoolMapCopy {
					updatedMacEntry, exist := poolManager.macPoolMap[macAddress]
					Expect(exist).To(BeTrue(), fmt.Sprintf("mac %s's entry should not be deleted from macPoolMap after running clearMacTransactionFromMacEntry", macAddress))
					if macAddress == c.macAddress {
						expectedMacEntry := macEntry{
							instanceName:         originalMacEntry.instanceName,
							macInstanceKey:       originalMacEntry.macInstanceKey,
							transactionTimestamp: nil,
						}
						Expect(updatedMacEntry).To(Equal(expectedMacEntry), fmt.Sprintf("cleaned mac entry %s should only remove transactionTimestamp from entry", macAddress))
					} else {
						Expect(updatedMacEntry).To(Equal(originalMacEntry), fmt.Sprintf("untouched mac entry %s should remain the same", macAddress))
					}
				}
			},
			table.Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:00",
				&clearMacTransactionFromMacEntryParams{
					macAddress: "02:00:00:00:00:00",
				}),
			table.Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:01",
				&clearMacTransactionFromMacEntryParams{
					macAddress: "02:00:00:00:00:01",
				}),
			table.Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:02",
				&clearMacTransactionFromMacEntryParams{
					macAddress: "02:00:00:00:00:02",
				}),
			table.Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:03",
				&clearMacTransactionFromMacEntryParams{
					macAddress: "02:00:00:00:00:03",
				}),
			table.Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:04",
				&clearMacTransactionFromMacEntryParams{
					macAddress: "02:00:00:00:00:04",
				}),
			table.Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:05",
				&clearMacTransactionFromMacEntryParams{
					macAddress: "02:00:00:00:00:05",
				}),
		)

		type findByMacAddressParams struct {
			macAddress    string
			shouldExist   bool
			expectedEntry macEntry
		}
		table.DescribeTable("and performing findByMacAddress on macPoolMap",
			func(f *findByMacAddressParams) {
				macEntry, exist := poolManager.macPoolMap.findByMacAddress(f.macAddress)
				Expect(exist).To(Equal(f.shouldExist), fmt.Sprintf("mac %s's entry existance should be as expected", f.macAddress))
				Expect(macEntry).To(Equal(f.expectedEntry), fmt.Sprintf("mac %s's entry should be as expected", f.macAddress))
			},
			table.Entry("Should not find non existent mac: 02:00:00:00:00:0F",
				&findByMacAddressParams{
					macAddress:    "02:00:00:00:00:0F",
					shouldExist:   false,
					expectedEntry: macEntry{},
				}),
			table.Entry("Should find mac in macPoolMap: 02:00:00:00:00:01",
				&findByMacAddressParams{
					macAddress:  "02:00:00:00:00:01",
					shouldExist: true,
					expectedEntry: macEntry{
						instanceName:         "vm/ns0/vm1",
						macInstanceKey:       "staleInterface",
						transactionTimestamp: &staleTimestamp,
					},
				}),
			table.Entry("Should find mac in macPoolMap: 02:00:00:00:00:02",
				&findByMacAddressParams{
					macAddress:  "02:00:00:00:00:02",
					shouldExist: true,
					expectedEntry: macEntry{
						instanceName:         "vm/ns2/vm2",
						macInstanceKey:       "validInterface",
						transactionTimestamp: &currentTimestamp,
					},
				}),
			table.Entry("Should find mac in macPoolMap: 02:00:00:00:00:05",
				&findByMacAddressParams{
					macAddress:  "02:00:00:00:00:05",
					shouldExist: true,
					expectedEntry: macEntry{
						instanceName:         "vm/default/vm0",
						macInstanceKey:       "validInterface",
						transactionTimestamp: nil,
					},
				}),
		)

		It("Should remove mac entry when running removeMacEntry", func() {
			for macAddress := range poolManager.macPoolMap {
				poolManager.macPoolMap.removeMacEntry(macAddress)
				_, exist := poolManager.macPoolMap[macAddress]
				Expect(exist).To(BeFalse(), "mac entry should be deleted by removeMacEntry")
			}
			Expect(poolManager.macPoolMap).To(BeEmpty(), "macPoolMap should be empty after removing all its entries")
		})

		type createOrUpdateInMacPoolMapParams struct {
			vmName        string
			macAddress    string
			interfaceName string
		}
		table.DescribeTable("and adding a new mac to macPoolMap",
			func(c *createOrUpdateInMacPoolMapParams) {
				poolManager.macPoolMap.createOrUpdateEntry(c.macAddress, c.vmName, c.interfaceName)
				updatedMacEntry, exist := poolManager.macPoolMap[c.macAddress]
				Expect(exist).To(BeTrue(), "mac entry should exist after added/updated")
				expectedMacEntry := macEntry{
					instanceName:         c.vmName,
					macInstanceKey:       c.interfaceName,
					transactionTimestamp: nil,
				}
				Expect(updatedMacEntry).To(Equal(expectedMacEntry), "macEntry should be added/updated")
			},
			table.Entry("Should succeed Adding a mac if mac is not in macPoolMap",
				&createOrUpdateInMacPoolMapParams{
					vmName:        "vm/default/vm0",
					interfaceName: "iface6",
					macAddress:    "02:00:00:00:00:06",
				}),
			table.Entry("Should succeed updating a mac if mac is already in macPoolMap",
				&createOrUpdateInMacPoolMapParams{
					vmName:        "vm/default/vm0",
					interfaceName: "validInterface",
					macAddress:    "02:00:00:00:00:00",
				}),
		)

		type filterMacsThatRequireCommitParams struct {
			latestPersistedTimestamp *time.Time
			expectedMacMap           macMap
		}
		table.DescribeTable("and performing filterMacsThatRequireCommit on macPoolMap",
			func(f *filterMacsThatRequireCommitParams) {
				testMacMap := poolManager.macPoolMap
				testMacMap.filterMacsThatRequireCommit(f.latestPersistedTimestamp, log.WithName("fake-logger"))
				Expect(testMacMap).To(Equal(f.expectedMacMap), "should get expected mac list")
			},
			table.Entry("Should only get stale mac entries if latestPersistedTimestamp is before the current timestamp",
				&filterMacsThatRequireCommitParams{
					latestPersistedTimestamp: &timestampBeforeCurrentTimestamp,
					expectedMacMap: macMap{
						"02:00:00:00:00:01": macEntry{
							instanceName:         "vm/ns0/vm1",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						"02:00:00:00:00:03": macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
					},
				}),
			table.Entry("Should only get all pending mac entries if latestPersistedTimestamp equals the current timestamp",
				&filterMacsThatRequireCommitParams{
					latestPersistedTimestamp: &currentTimestamp,
					expectedMacMap: macMap{
						"02:00:00:00:00:00": macEntry{
							instanceName:         "vm/default/vm0",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
						"02:00:00:00:00:01": macEntry{
							instanceName:         "vm/ns0/vm1",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						"02:00:00:00:00:02": macEntry{
							instanceName:         "vm/ns2/vm2",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
						"02:00:00:00:00:03": macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						"02:00:00:00:00:04": macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
					},
				}),
			table.Entry("Should only get all pending mac entries if latestPersistedTimestamp is after current timestamp",
				&filterMacsThatRequireCommitParams{
					latestPersistedTimestamp: &newTimestamp,
					expectedMacMap: macMap{
						"02:00:00:00:00:00": macEntry{
							instanceName:         "vm/default/vm0",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
						"02:00:00:00:00:01": macEntry{
							instanceName:         "vm/ns0/vm1",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						"02:00:00:00:00:02": macEntry{
							instanceName:         "vm/ns2/vm2",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
						"02:00:00:00:00:03": macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						"02:00:00:00:00:04": macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
					},
				}),
		)
	})
})
