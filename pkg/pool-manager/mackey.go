package pool_manager

import "net"

type macKey struct {
	normalizedMacAddress string
}

// NewMacKey creates a macKey struct containing a mac string
// that can be used for different mac string format comparison.
// It uses net.ParseMAC function to parse the given macAddress
// and then stores its String() value.
// The given macAddress MUST have already been validated.
func NewMacKey(macAddress string) macKey {
	// we can ignore the error here since the string value has already been validated
	hwMacAddress, _ := net.ParseMAC(macAddress)
	return macKey{normalizedMacAddress: hwMacAddress.String()}
}

func (m macKey) String() string {
	return m.normalizedMacAddress
}
