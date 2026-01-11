/*
Copyright 2025 The KubeMacPool Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vmicollision

import "net"

// NormalizeMacAddress parses and normalizes a MAC address string for comparison.
// Returns the normalized MAC address string and an error if parsing fails.
func NormalizeMacAddress(macAddress string) (string, error) {
	mac, err := net.ParseMAC(macAddress)
	if err != nil {
		return "", err
	}
	return mac.String(), nil
}
