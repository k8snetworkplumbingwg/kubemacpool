/*
Copyright 2019 The KubeMacPool Authors.

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

package utils

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func ContainsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func RemoveString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

func ConvertHwAddrToInt64(address net.HardwareAddr) (int64, error) {
	var addressTokenList []string
	for _, octet := range address {
		addressTokenList = append(addressTokenList, fmt.Sprintf("%02x", octet))
	}
	addressString := strings.Join(addressTokenList, "")
	addressValue, err := strconv.ParseInt(addressString, 16, 64)
	if err != nil {
		return 0, err
	}

	return addressValue, nil
}
