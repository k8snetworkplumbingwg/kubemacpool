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

type ObjectType string

const (
	ObjectTypeVMI ObjectType = "vmi"
)

type ObjectReference struct {
	Type      ObjectType
	Namespace string
	Name      string
}

func (o ObjectReference) Key() string {
	return string(o.Type) + "/" + o.Namespace + "/" + o.Name
}

func (p *PoolManager) UpdateCollisionsMap(objectRef ObjectReference, collisions map[string][]ObjectReference) {
	p.poolMutex.Lock()
	defer p.poolMutex.Unlock()

	if p.collidingObjectsPerMAC == nil {
		p.collidingObjectsPerMAC = make(map[string][]ObjectReference)
	}

	objectKey := objectRef.Key()
	macsBefore := copyMACSet(p.collidingObjectsPerMAC)

	// to account for MACs removed from objects / objects removed
	removeObjectFromAllMACs(p.collidingObjectsPerMAC, objectKey)

	// to account for added colliding MACs
	for mac, collidingObjects := range collisions {
		p.collidingObjectsPerMAC[mac] = append([]ObjectReference{objectRef}, collidingObjects...)
	}

	updateCollisionGauge(p.collisionGauge, macsBefore, p.collidingObjectsPerMAC)
}

func removeObjectFromAllMACs(collidingObjectsPerMAC map[string][]ObjectReference, objectKey string) {
	for mac, objects := range collidingObjectsPerMAC {
		var newObjects []ObjectReference
		for _, obj := range objects {
			if obj.Key() != objectKey {
				newObjects = append(newObjects, obj)
			}
		}

		if len(newObjects) <= 1 {
			delete(collidingObjectsPerMAC, mac)
		} else {
			collidingObjectsPerMAC[mac] = newObjects
		}
	}
}

func copyMACSet(collidingObjectsPerMAC map[string][]ObjectReference) map[string]bool {
	macs := make(map[string]bool)
	for mac := range collidingObjectsPerMAC {
		macs[mac] = true
	}
	return macs
}

func updateCollisionGauge(gauge CollisionGauge, macsBefore map[string]bool, collidingObjectsPerMAC map[string][]ObjectReference) {
	for mac, objects := range collidingObjectsPerMAC {
		gauge.Set(mac, len(objects))
	}

	for mac := range macsBefore {
		if _, exists := collidingObjectsPerMAC[mac]; !exists {
			gauge.Delete(mac)
		}
	}
}
