package maccollision

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
)

var _ = Describe("Predicates", func() {
	const (
		testMAC1 = "aa:bb:cc:dd:ee:01"
		testMAC2 = "aa:bb:cc:dd:ee:02"
	)

	var predicate = collisionRelevantChanges()

	Describe("Create", func() {
		It("should allow create events", func() {
			vmi := &kubevirtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vmi",
					Namespace: "test-ns",
				},
			}

			e := event.TypedCreateEvent[*kubevirtv1.VirtualMachineInstance]{
				Object: vmi,
			}

			Expect(predicate.Create(e)).To(BeTrue())
		})
	})

	Describe("Delete", func() {
		It("should allow delete events", func() {
			vmi := &kubevirtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vmi",
					Namespace: "test-ns",
				},
			}

			e := event.TypedDeleteEvent[*kubevirtv1.VirtualMachineInstance]{
				Object: vmi,
			}

			Expect(predicate.Delete(e)).To(BeTrue())
		})
	})

	Describe("Update", func() {
		Context("when phase changes", func() {
			It("should trigger reconciliation", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Pending,
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Status.Phase = kubevirtv1.Running

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeTrue())
			})
		})

		Context("when phase doesn't change", func() {
			It("should not trigger reconciliation", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}

				newVMI := oldVMI.DeepCopy()

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeFalse())
			})
		})

		Context("when MAC addresses change", func() {
			It("should trigger reconciliation when MAC is added", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Status.Interfaces = []kubevirtv1.VirtualMachineInstanceNetworkInterface{
					{Name: "eth0", MAC: testMAC1},
				}

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeTrue())
			})

			It("should trigger reconciliation when MAC is removed", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
						Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
							{Name: "eth0", MAC: testMAC1},
						},
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Status.Interfaces = []kubevirtv1.VirtualMachineInstanceNetworkInterface{}

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeTrue())
			})

			It("should trigger reconciliation when MAC value changes", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
						Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
							{Name: "eth0", MAC: testMAC1},
						},
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Status.Interfaces[0].MAC = testMAC2

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeTrue())
			})

			It("should not trigger reconciliation when MAC order changes but set is same", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
						Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
							{Name: "eth0", MAC: testMAC1},
							{Name: "eth1", MAC: testMAC2},
						},
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Status.Interfaces = []kubevirtv1.VirtualMachineInstanceNetworkInterface{
					{Name: "eth0", MAC: testMAC2},
					{Name: "eth1", MAC: testMAC1},
				}

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeFalse())
			})
		})

		Context("when migration state changes", func() {
			It("should trigger reconciliation when source migration UID is added", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Status.MigrationState = &kubevirtv1.VirtualMachineInstanceMigrationState{
					SourceState: &kubevirtv1.VirtualMachineInstanceMigrationSourceState{
						VirtualMachineInstanceCommonMigrationState: kubevirtv1.VirtualMachineInstanceCommonMigrationState{
							MigrationUID: types.UID("migration-123"),
						},
					},
				}

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeTrue())
			})

			It("should trigger reconciliation when target migration UID is added", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Status.MigrationState = &kubevirtv1.VirtualMachineInstanceMigrationState{
					TargetState: &kubevirtv1.VirtualMachineInstanceMigrationTargetState{
						VirtualMachineInstanceCommonMigrationState: kubevirtv1.VirtualMachineInstanceCommonMigrationState{
							MigrationUID: types.UID("migration-123"),
						},
					},
				}

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeTrue())
			})

			It("should trigger reconciliation when migration UID changes", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
						MigrationState: &kubevirtv1.VirtualMachineInstanceMigrationState{
							SourceState: &kubevirtv1.VirtualMachineInstanceMigrationSourceState{
								VirtualMachineInstanceCommonMigrationState: kubevirtv1.VirtualMachineInstanceCommonMigrationState{
									MigrationUID: types.UID("migration-123"),
								},
							},
						},
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Status.MigrationState.SourceState.MigrationUID = types.UID("migration-456")

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeTrue())
			})

			It("should not trigger reconciliation when migration UIDs are unchanged", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
						MigrationState: &kubevirtv1.VirtualMachineInstanceMigrationState{
							SourceState: &kubevirtv1.VirtualMachineInstanceMigrationSourceState{
								VirtualMachineInstanceCommonMigrationState: kubevirtv1.VirtualMachineInstanceCommonMigrationState{
									MigrationUID: types.UID("migration-123"),
								},
							},
						},
					},
				}

				newVMI := oldVMI.DeepCopy()

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeFalse())
			})
		})

		Context("when other fields change", func() {
			It("should not trigger reconciliation for conditions change", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Status.Conditions = append(newVMI.Status.Conditions, kubevirtv1.VirtualMachineInstanceCondition{
					Type:   kubevirtv1.VirtualMachineInstanceReady,
					Status: "True",
				})

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeFalse())
			})

			It("should not trigger reconciliation for label changes", func() {
				oldVMI := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vmi",
						Namespace: "test-ns",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}

				newVMI := oldVMI.DeepCopy()
				newVMI.Labels = map[string]string{"new-label": "value"}

				e := event.TypedUpdateEvent[*kubevirtv1.VirtualMachineInstance]{
					ObjectOld: oldVMI,
					ObjectNew: newVMI,
				}

				Expect(predicate.Update(e)).To(BeFalse())
			})
		})
	})

	Describe("Generic", func() {
		It("should allow generic events", func() {
			vmi := &kubevirtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vmi",
					Namespace: "test-ns",
				},
			}

			e := event.TypedGenericEvent[*kubevirtv1.VirtualMachineInstance]{
				Object: vmi,
			}

			Expect(predicate.Generic(e)).To(BeTrue())
		})
	})
})

