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

package maccollision

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
)

type PodMockEventRecorder struct {
	Events []MockEvent
}

func (m *PodMockEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	pod, ok := object.(*corev1.Pod)
	if !ok {
		return
	}
	m.Events = append(m.Events, MockEvent{
		ObjectNamespace: pod.Namespace,
		ObjectName:      pod.Name,
		Type:            eventtype,
		Reason:          reason,
		Message:         message,
	})
}

func (m *PodMockEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	m.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

func (m *PodMockEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	m.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

type podOption func(*corev1.Pod)

func withPodPhase(phase corev1.PodPhase) podOption {
	return func(pod *corev1.Pod) {
		pod.Status.Phase = phase
	}
}

func withPodMACs(macs ...string) podOption {
	return func(pod *corev1.Pod) {
		var networks []*networkv1.NetworkSelectionElement
		for i, mac := range macs {
			networks = append(networks, &networkv1.NetworkSelectionElement{
				Name:       fmt.Sprintf("net%d", i),
				MacRequest: mac,
			})
		}
		netJSON, _ := json.Marshal(networks)
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[networkv1.NetworkAttachmentAnnot] = string(netJSON)
		pod.Annotations[networkv1.NetworkStatusAnnot] = "[]"
	}
}

func withVirtLauncherLabel() podOption {
	return func(pod *corev1.Pod) {
		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		pod.Labels[kubevirtv1.AppLabel] = "virt-launcher"
	}
}

func newTestPod(namespace, name string, opts ...podOption) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(namespace + "-" + name + "-uid"),
		},
	}
	for _, opt := range opts {
		opt(pod)
	}
	return pod
}

func setupPodReconciler(mockPoolManager *MockPoolManager, objects ...client.Object) (*PodReconciler, *PodMockEventRecorder) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kubevirtv1.AddToScheme(scheme)

	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithIndex(&corev1.Pod{}, PodMacAddressIndexName, IndexPodByMAC)

	if mockPoolManager.kubevirtEnabled {
		builder = builder.WithIndex(&kubevirtv1.VirtualMachineInstance{}, MacAddressIndexName, IndexVMIByMAC)
	}

	mockRecorder := &PodMockEventRecorder{Events: []MockEvent{}}

	reconciler := &PodReconciler{
		Client:      builder.Build(),
		poolManager: mockPoolManager,
		recorder:    mockRecorder,
	}

	return reconciler, mockRecorder
}

