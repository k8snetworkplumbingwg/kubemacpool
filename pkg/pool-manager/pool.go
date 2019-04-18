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

package pool_manager

import (
	"fmt"
	"net"
	"sync"

	"k8s.io/client-go/kubernetes"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

const (
	RangeStartEnv            = "RANGE_START"
	RangeEndEvn              = "RANGE_END"
	networksAnnotation       = "k8s.v1.cni.cncf.io/networks"
	networksStatusAnnotation = "k8s.v1.cni.cncf.io/networks-status"
)

var log = logf.Log.WithName("PoolManager")

type PoolManager struct {
	kubeClient      kubernetes.Interface         // kubernetes client
	rangeStart      net.HardwareAddr             // fist mac in range
	rangeEnd        net.HardwareAddr             // last mac in range
	currentMac      net.HardwareAddr             // last given mac
	macPoolMap      map[string]AllocationStatus  // allocated mac map and status
	podToMacPoolMap map[string]map[string]string // map allocated mac address by networkname and namespace/podname: {"namespace/podname: {"network name": "mac address"}}
	vmToMacPoolMap  map[string][]string          // cap for namespace/vmname and a list of allocated mac addresses
	poolMutex       sync.Mutex                   // mutex for allocation an release
	isLeader        bool                         // leader boolean
	isKubevirt      bool                         // bool if kubevirt virtualmachine crd exist in the cluster
}

type AllocationStatus string

const (
	AllocationStatusAllocated     AllocationStatus = "Allocated"
	AllocationStatusWaitingForPod AllocationStatus = "WaitingForPod"
)

func NewPoolManager(kubeClient kubernetes.Interface, rangeStart, rangeEnd net.HardwareAddr, kubevirtExist bool) (*PoolManager, error) {
	err := checkRange(rangeStart, rangeEnd)
	if err != nil {
		return nil, err
	}

	currentMac := make(net.HardwareAddr, len(rangeStart))
	copy(currentMac, rangeStart)

	poolManger := &PoolManager{kubeClient: kubeClient,
		isLeader:        false,
		isKubevirt:      kubevirtExist,
		rangeEnd:        rangeEnd,
		rangeStart:      rangeStart,
		currentMac:      currentMac,
		podToMacPoolMap: map[string]map[string]string{},
		vmToMacPoolMap:  map[string][]string{},
		macPoolMap:      map[string]AllocationStatus{},
		poolMutex:       sync.Mutex{}}

	err = poolManger.InitMaps()
	if err != nil {
		return nil, err
	}

	return poolManger, nil
}

func (p *PoolManager) getFreeMac() (net.HardwareAddr, error) {
	// this look will ensure that we check all the range
	// first iteration from current mac to last mac in the range
	// second iteration from first mac in the range to the latest one
	for idx := 0; idx <= 1; idx++ {

		// This loop runs from the current mac to the last one in the range
		for {
			if _, ok := p.macPoolMap[p.currentMac.String()]; !ok {
				log.V(1).Info("found unused mac", "mac", p.currentMac)
				freeMac := make(net.HardwareAddr, len(p.currentMac))
				copy(freeMac, p.currentMac)

				// move to the next mac after we found a free one
				// If we allocate a mac address then release it and immediately allocate the same one to another object
				// we can have issues with dhcp and arp discovery
				if p.currentMac.String() == p.rangeEnd.String() {
					copy(p.currentMac, p.rangeStart)
				} else {
					p.currentMac = getNextMac(p.currentMac)
				}

				return freeMac, nil
			}

			if p.currentMac.String() == p.rangeEnd.String() {
				break
			}
			p.currentMac = getNextMac(p.currentMac)
		}

		copy(p.currentMac, p.rangeStart)
	}

	return nil, fmt.Errorf("the range is full")
}

func (p *PoolManager) InitMaps() error {
	err := p.initPodMap()
	if err != nil {
		return err
	}

	err = p.initVirtualMachineMap()
	if err != nil {
		return err
	}

	return nil
}

func (p *PoolManager) releaseAllocations(allocations []string) {
	for _, macAddr := range allocations {
		delete(p.macPoolMap, macAddr)
	}
}

func checkRange(startMac, endMac net.HardwareAddr) error {
	for idx := 0; idx <= 5; idx++ {
		if startMac[idx] < endMac[idx] {
			return nil
		}
	}

	return fmt.Errorf("Invalid range start: %s end: %s", startMac.String(), endMac.String())
}

func getNextMac(currentMac net.HardwareAddr) net.HardwareAddr {
	for idx := 5; idx >= 0; idx-- {
		currentMac[idx] += 1
		if currentMac[idx] != 0 {
			break
		}
	}

	return currentMac
}