var _ = Describe("Pod Predicates", func() {
	var podPredicate = podCollisionRelevantChanges()

	basePod := func() *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "test-ns",
				Annotations: map[string]string{
					networkv1.NetworkAttachmentAnnot: `[{"name":"net1","mac":"02:00:00:00:00:01"}]`,
					networkv1.NetworkStatusAnnot:     "[]",
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		}
	}

	It("should allow create events for pods with network attachment annotation", func() {
		e := event.TypedCreateEvent[*corev1.Pod]{Object: basePod()}
		Expect(podPredicate.Create(e)).To(BeTrue())
	})

	It("should filter create events for pods without network attachment annotation", func() {
		pod := basePod()
		delete(pod.Annotations, networkv1.NetworkAttachmentAnnot)

		e := event.TypedCreateEvent[*corev1.Pod]{Object: pod}
		Expect(podPredicate.Create(e)).To(BeFalse())
	})

	It("should allow delete events", func() {
		e := event.TypedDeleteEvent[*corev1.Pod]{Object: basePod()}
		Expect(podPredicate.Delete(e)).To(BeTrue())
	})

	DescribeTable("Update",
		func(mutate func(old, new *corev1.Pod), expected bool) {
			oldPod := basePod()
			newPod := oldPod.DeepCopy()
			mutate(oldPod, newPod)

			e := event.TypedUpdateEvent[*corev1.Pod]{
				ObjectOld: oldPod,
				ObjectNew: newPod,
			}
			Expect(podPredicate.Update(e)).To(Equal(expected))
		},
		Entry("phase changes", func(_, newPod *corev1.Pod) {
			newPod.Status.Phase = corev1.PodSucceeded
		}, true),
		Entry("network-attachment annotation changes", func(_, newPod *corev1.Pod) {
			newPod.Annotations[networkv1.NetworkAttachmentAnnot] = `[{"name":"net2","mac":"02:00:00:00:00:02"}]`
		}, true),
		Entry("network-status annotation appears", func(oldPod, _ *corev1.Pod) {
			delete(oldPod.Annotations, networkv1.NetworkStatusAnnot)
		}, true),
		Entry("no relevant changes", func(_, newPod *corev1.Pod) {
			newPod.Labels = map[string]string{"new-label": "value"}
		}, false),
	)
})
