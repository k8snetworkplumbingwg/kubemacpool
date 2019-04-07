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
	StartPoolRangeEnv  = "START_POOL_RANGE"
	EndPoolRangeEnv    = "END_POOL_RANGE"
	networksAnnotation = "k8s.v1.cni.cncf.io/networks"
)

var log = logf.Log.WithName("PoolManager")

type PoolManager struct {
	kubeClient      kubernetes.Interface // kubernetes client
	startRange      net.HardwareAddr     // fist mac in range
	endRange        net.HardwareAddr     // last mac in range
	currentMac      net.HardwareAddr     // last given mac
	macPoolMap      map[string]bool      // allocated mac map
	podToMacPoolMap map[string][]string  // map for namespace/podname and a list of allocated mac addresses
	vmToMacPoolMap  map[string][]string  // cap for namespace/vmname and a list of allocated mac addresses
	poolMutex       sync.Mutex           // mutex for allocation an release
	isLeader        bool                 // leader boolean
	isKubevirt      bool                 // bool if kubevirt virtualmachine crd exist in the cluster
}

func NewPoolManager(kubeClient kubernetes.Interface, startPoolRange, endPoolRange net.HardwareAddr, kubevirtExist bool) (*PoolManager, error) {
	err := checkRange(startPoolRange, endPoolRange)
	if err != nil {
		return nil, err
	}

	poolManger := &PoolManager{kubeClient: kubeClient,
		isLeader:        false,
		isKubevirt:      kubevirtExist,
		endRange:        endPoolRange,
		startRange:      startPoolRange,
		currentMac:      startPoolRange,
		podToMacPoolMap: map[string][]string{},
		vmToMacPoolMap:  map[string][]string{},
		macPoolMap:      map[string]bool{},
		poolMutex:       sync.Mutex{}}

	err = poolManger.InitMaps()
	if err != nil {
		return nil, err
	}

	return poolManger, nil
}

func (p *PoolManager) getFreeMac() (net.HardwareAddr, error) {
	currentMac := p.currentMac

	// this look will ensure that we check all the range
	// first iteration from current mac to last mac in the range
	// second iteration from first mac in the range to the latest one
	for idx := 0; idx <= 1; idx++ {

		// This loop runs from the current mac to the last one in the range
		for {
			currentMac := getNextMac(currentMac)
			if _, ok := p.macPoolMap[currentMac.String()]; !ok {
				log.V(1).Info("found unused mac", "mac", currentMac)
				p.currentMac = currentMac
				return currentMac, nil
			}

			if currentMac.String() == p.endRange.String() {
				break
			}
		}

		currentMac = p.startRange
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
