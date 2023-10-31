package pool_manager

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MacKey", func() {
	DescribeTable("String should return the normalized MAC address",
		func(input string, expected string) {
			macKey := NewMacKey(input)
			Expect(macKey.String()).To(Equal(expected))
		},
		Entry("colon-separated EUI-48 input", "00:00:5e:00:53:01", "00:00:5e:00:53:01"),
		Entry("colon-separated EUI-64 input", "02:00:5e:10:00:00:00:01", "02:00:5e:10:00:00:00:01"),
		Entry("colon-separated 20-octet IP input", "00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01", "00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01"),
		Entry("hyphen-separated EUI-48 input", "00-00-5e-00-53-01", "00:00:5e:00:53:01"),
		Entry("hyphen-separated EUI-64 input", "02-00-5e-10-00-00-00-01", "02:00:5e:10:00:00:00:01"),
		Entry("hyphen-separated 20-octet IP input", "00-00-00-00-fe-80-00-00-00-00-00-00-02-00-5e-10-00-00-00-01", "00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01"),
		Entry("period-separated EUI-48 input", "0000.5e00.5301", "00:00:5e:00:53:01"),
		Entry("period-separated EUI-64 input", "0200.5e10.0000.0001", "02:00:5e:10:00:00:00:01"),
		Entry("period-separated 20-octet IP input", "0000.0000.fe80.0000.0000.0000.0200.5e10.0000.0001", "00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01"),
	)
})