var _ = Describe("Pod Collision Controller", func() {
	const (
		testNamespace = "test-ns"
		testPodName   = "test-pod"
		testMAC1      = "aa:bb:cc:dd:ee:01"
		testMAC2      = "aa:bb:cc:dd:ee:02"
	)

	var (
		mockPoolManager *MockPoolManager
		mockRecorder    *PodMockEventRecorder
		reconciler      *PodReconciler
		ctx             context.Context
	)

	BeforeEach(func() {
		mockPoolManager = &MockPoolManager{
			isPodManagedCalls: []string{},
			managedNamespaces: nil,
			kubevirtEnabled:   false,
		}
		ctx = context.Background()
	})

	Describe("Reconcile", func() {
		Context("when Pod is not found", func() {
			It("should clean up collision tracking", func() {
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "non-existent"},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when Pod is a virt-launcher", func() {
			It("should skip without checking namespace", func() {
				pod := newTestPod(testNamespace, testPodName,
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1),
					withVirtLauncherLabel())
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testPodName},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockPoolManager.isPodManagedCalls).To(BeEmpty())
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when Pod is in non-managed namespace", func() {
			It("should skip collision detection", func() {
				pod := newTestPod(testNamespace, testPodName,
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				mockPoolManager.managedNamespaces = map[string]bool{testNamespace: false}
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testPodName},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when Pod is not running", func() {
			It("should clean up collision tracking", func() {
				pod := newTestPod(testNamespace, testPodName,
					withPodPhase(corev1.PodPending),
					withPodMACs(testMAC1))
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testPodName},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when Pod is running with no collisions", func() {
			It("should report no collisions", func() {
				pod := newTestPod(testNamespace, testPodName,
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testPodName},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when Pod has MAC collision with another running Pod", func() {
			It("should emit events on all colliding Pods", func() {
				pod1 := newTestPod(testNamespace, "pod1",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				pod2 := newTestPod(testNamespace, "pod2",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod1, pod2)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "pod1"},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(2))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between pod/%s/pod1, pod/%s/pod2",
					testMAC1, testNamespace, testNamespace)

				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "pod1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "pod2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))

				Expect(mockRecorder.Events[0].Message).To(Equal(mockRecorder.Events[1].Message))
			})
		})

		Context("when colliding Pod is not running", func() {
			It("should not report collision", func() {
				pod1 := newTestPod(testNamespace, "pod1",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				pod2 := newTestPod(testNamespace, "pod2",
					withPodPhase(corev1.PodPending),
					withPodMACs(testMAC1))
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod1, pod2)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "pod1"},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when colliding Pod is a virt-launcher", func() {
			It("should filter it out", func() {
				pod1 := newTestPod(testNamespace, "pod1",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				pod2 := newTestPod(testNamespace, "pod2",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1),
					withVirtLauncherLabel())
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod1, pod2)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "pod1"},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when colliding Pod is in unmanaged namespace", func() {
			It("should filter it out", func() {
				pod1 := newTestPod("managed-ns", "pod1",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				pod2 := newTestPod("unmanaged-ns", "pod2",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				mockPoolManager.managedNamespaces = map[string]bool{
					"managed-ns":   true,
					"unmanaged-ns": false,
				}
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod1, pod2)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: "managed-ns", Name: "pod1"},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when Pod has multiple MACs with different collision states", func() {
			It("should report collision only for the colliding MAC", func() {
				pod1 := newTestPod(testNamespace, "pod1",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1, testMAC2))
				pod2 := newTestPod(testNamespace, "pod2",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod1, pod2)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "pod1"},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(2))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between pod/%s/pod1, pod/%s/pod2",
					testMAC1, testNamespace, testNamespace)

				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "pod1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "pod2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})
		})

		Context("cross-type Pod-VMI collision detection", func() {
			BeforeEach(func() {
				mockPoolManager.kubevirtEnabled = true
			})

			It("should detect collision between Pod and running VMI", func() {
				pod := newTestPod(testNamespace, testPodName,
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				vmi := newVMI(testNamespace, "colliding-vmi",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod, vmi)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testPodName},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(1))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between pod/%s/%s, vmi/%s/colliding-vmi",
					testMAC1, testNamespace, testPodName, testNamespace)
				Expect(mockRecorder.Events[0]).To(Equal(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      testPodName,
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})

			It("should not detect collision with non-running VMI", func() {
				pod := newTestPod(testNamespace, testPodName,
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				vmi := newVMI(testNamespace, "pending-vmi",
					withPhase(kubevirtv1.Pending),
					withMACs(testMAC1))
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod, vmi)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testPodName},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})

			It("should detect collision with both Pods and VMIs for the same MAC", func() {
				pod1 := newTestPod(testNamespace, "pod1",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				pod2 := newTestPod(testNamespace, "pod2",
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				vmi := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod1, pod2, vmi)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "pod1"},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(2))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between pod/%s/pod1, pod/%s/pod2, vmi/%s/vmi1",
					testMAC1, testNamespace, testNamespace, testNamespace)

				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "pod1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "pod2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})

			It("should filter out VMI from unmanaged namespace", func() {
				pod := newTestPod("managed-ns", testPodName,
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				vmi := newVMI("unmanaged-ns", "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				mockPoolManager.managedNamespaces = map[string]bool{
					"managed-ns":   true,
					"unmanaged-ns": false,
				}
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod, vmi)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: "managed-ns", Name: testPodName},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})

			It("should not query VMI index when kubevirt is disabled", func() {
				mockPoolManager.kubevirtEnabled = false
				pod := newTestPod(testNamespace, testPodName,
					withPodPhase(corev1.PodRunning),
					withPodMACs(testMAC1))
				reconciler, mockRecorder = setupPodReconciler(mockPoolManager, pod)

				result, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testPodName},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})
	})
})
