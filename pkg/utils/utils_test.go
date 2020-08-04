package utils

import (
	"math"
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/onsi/ginkgo/extensions/table"
)

var _ = Describe("Utils", func() {

	Describe("Internal Functions", func() {
		table.DescribeTable("should convert from mac address to int64 correctly", func(macAddr string, expectedValue float64) {
			macAddrHW, err := net.ParseMAC(macAddr)
			Expect(err).ToNot(HaveOccurred(), "should succeed parsing the mac address")
			convertedMacAddrValue, err := ConvertHwAddrToInt64(macAddrHW)
			Expect(err).ToNot(HaveOccurred(), "should succeed converting the mac address to int64 value")
			Expect(float64(convertedMacAddrValue)).To(Equal(expectedValue), "should match expected value")
		},
			table.Entry("10:00:00:00:00:00 -> 2^44", "10:00:00:00:00:00", math.Pow(2, 11*4)),
			table.Entry("01:00:00:00:00:00 -> 2^40", "01:00:00:00:00:00", math.Pow(2, 10*4)),
			table.Entry("00:00:00:00:00:10 -> 2^4", "00:00:00:00:00:10", math.Pow(2, 1*4)),
			table.Entry("00:00:00:10:00:00 -> 2^20", "00:00:00:10:00:00", math.Pow(2, 5*4)),
			table.Entry("00:00:00:00:00:01 -> 2^0", "00:00:00:00:00:01", math.Pow(2, 0*4)),
			table.Entry("00:00:00:00:00:00 -> 0", "00:00:00:00:00:00", float64(0)),
			table.Entry("FF:FF:FF:FF:FF:FF -> 0", "FF:FF:FF:FF:FF:FF", math.Pow(2, 12*4)-1),
		)
	})
})
