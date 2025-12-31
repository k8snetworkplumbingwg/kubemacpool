package pool_manager

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("mac-entry", func() {
	waitTimeSeconds := 10

	Context("check operations on mac-entry", func() {
		poolManager := &PoolManager{}
		// Freeze time
		now := time.Now()
		currentTimestamp := now
		timestampBeforeCurrentTimestamp := now.Add(-time.Second)
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

		type hasExpiredTransactionParams struct {
			macAddress    string
			shouldSucceed bool
		}
		DescribeTable("and performing hasExpiredTransaction on macPoolMap entry",
			func(i *hasExpiredTransactionParams) {
				entries := poolManager.macPoolMap[NewMacKey(i.macAddress)]
				Expect(entries).To(HaveLen(1))
				isStale, err := entries[0].hasExpiredTransaction(poolManager.waitTime)
				Expect(err).ToNot(HaveOccurred(), "hasExpiredTransaction should not return error")
				Expect(isStale).To(Equal(i.shouldSucceed), fmt.Sprintf("mac entry %s staleness status is not as expected", i.macAddress))
			},
			Entry("Should find all stale entries in macPoolMap on entry mac: 02:00:00:00:00:00",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:00",
					shouldSucceed: false,
				}),
			Entry("Should find all stale entries in macPoolMap on entry mac: 02:00:00:00:00:01",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:01",
					shouldSucceed: true,
				}),
			Entry("Should find all stale entries in macPoolMap on entry mac: mac: 02:00:00:00:00:02",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:02",
					shouldSucceed: false,
				}),
			Entry("Should find all stale entries in macPoolMap on entry mac: mac: 02:00:00:00:00:03",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:03",
					shouldSucceed: true,
				}),
			Entry("Should find all stale entries in macPoolMap on entry mac: mac: 02:00:00:00:00:04",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:04",
					shouldSucceed: false,
				}),
			Entry("Should find all stale entries in macPoolMap on entry mac: mac: 02:00:00:00:00:05",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:05",
					shouldSucceed: false,
				}),
		)

		type hasPendingTransactionParams struct {
			macAddress    string
			shouldSucceed bool
		}
		DescribeTable("and performing hasPendingTransaction on macPoolMap entry",
			func(i *hasPendingTransactionParams) {
				entries := poolManager.macPoolMap[NewMacKey(i.macAddress)]
				Expect(entries).To(HaveLen(1))
				hasPendingTransaction := entries[0].hasPendingTransaction()
				Expect(hasPendingTransaction).To(Equal(i.shouldSucceed), fmt.Sprintf("mac entry %s update required status is not as expected", i.macAddress))
			},
			Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:00",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:00",
					shouldSucceed: true,
				}),
			Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:01",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:01",
					shouldSucceed: true,
				}),
			Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:02",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:02",
					shouldSucceed: true,
				}),
			Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:03",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:03",
					shouldSucceed: true,
				}),
			Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:04",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:04",
					shouldSucceed: true,
				}),
			Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:05",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:05",
					shouldSucceed: false,
				}),
		)

		type hasReadyTransactionParams struct {
			macAddress               string
			latestPersistedTimestamp *time.Time
			expectedEntryReadiness   bool
		}
		DescribeTable("and performing hasReadyTransaction on macPoolMap",
			func(i *hasReadyTransactionParams) {
				entries := poolManager.macPoolMap[NewMacKey(i.macAddress)]
				Expect(entries).To(HaveLen(1))
				isUpdated := entries[0].hasReadyTransaction(i.latestPersistedTimestamp)
				Expect(isUpdated).To(Equal(i.expectedEntryReadiness), fmt.Sprintf("should get expected readiness on mac %s", i.macAddress))
			},
			Entry("Should deem mac entry with old timestamp as ready if persistent timestamp occurred after it",
				&hasReadyTransactionParams{
					macAddress:               "02:00:00:00:00:01",
					latestPersistedTimestamp: &timestampBeforeCurrentTimestamp,
					expectedEntryReadiness:   true,
				}),
			Entry("Should deem mac entry with current timestamp as ready if persistent timestamp occurred after it",
				&hasReadyTransactionParams{
					macAddress:               "02:00:00:00:00:02",
					latestPersistedTimestamp: &newTimestamp,
					expectedEntryReadiness:   true,
				}),
			Entry("Should deem mac entry with current timestamp as ready if persistent timestamp equals to it",
				&hasReadyTransactionParams{
					macAddress:               "02:00:00:00:00:02",
					latestPersistedTimestamp: &currentTimestamp,
					expectedEntryReadiness:   true,
				}),
			Entry("Should deem mac entry with current timestamp as not ready if persistent timestamp occurred before it",
				&hasReadyTransactionParams{
					macAddress:               "02:00:00:00:00:02",
					latestPersistedTimestamp: &timestampBeforeCurrentTimestamp,
					expectedEntryReadiness:   false,
				}),
		)
	})
})
