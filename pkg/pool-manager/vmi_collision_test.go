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

package pool_manager

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type mockCollisionGauge struct {
	gauges map[string]int
}

func (m *mockCollisionGauge) Set(mac string, count int) {
	if m.gauges == nil {
		m.gauges = make(map[string]int)
	}
	m.gauges[mac] = count
}

func (m *mockCollisionGauge) Delete(mac string) {
	delete(m.gauges, mac)
}

func (m *mockCollisionGauge) GetCount(mac string) int {
	return m.gauges[mac]
}

func (m *mockCollisionGauge) TotalMACs() int {
	return len(m.gauges)
}

const (
	testMAC1 = "00:11:22:33:44:55"
	testMAC2 = "aa:bb:cc:dd:ee:ff"
)

func vmiRef(ns, name string) ObjectReference {
	return ObjectReference{Type: ObjectTypeVMI, Namespace: ns, Name: name}
}

var _ = Describe("UpdateCollisionsMap", func() {
	var poolManager *PoolManager
	var mockGauge *mockCollisionGauge

	BeforeEach(func() {
		mockGauge = &mockCollisionGauge{}
		poolManager = &PoolManager{
			collidingObjectsPerMAC: make(map[string][]ObjectReference),
			collisionGauge:         mockGauge,
		}
	})

	It("should add new collision and set gauge", func() {
		collisions := map[string][]ObjectReference{
			testMAC1: {vmiRef("ns1", "vmi2")},
		}

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi1"), collisions)

		expected := map[string][]ObjectReference{
			testMAC1: {
				vmiRef("ns1", "vmi1"),
				vmiRef("ns1", "vmi2"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected))
		Expect(mockGauge.TotalMACs()).To(Equal(1))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(2))
	})

	It("should update existing collision", func() {
		poolManager.collidingObjectsPerMAC[testMAC1] = []ObjectReference{
			vmiRef("ns1", "vmi1"),
			vmiRef("ns1", "vmi2"),
		}
		mockGauge.Set(testMAC1, 2)

		collisions := map[string][]ObjectReference{
			testMAC1: {vmiRef("ns1", "vmi2")},
		}

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi1"), collisions)

		expected := map[string][]ObjectReference{
			testMAC1: {
				vmiRef("ns1", "vmi1"),
				vmiRef("ns1", "vmi2"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected))
		Expect(mockGauge.TotalMACs()).To(Equal(1))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(2))
	})

	It("should remove object from tracking when collisions is nil", func() {
		poolManager.collidingObjectsPerMAC[testMAC1] = []ObjectReference{
			vmiRef("ns1", "vmi1"),
			vmiRef("ns1", "vmi2"),
		}
		mockGauge.Set(testMAC1, 2)

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi1"), nil)

		Expect(poolManager.collidingObjectsPerMAC).To(HaveLen(0))
		Expect(mockGauge.TotalMACs()).To(Equal(0))
	})

	It("should remove object from tracking when collisions is empty map", func() {
		poolManager.collidingObjectsPerMAC[testMAC1] = []ObjectReference{
			vmiRef("ns1", "vmi1"),
			vmiRef("ns1", "vmi2"),
		}
		mockGauge.Set(testMAC1, 2)

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi1"), map[string][]ObjectReference{})

		expected := map[string][]ObjectReference{}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected))
		Expect(mockGauge.TotalMACs()).To(Equal(0))
	})

	It("should handle object changing MACs", func() {
		poolManager.collidingObjectsPerMAC[testMAC1] = []ObjectReference{
			vmiRef("ns1", "vmi1"),
			vmiRef("ns1", "vmi2"),
		}
		mockGauge.Set(testMAC1, 2)

		collisions := map[string][]ObjectReference{
			testMAC2: {vmiRef("ns1", "vmi3")},
		}

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi1"), collisions)

		expected := map[string][]ObjectReference{
			testMAC2: {
				vmiRef("ns1", "vmi1"),
				vmiRef("ns1", "vmi3"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected))
		Expect(mockGauge.TotalMACs()).To(Equal(1))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(0))
		Expect(mockGauge.GetCount(testMAC2)).To(Equal(2))
	})

	It("should handle multiple MACs per object", func() {
		collisions := map[string][]ObjectReference{
			testMAC1: {vmiRef("ns1", "vmi2")},
			testMAC2: {vmiRef("ns1", "vmi3")},
		}

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi1"), collisions)

		expected := map[string][]ObjectReference{
			testMAC1: {
				vmiRef("ns1", "vmi1"),
				vmiRef("ns1", "vmi2"),
			},
			testMAC2: {
				vmiRef("ns1", "vmi1"),
				vmiRef("ns1", "vmi3"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected))
		Expect(mockGauge.TotalMACs()).To(Equal(2))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(2))
		Expect(mockGauge.GetCount(testMAC2)).To(Equal(2))
	})

	It("should keep collision when 3+ objects share same MAC", func() {
		poolManager.collidingObjectsPerMAC[testMAC1] = []ObjectReference{
			vmiRef("ns1", "vmi1"),
			vmiRef("ns1", "vmi2"),
			vmiRef("ns1", "vmi3"),
		}
		mockGauge.Set(testMAC1, 3)

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi1"), nil)

		expected := map[string][]ObjectReference{
			testMAC1: {
				vmiRef("ns1", "vmi2"),
				vmiRef("ns1", "vmi3"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected))
		Expect(mockGauge.TotalMACs()).To(Equal(1))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(2))
	})

	It("should handle adding collision when map already has other MACs", func() {
		poolManager.collidingObjectsPerMAC[testMAC1] = []ObjectReference{
			vmiRef("ns1", "vmi1"),
			vmiRef("ns1", "vmi2"),
		}
		mockGauge.Set(testMAC1, 2)

		collisions := map[string][]ObjectReference{
			testMAC2: {vmiRef("ns1", "vmi4")},
		}

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi3"), collisions)

		expected := map[string][]ObjectReference{
			testMAC1: {
				vmiRef("ns1", "vmi1"),
				vmiRef("ns1", "vmi2"),
			},
			testMAC2: {
				vmiRef("ns1", "vmi3"),
				vmiRef("ns1", "vmi4"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected))
		Expect(mockGauge.TotalMACs()).To(Equal(2))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(2))
		Expect(mockGauge.GetCount(testMAC2)).To(Equal(2))
	})

	It("should handle object moving from one collision to another", func() {
		// Initial state: VM1,VM2 collide on MAC1; VM3,VM4 collide on MAC2
		poolManager.collidingObjectsPerMAC[testMAC1] = []ObjectReference{
			vmiRef("ns1", "vmi1"),
			vmiRef("ns1", "vmi2"),
		}
		poolManager.collidingObjectsPerMAC[testMAC2] = []ObjectReference{
			vmiRef("ns1", "vmi3"),
			vmiRef("ns1", "vmi4"),
		}
		mockGauge.Set(testMAC1, 2)
		mockGauge.Set(testMAC2, 2)

		// VM3 changes to use MAC1 (now collides with VM1, VM2)
		collisions := map[string][]ObjectReference{
			testMAC1: {vmiRef("ns1", "vmi1"), vmiRef("ns1", "vmi2")},
		}
		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi3"), collisions)

		// MAC1 now has 3 objects, MAC2 is removed (only VM4 left = no collision)
		expected := map[string][]ObjectReference{
			testMAC1: {
				vmiRef("ns1", "vmi3"),
				vmiRef("ns1", "vmi1"),
				vmiRef("ns1", "vmi2"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected))
		Expect(mockGauge.TotalMACs()).To(Equal(1))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(3))
		Expect(mockGauge.GetCount(testMAC2)).To(Equal(0))
	})

	It("should maintain map consistency during multiple updates", func() {
		collisions1 := map[string][]ObjectReference{
			testMAC1: {vmiRef("ns1", "vmi2")},
		}
		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi1"), collisions1)
		expected1 := map[string][]ObjectReference{
			testMAC1: {
				vmiRef("ns1", "vmi1"),
				vmiRef("ns1", "vmi2"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected1))
		Expect(mockGauge.TotalMACs()).To(Equal(1))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(2))

		collisions2 := map[string][]ObjectReference{
			testMAC2: {vmiRef("ns1", "vmi4")},
		}
		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi3"), collisions2)
		expected2 := map[string][]ObjectReference{
			testMAC1: {
				vmiRef("ns1", "vmi1"),
				vmiRef("ns1", "vmi2"),
			},
			testMAC2: {
				vmiRef("ns1", "vmi3"),
				vmiRef("ns1", "vmi4"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected2))
		Expect(mockGauge.TotalMACs()).To(Equal(2))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(2))
		Expect(mockGauge.GetCount(testMAC2)).To(Equal(2))

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi1"), nil)
		expected3 := map[string][]ObjectReference{
			testMAC2: {
				vmiRef("ns1", "vmi3"),
				vmiRef("ns1", "vmi4"),
			},
		}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected3))
		Expect(mockGauge.TotalMACs()).To(Equal(1))
		Expect(mockGauge.GetCount(testMAC1)).To(Equal(0))
		Expect(mockGauge.GetCount(testMAC2)).To(Equal(2))

		poolManager.UpdateCollisionsMap(vmiRef("ns1", "vmi3"), nil)
		expected4 := map[string][]ObjectReference{}
		Expect(poolManager.collidingObjectsPerMAC).To(Equal(expected4))
		Expect(mockGauge.TotalMACs()).To(Equal(0))
	})
})

var _ = Describe("ObjectReference", func() {
	It("should generate correct key for VMI", func() {
		ref := ObjectReference{
			Type:      ObjectTypeVMI,
			Namespace: "test-ns",
			Name:      "test-vmi",
		}

		Expect(ref.Key()).To(Equal("vmi/test-ns/test-vmi"))
	})
})
