package pool_manager

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
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
			poolManager.macPoolMap = map[string]macEntry{
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

		type hasExpiredTransactionParams struct {
			macAddress    string
			shouldSucceed bool
		}
		table.DescribeTable("and performing hasExpiredTransaction on macPoolMap entry",
			func(i *hasExpiredTransactionParams) {
				entry := poolManager.macPoolMap[i.macAddress]
				isStale, err := entry.hasExpiredTransaction(poolManager.waitTime)
				Expect(err).ToNot(HaveOccurred(), "hasExpiredTransaction should not return error")
				Expect(isStale).To(Equal(i.shouldSucceed), fmt.Sprintf("mac entry %s staleness status is not as expected", i.macAddress))
			},
			table.Entry("Should find all stale entries in macPoolMap on entry mac: 02:00:00:00:00:00",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:00",
					shouldSucceed: false,
				}),
			table.Entry("Should find all stale entries in macPoolMap on entry mac: 02:00:00:00:00:01",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:01",
					shouldSucceed: true,
				}),
			table.Entry("Should find all stale entries in macPoolMap on entry mac: mac: 02:00:00:00:00:02",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:02",
					shouldSucceed: false,
				}),
			table.Entry("Should find all stale entries in macPoolMap on entry mac: mac: 02:00:00:00:00:03",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:03",
					shouldSucceed: true,
				}),
			table.Entry("Should find all stale entries in macPoolMap on entry mac: mac: 02:00:00:00:00:04",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:04",
					shouldSucceed: false,
				}),
			table.Entry("Should find all stale entries in macPoolMap on entry mac: mac: 02:00:00:00:00:05",
				&hasExpiredTransactionParams{
					macAddress:    "02:00:00:00:00:05",
					shouldSucceed: false,
				}),
		)

		type hasPendingTransactionParams struct {
			macAddress    string
			shouldSucceed bool
		}
		table.DescribeTable("and performing hasPendingTransaction on macPoolMap entry",
			func(i *hasPendingTransactionParams) {
				entry := poolManager.macPoolMap[i.macAddress]
				hasPendingTransaction := entry.hasPendingTransaction()
				Expect(hasPendingTransaction).To(Equal(i.shouldSucceed), fmt.Sprintf("mac entry %s update required status is not as expected", i.macAddress))
			},
			table.Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:00",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:00",
					shouldSucceed: true,
				}),
			table.Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:01",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:01",
					shouldSucceed: true,
				}),
			table.Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:02",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:02",
					shouldSucceed: true,
				}),
			table.Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:03",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:03",
					shouldSucceed: true,
				}),
			table.Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:04",
				&hasPendingTransactionParams{
					macAddress:    "02:00:00:00:00:04",
					shouldSucceed: true,
				}),
			table.Entry("Should distinguish between entries that needs update on mac entry: 02:00:00:00:00:05",
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
		table.DescribeTable("and performing hasReadyTransaction on macPoolMap",
			func(i *hasReadyTransactionParams) {
				entry := poolManager.macPoolMap[i.macAddress]
				isUpdated := entry.hasReadyTransaction(i.latestPersistedTimestamp)
				Expect(isUpdated).To(Equal(i.expectedEntryReadiness), fmt.Sprintf("should get expected readiness on mac %s", i.macAddress))
			},
			table.Entry("Should deem mac entry with old timestamp as ready if persistent timestamp occurred after it",
				&hasReadyTransactionParams{
					macAddress:               "02:00:00:00:00:01",
					latestPersistedTimestamp: &timestampBeforeCurrentTimestamp,
					expectedEntryReadiness:   true,
				}),
			table.Entry("Should deem mac entry with current timestamp as ready if persistent timestamp occurred after it",
				&hasReadyTransactionParams{
					macAddress:               "02:00:00:00:00:02",
					latestPersistedTimestamp: &newTimestamp,
					expectedEntryReadiness:   true,
				}),
			table.Entry("Should deem mac entry with current timestamp as ready if persistent timestamp equals to it",
				&hasReadyTransactionParams{
					macAddress:               "02:00:00:00:00:02",
					latestPersistedTimestamp: &currentTimestamp,
					expectedEntryReadiness:   true,
				}),
			table.Entry("Should deem mac entry with current timestamp as not ready if persistent timestamp occurred before it",
				&hasReadyTransactionParams{
					macAddress:               "02:00:00:00:00:02",
					latestPersistedTimestamp: &timestampBeforeCurrentTimestamp,
					expectedEntryReadiness:   false,
				}),
		)
	})
})
