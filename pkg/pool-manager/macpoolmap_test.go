package pool_manager

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kubevirt "kubevirt.io/api/core/v1"
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
				NewMacKey("02:00:00:00:00:00"): []macEntry{{
					instanceName:         "vm/default/vm0",
					macInstanceKey:       "validInterface",
					transactionTimestamp: &currentTimestamp,
				}},
				NewMacKey("02:00:00:00:00:01"): []macEntry{{
					instanceName:         "vm/ns0/vm1",
					macInstanceKey:       "staleInterface",
					transactionTimestamp: &staleTimestamp,
				}},
				NewMacKey("02:00:00:00:00:02"): []macEntry{{
					instanceName:         "vm/ns2/vm2",
					macInstanceKey:       "validInterface",
					transactionTimestamp: &currentTimestamp,
				}},
				NewMacKey("02:00:00:00:00:03"): []macEntry{{
					instanceName:         "vm/ns3-4/vm3-4",
					macInstanceKey:       "staleInterface",
					transactionTimestamp: &staleTimestamp,
				}},
				NewMacKey("02:00:00:00:00:04"): []macEntry{{
					instanceName:         "vm/ns3-4/vm3-4",
					macInstanceKey:       "validInterface",
					transactionTimestamp: &currentTimestamp,
				}},
				NewMacKey("02:00:00:00:00:05"): []macEntry{{
					instanceName:         "vm/default/vm0",
					macInstanceKey:       "validInterface",
					transactionTimestamp: nil,
				}},
			}
		})

		type filterInByInstanceNameParams struct {
			vmName           string
			expectedVmMacMap *macMap
		}
		DescribeTable("and performing filterInByInstanceName on macPoolMap",
			func(i *filterInByInstanceNameParams) {
				vmMacMap, err := poolManager.macPoolMap.filterInByInstanceName(i.vmName)
				Expect(err).ToNot(HaveOccurred(), "filterInByInstanceName should not return error")
				Expect(vmMacMap).To(Equal(i.expectedVmMacMap), "should match expected vm mac map")
			},
			Entry("Should return empty map when vm not in macPoolMap",
				&filterInByInstanceNameParams{
					vmName:           "some-random-name",
					expectedVmMacMap: &macMap{},
				}),
			Entry("Should return sub map of vm name: vm/default/vm0",
				&filterInByInstanceNameParams{
					vmName: "vm/default/vm0",
					expectedVmMacMap: &macMap{
						NewMacKey("02:00:00:00:00:00"): []macEntry{{
							instanceName:         "vm/default/vm0",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						}},
						NewMacKey("02:00:00:00:00:05"): []macEntry{{
							instanceName:         "vm/default/vm0",
							macInstanceKey:       "validInterface",
							transactionTimestamp: nil,
						}},
					},
				}),
			Entry("Should return sub map of vm name: vm/ns0/vm1",
				&filterInByInstanceNameParams{
					vmName: "vm/ns0/vm1",
					expectedVmMacMap: &macMap{
						NewMacKey("02:00:00:00:00:01"): []macEntry{{
							instanceName:         "vm/ns0/vm1",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						}},
					},
				}),
			Entry("Should return sub map of vm name: vm/ns2/vm2",
				&filterInByInstanceNameParams{
					vmName: "vm/ns2/vm2",
					expectedVmMacMap: &macMap{
						NewMacKey("02:00:00:00:00:02"): []macEntry{{
							instanceName:         "vm/ns2/vm2",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						}},
					},
				}),
			Entry("Should return sub map of vm name: vm/ns3-4/vm3-4",
				&filterInByInstanceNameParams{
					vmName: "vm/ns3-4/vm3-4",
					expectedVmMacMap: &macMap{
						NewMacKey("02:00:00:00:00:03"): []macEntry{{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						}},
						NewMacKey("02:00:00:00:00:04"): []macEntry{{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						}},
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
		DescribeTable("and performing alignMacEntryAccordingToVmInterface on macPoolMap entry",
			func(a *alignMacEntryAccordingToVmInterfaceParams) {
				poolManager.macPoolMap.alignMacEntryAccordingToVmInterface(a.macAddress, a.vmName, a.vmInterfaces)
				entries, exist := poolManager.macPoolMap[NewMacKey(a.macAddress)]
				Expect(exist).To(Equal(a.expectedExist))
				if a.expectedExist {
					Expect(entries).To(HaveLen(1))
					Expect(entries[0]).To(Equal(a.expectedMacEntry), "should align mac entry according to current interface")
				}
			},
			Entry("Should keep the mac entry and remove the transaction timestamp when interface exists in the vm interfaces list",
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
			Entry("Should remove the mac entry when interface does not exist in the vm interfaces list",
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
		DescribeTable("and performing updateMacTransactionTimestampForUpdatedMacs on macPoolMap",
			func(u *updateMacTransactionTimestampForUpdatedMacsParams) {
				err := poolManager.macPoolMap.updateMacTransactionTimestampForUpdatedMacs(u.vmName, u.transactionTimestamp, u.updatedInterfaceMap)
				if !u.shouldSucceed {
					Expect(err).To(HaveOccurred(), "should fail updating mac transaction timestamp")
				} else {
					Expect(err).ToNot(HaveOccurred(), "should not fail updating macEntry")

					for _, macAddress := range u.updatedInterfaceMap {
						entries := poolManager.macPoolMap[NewMacKey(macAddress)]
						Expect(entries).ToNot(BeEmpty(), "mac entry should exist")
						found := false
						for _, entry := range entries {
							if entry.instanceName == u.vmName {
								Expect(entry.transactionTimestamp).To(Equal(u.transactionTimestamp))
								found = true
								break
							}
						}
						Expect(found).To(BeTrue(), "entry for vm should exist")
					}
				}
			},
			Entry("Should fail updating updating mac timestamp when the mac belongs to other vm",
				&updateMacTransactionTimestampForUpdatedMacsParams{
					vmName:               "new-vm",
					transactionTimestamp: &newTimestamp,
					updatedInterfaceMap:  map[string]string{"iface1": "02:00:00:00:00:00"},
					shouldSucceed:        false,
				}),
			Entry("Should fail updating mac timestamp when the mac does not exist in macPoolMap",
				&updateMacTransactionTimestampForUpdatedMacsParams{
					vmName:               "vm/default/vm0",
					transactionTimestamp: &newTimestamp,
					updatedInterfaceMap:  map[string]string{"iface1": "02:00:00:00:00:FF"},
					shouldSucceed:        false,
				}),
			Entry("Should succeed updating mac that already has a timestamp",
				&updateMacTransactionTimestampForUpdatedMacsParams{
					vmName:               "vm/default/vm0",
					transactionTimestamp: &newTimestamp,
					updatedInterfaceMap:  map[string]string{"iface1": "02:00:00:00:00:00"},
					shouldSucceed:        true,
				}),
			Entry("Should succeed updating mac that has a no pending transaction timestamp",
				&updateMacTransactionTimestampForUpdatedMacsParams{
					vmName:               "vm/default/vm0",
					transactionTimestamp: &newTimestamp,
					updatedInterfaceMap:  map[string]string{"iface1": "02:00:00:00:00:05"},
					shouldSucceed:        true,
				}),
		)

		type clearMacTransactionFromMacEntryParams struct {
			macAddress       string
			instanceFullName string
		}
		DescribeTable("and performing clearMacTransactionFromMacEntry on macPoolMap entry",
			func(c *clearMacTransactionFromMacEntryParams) {
				macPoolMapCopy := map[macKey][]macEntry{}
				for macAddress, entries := range poolManager.macPoolMap {
					entriesCopy := make([]macEntry, len(entries))
					copy(entriesCopy, entries)
					macPoolMapCopy[macAddress] = entriesCopy
				}

				poolManager.macPoolMap.clearMacTransactionFromMacEntry(c.macAddress, c.instanceFullName)
				for macAddress, originalEntries := range macPoolMapCopy {
					updatedEntries, exist := poolManager.macPoolMap[macAddress]
					Expect(exist).To(BeTrue(), fmt.Sprintf("mac %s's entries should not be deleted from macPoolMap after running clearMacTransactionFromMacEntry", macAddress))
					Expect(updatedEntries).To(HaveLen(len(originalEntries)), fmt.Sprintf("mac %s should have same number of entries", macAddress))

					if macAddress.String() == c.macAddress {
						for i, originalEntry := range originalEntries {
							if originalEntry.instanceName == c.instanceFullName {
								expectedEntry := macEntry{
									instanceName:         originalEntry.instanceName,
									macInstanceKey:       originalEntry.macInstanceKey,
									transactionTimestamp: nil,
								}
								Expect(updatedEntries[i]).To(Equal(expectedEntry), fmt.Sprintf("cleaned mac entry %s for instance %s should only remove transactionTimestamp", macAddress, c.instanceFullName))
							} else {
								Expect(updatedEntries[i]).To(Equal(originalEntry), fmt.Sprintf("other entries for mac %s should remain unchanged", macAddress))
							}
						}
					} else {
						Expect(updatedEntries).To(Equal(originalEntries), fmt.Sprintf("untouched mac %s entries should remain the same", macAddress))
					}
				}
			},
			Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:00",
				&clearMacTransactionFromMacEntryParams{
					macAddress:       "02:00:00:00:00:00",
					instanceFullName: "vm/default/vm0",
				}),
			Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:01",
				&clearMacTransactionFromMacEntryParams{
					macAddress:       "02:00:00:00:00:01",
					instanceFullName: "vm/ns0/vm1",
				}),
			Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:02",
				&clearMacTransactionFromMacEntryParams{
					macAddress:       "02:00:00:00:00:02",
					instanceFullName: "vm/ns2/vm2",
				}),
			Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:03",
				&clearMacTransactionFromMacEntryParams{
					macAddress:       "02:00:00:00:00:03",
					instanceFullName: "vm/ns3-4/vm3-4",
				}),
			Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:04",
				&clearMacTransactionFromMacEntryParams{
					macAddress:       "02:00:00:00:00:04",
					instanceFullName: "vm/ns3-4/vm3-4",
				}),
			Entry("Should only remove the timestamp from the mac entry mac: 02:00:00:00:00:05",
				&clearMacTransactionFromMacEntryParams{
					macAddress:       "02:00:00:00:00:05",
					instanceFullName: "vm/default/vm0",
				}),
		)

		type findByMacAddressParams struct {
			macAddress    string
			shouldExist   bool
			expectedEntry macEntry
		}
		DescribeTable("and performing findByMacAddress on macPoolMap",
			func(f *findByMacAddressParams) {
				entries, exist := poolManager.macPoolMap.findByMacAddress(f.macAddress)
				Expect(exist).To(Equal(f.shouldExist), fmt.Sprintf("mac %s's entry existance should be as expected", f.macAddress))
				if f.shouldExist {
					Expect(entries).To(HaveLen(1))
					Expect(entries[0]).To(Equal(f.expectedEntry), fmt.Sprintf("mac %s's entry should be as expected", f.macAddress))
				} else {
					Expect(entries).To(BeEmpty())
				}
			},
			Entry("Should not find non existent mac: 02:00:00:00:00:0F",
				&findByMacAddressParams{
					macAddress:    "02:00:00:00:00:0F",
					shouldExist:   false,
					expectedEntry: macEntry{},
				}),
			Entry("Should find mac in macPoolMap: 02:00:00:00:00:01",
				&findByMacAddressParams{
					macAddress:  "02:00:00:00:00:01",
					shouldExist: true,
					expectedEntry: macEntry{
						instanceName:         "vm/ns0/vm1",
						macInstanceKey:       "staleInterface",
						transactionTimestamp: &staleTimestamp,
					},
				}),
			Entry("Should find mac in macPoolMap: 02:00:00:00:00:02",
				&findByMacAddressParams{
					macAddress:  "02:00:00:00:00:02",
					shouldExist: true,
					expectedEntry: macEntry{
						instanceName:         "vm/ns2/vm2",
						macInstanceKey:       "validInterface",
						transactionTimestamp: &currentTimestamp,
					},
				}),
			Entry("Should find mac in macPoolMap: 02:00:00:00:00:05",
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

		It("Should remove instance's mac entry when running removeInstanceFromMac", func() {
			for macAddress, macEntries := range poolManager.macPoolMap {
				for _, entry := range macEntries {
					poolManager.macPoolMap.removeInstanceFromMac(macAddress.String(), entry.instanceName)
				}
				_, exist := poolManager.macPoolMap[macAddress]
				Expect(exist).To(BeFalse(), "mac entry should be deleted after removing all instances")
			}
			Expect(poolManager.macPoolMap).To(BeEmpty(), "macPoolMap should be empty after removing all its entries")
		})

		It("Should remove only specific instance's entry when MAC is shared by multiple instances", func() {
			macAddr := "02:00:00:00:00:20"
			vm1Name := "vm/ns1/vm1"
			vm2Name := "vm/ns2/vm2"
			vm3Name := "vm/ns3/vm3"
			ifaceName := "eth0"

			// Setup: Three VMs sharing the same MAC
			poolManager.macPoolMap.createOrUpdateEntry(macAddr, vm1Name, ifaceName)
			poolManager.macPoolMap.createOrUpdateEntry(macAddr, vm2Name, ifaceName)
			poolManager.macPoolMap.createOrUpdateEntry(macAddr, vm3Name, ifaceName)

			entries := poolManager.macPoolMap[NewMacKey(macAddr)]
			Expect(entries).To(HaveLen(3), "should have 3 entries for shared MAC")

			// Remove VM2's entry
			poolManager.macPoolMap.removeInstanceFromMac(macAddr, vm2Name)

			// Verify VM2's entry was removed
			entries = poolManager.macPoolMap[NewMacKey(macAddr)]
			Expect(entries).To(HaveLen(2), "should have 2 entries after removing one")

			// Verify VM1 and VM3 still exist
			vmNames := []string{entries[0].instanceName, entries[1].instanceName}
			Expect(vmNames).To(ContainElements(vm1Name, vm3Name))
			Expect(vmNames).NotTo(ContainElement(vm2Name))

			// Remove VM1's entry
			poolManager.macPoolMap.removeInstanceFromMac(macAddr, vm1Name)

			entries = poolManager.macPoolMap[NewMacKey(macAddr)]
			Expect(entries).To(HaveLen(1), "should have 1 entry after removing second")
			Expect(entries[0].instanceName).To(Equal(vm3Name))

			// Remove last entry - MAC should be deleted entirely
			poolManager.macPoolMap.removeInstanceFromMac(macAddr, vm3Name)

			_, exist := poolManager.macPoolMap[NewMacKey(macAddr)]
			Expect(exist).To(BeFalse(), "MAC should be deleted when last entry is removed")
		})

		type createOrUpdateInMacPoolMapParams struct {
			vmName        string
			macAddress    string
			interfaceName string
		}
		DescribeTable("and adding a new mac to macPoolMap",
			func(c *createOrUpdateInMacPoolMapParams) {
				poolManager.macPoolMap.createOrUpdateEntry(c.macAddress, c.vmName, c.interfaceName)
				updatedMacEntries, exist := poolManager.macPoolMap[NewMacKey(c.macAddress)]
				Expect(exist).To(BeTrue(), "mac entry should exist after added/updated")

				// Find the entry for this VM+interface
				found := false
				for _, entry := range updatedMacEntries {
					if entry.instanceName == c.vmName && entry.macInstanceKey == c.interfaceName {
						expectedMacEntry := macEntry{
							instanceName:         c.vmName,
							macInstanceKey:       c.interfaceName,
							transactionTimestamp: nil,
						}
						Expect(entry).To(Equal(expectedMacEntry), "macEntry should be added/updated")
						found = true
						break
					}
				}
				Expect(found).To(BeTrue(), "entry for VM+interface should exist")
			},
			Entry("Should succeed Adding a mac if mac is not in macPoolMap",
				&createOrUpdateInMacPoolMapParams{
					vmName:        "vm/default/vm0",
					interfaceName: "iface6",
					macAddress:    "02:00:00:00:00:06",
				}),
			Entry("Should succeed updating a mac if mac is already in macPoolMap",
				&createOrUpdateInMacPoolMapParams{
					vmName:        "vm/default/vm0",
					interfaceName: "validInterface",
					macAddress:    "02:00:00:00:00:00",
				}),
		)

		It("should not create duplicate entries when called twice with same VM+interface", func() {
			macAddr := "02:00:00:00:00:10"
			vmName := "vm/test/vm1"
			ifaceName := "eth0"

			// Call twice
			poolManager.macPoolMap.createOrUpdateEntry(macAddr, vmName, ifaceName)
			poolManager.macPoolMap.createOrUpdateEntry(macAddr, vmName, ifaceName)

			entries := poolManager.macPoolMap[NewMacKey(macAddr)]
			Expect(entries).To(HaveLen(1), "should only have one entry, not duplicates")
			Expect(entries[0].instanceName).To(Equal(vmName))
			Expect(entries[0].macInstanceKey).To(Equal(ifaceName))
		})

		It("should allow multiple VMs to share same MAC with different entries", func() {
			macAddr := "02:00:00:00:00:11"
			vm1Name := "vm/ns1/vm1"
			vm2Name := "vm/ns2/vm2"
			ifaceName := "eth0"

			poolManager.macPoolMap.createOrUpdateEntry(macAddr, vm1Name, ifaceName)
			poolManager.macPoolMap.createOrUpdateEntry(macAddr, vm2Name, ifaceName)

			entries := poolManager.macPoolMap[NewMacKey(macAddr)]
			Expect(entries).To(HaveLen(2), "should have two entries for two different VMs")

			vmNames := []string{entries[0].instanceName, entries[1].instanceName}
			Expect(vmNames).To(ContainElements(vm1Name, vm2Name))
		})

		type filterMacsThatRequireCommitParams struct {
			latestPersistedTimestamp *time.Time
			expectedMacMap           macMap
		}
		DescribeTable("and performing filterMacsThatRequireCommit on macPoolMap",
			func(f *filterMacsThatRequireCommitParams) {
				testMacMap := poolManager.macPoolMap
				testMacMap.filterMacsThatRequireCommit(f.latestPersistedTimestamp, log.WithName("fake-logger"))
				Expect(testMacMap).To(Equal(f.expectedMacMap), "should get expected mac list")
			},
			Entry("Should only get stale mac entries if latestPersistedTimestamp is before the current timestamp",
				&filterMacsThatRequireCommitParams{
					latestPersistedTimestamp: &timestampBeforeCurrentTimestamp,
					expectedMacMap: macMap{
						NewMacKey("02:00:00:00:00:01"): macEntry{
							instanceName:         "vm/ns0/vm1",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						NewMacKey("02:00:00:00:00:03"): macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
					},
				}),
			Entry("Should only get all pending mac entries if latestPersistedTimestamp equals the current timestamp",
				&filterMacsThatRequireCommitParams{
					latestPersistedTimestamp: &currentTimestamp,
					expectedMacMap: macMap{
						NewMacKey("02:00:00:00:00:00"): macEntry{
							instanceName:         "vm/default/vm0",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
						NewMacKey("02:00:00:00:00:01"): macEntry{
							instanceName:         "vm/ns0/vm1",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						NewMacKey("02:00:00:00:00:02"): macEntry{
							instanceName:         "vm/ns2/vm2",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
						NewMacKey("02:00:00:00:00:03"): macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						NewMacKey("02:00:00:00:00:04"): macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
					},
				}),
			Entry("Should only get all pending mac entries if latestPersistedTimestamp is after current timestamp",
				&filterMacsThatRequireCommitParams{
					latestPersistedTimestamp: &newTimestamp,
					expectedMacMap: macMap{
						NewMacKey("02:00:00:00:00:00"): macEntry{
							instanceName:         "vm/default/vm0",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
						NewMacKey("02:00:00:00:00:01"): macEntry{
							instanceName:         "vm/ns0/vm1",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						NewMacKey("02:00:00:00:00:02"): macEntry{
							instanceName:         "vm/ns2/vm2",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
						NewMacKey("02:00:00:00:00:03"): macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "staleInterface",
							transactionTimestamp: &staleTimestamp,
						},
						NewMacKey("02:00:00:00:00:04"): macEntry{
							instanceName:         "vm/ns3-4/vm3-4",
							macInstanceKey:       "validInterface",
							transactionTimestamp: &currentTimestamp,
						},
					},
				}),
		)
	})
})
