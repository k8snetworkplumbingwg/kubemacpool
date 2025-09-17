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

package configmap

import (
	"context"
	"fmt"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

// MockEventRecorder captures events for testing
type MockEventRecorder struct {
	events []Event
}

type Event struct {
	Object    runtime.Object
	EventType string
	Reason    string
	Message   string
}

func (m *MockEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	m.events = append(m.events, Event{
		Object:    object,
		EventType: eventtype,
		Reason:    reason,
		Message:   message,
	})
}

func (m *MockEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	m.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

func (m *MockEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	m.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

const managerNamespace = "kubemacpool-system"

// MockPoolManager is a test double for PoolManager
type MockPoolManager struct {
	currentStart       net.HardwareAddr
	currentEnd         net.HardwareAddr
	updateRangesCalled bool
	updateRangesError  error
}

func (m *MockPoolManager) GetCurrentRanges() (start, end string) {
	return m.currentStart.String(), m.currentEnd.String()
}

func (m *MockPoolManager) UpdateRanges(start, end net.HardwareAddr) error {
	m.updateRangesCalled = true
	if m.updateRangesError != nil {
		return m.updateRangesError
	}
	m.currentStart = start
	m.currentEnd = end
	return nil
}

func (m *MockPoolManager) ManagerNamespace() string {
	return managerNamespace
}

var _ = Describe("ConfigMap Controller", func() {
	var reconciler *ConfigMapReconciler
	var mockPoolManager *MockPoolManager

	BeforeEach(func() {
		// Setup mock with default ranges
		startMac, _ := net.ParseMAC("02:00:00:00:00:00")
		endMac, _ := net.ParseMAC("02:ff:ff:ff:ff:ff")

		mockPoolManager = &MockPoolManager{
			currentStart:       startMac,
			currentEnd:         endMac,
			updateRangesCalled: false,
		}

		reconciler = &ConfigMapReconciler{
			PoolManager:   mockPoolManager,
			EventRecorder: nil,
		}
	})

	Describe("Reconcile", func() {
		Context("with valid ConfigMap data", func() {
			It("should return early when ranges are unchanged", func() {
				// Create ConfigMap with same ranges as mock PoolManager current ranges
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
					Data: map[string]string{
						"RANGE_START": "02:00:00:00:00:00", // Same as mock current
						"RANGE_END":   "02:ff:ff:ff:ff:ff", // Same as mock current
					},
				}

				// Create fake client with the ConfigMap
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(configMap).
					Build()
				reconciler.Client = fakeClient

				// Call Reconcile
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
				}
				result, err := reconciler.Reconcile(context.TODO(), req)

				// Verify early return
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(mockPoolManager.updateRangesCalled).To(BeFalse(), "UpdateRanges should not be called when ranges are unchanged")
			})

			It("should update PoolManager when ranges are changed", func() {
				// Create ConfigMap with different ranges
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
					Data: map[string]string{
						"RANGE_START": "02:11:11:11:11:11", // Different from mock current
						"RANGE_END":   "02:22:22:22:22:22", // Different from mock current
					},
				}

				// Create fake client with the ConfigMap
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(configMap).
					Build()
				reconciler.Client = fakeClient

				// Call Reconcile
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
				}
				result, err := reconciler.Reconcile(context.TODO(), req)

				// Verify UpdateRanges was called
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(mockPoolManager.updateRangesCalled).To(BeTrue(), "UpdateRanges should be called when ranges changed")

				// Verify the new ranges were set
				newStart, newEnd := mockPoolManager.GetCurrentRanges()
				Expect(newStart).To(Equal("02:11:11:11:11:11"))
				Expect(newEnd).To(Equal("02:22:22:22:22:22"))
			})
		})

		Context("with invalid ConfigMap data", func() {
			It("should return error when RANGE_START is missing", func() {
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
					Data: map[string]string{
						"RANGE_END": "02:ff:ff:ff:ff:ff",
					},
				}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(configMap).
					Build()
				reconciler.Client = fakeClient

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
				}
				result, err := reconciler.Reconcile(context.TODO(), req)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("RANGE_START not found"))
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(mockPoolManager.updateRangesCalled).To(BeFalse(), "UpdateRanges should not be called on error")
			})

			It("should return error when RANGE_END is missing", func() {
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
					Data: map[string]string{
						"RANGE_START": "02:00:00:00:00:00",
					},
				}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(configMap).
					Build()
				reconciler.Client = fakeClient

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
				}
				result, err := reconciler.Reconcile(context.TODO(), req)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("RANGE_END not found"))
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(mockPoolManager.updateRangesCalled).To(BeFalse(), "UpdateRanges should not be called on error")
			})

			It("should return error when RANGE_START is invalid MAC", func() {
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
					Data: map[string]string{
						"RANGE_START": "invalid-mac-address",
						"RANGE_END":   "02:ff:ff:ff:ff:ff",
					},
				}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(configMap).
					Build()
				reconciler.Client = fakeClient

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
				}
				result, err := reconciler.Reconcile(context.TODO(), req)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid RANGE_START"))
				Expect(err.Error()).To(ContainSubstring("invalid-mac-address"))
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(mockPoolManager.updateRangesCalled).To(BeFalse(), "UpdateRanges should not be called on error")
			})

			It("should return error when RANGE_END is invalid MAC", func() {
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
					Data: map[string]string{
						"RANGE_START": "02:00:00:00:00:00",
						"RANGE_END":   "invalid-mac-address",
					},
				}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(configMap).
					Build()
				reconciler.Client = fakeClient

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
				}
				result, err := reconciler.Reconcile(context.TODO(), req)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid RANGE_END"))
				Expect(err.Error()).To(ContainSubstring("invalid-mac-address"))
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(mockPoolManager.updateRangesCalled).To(BeFalse(), "UpdateRanges should not be called on error")
			})

			It("should return error when ConfigMap has empty data", func() {
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
					Data: map[string]string{},
				}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(configMap).
					Build()
				reconciler.Client = fakeClient

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
				}
				result, err := reconciler.Reconcile(context.TODO(), req)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("RANGE_START not found"))
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(mockPoolManager.updateRangesCalled).To(BeFalse(), "UpdateRanges should not be called on error")
			})
		})

		Context("when ConfigMap is not found", func() {
			It("should handle not found gracefully", func() {
				// Don't create any ConfigMap, so Get will return NotFound
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					Build()
				reconciler.Client = fakeClient
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
				}
				result, err := reconciler.Reconcile(context.TODO(), req)

				// Should handle gracefully without error
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
				Expect(mockPoolManager.updateRangesCalled).To(BeFalse(), "UpdateRanges should not be called when ConfigMap not found")
			})
		})
	})

	Describe("Event Recording", func() {
		var mockEventRecorder *MockEventRecorder
		BeforeEach(func() {
			mockEventRecorder = &MockEventRecorder{
				events: []Event{},
			}
			reconciler.EventRecorder = mockEventRecorder
		})

		type eventTestParams struct {
			configMapData           map[string]string
			mockError               error
			expectedReason          string
			expectedMessageContains []string
		}

		DescribeTable("should record error events",
			func(params eventTestParams) {
				if params.mockError != nil {
					mockPoolManager.updateRangesError = params.mockError
				}

				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
					Data: params.configMapData,
				}

				managerPod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubemacpool-manager-test",
						Namespace: managerNamespace,
						Labels: map[string]string{
							"app":           "kubemacpool",
							"control-plane": "mac-controller-manager",
						},
					},
					Status: corev1.PodStatus{Phase: corev1.PodRunning},
				}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(configMap, managerPod).
					Build()
				reconciler.Client = fakeClient

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      names.MAC_RANGE_CONFIGMAP,
						Namespace: managerNamespace,
					},
				}
				result, err := reconciler.Reconcile(context.TODO(), req)
				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))

				Expect(mockEventRecorder.events).To(HaveLen(1))
				event := mockEventRecorder.events[0]
				Expect(event.EventType).To(Equal(corev1.EventTypeWarning))
				Expect(event.Reason).To(Equal(params.expectedReason))
				for _, expectedMessage := range params.expectedMessageContains {
					Expect(event.Message).To(ContainSubstring(expectedMessage))
				}
			},
			Entry("when ConfigMap has invalid range format", eventTestParams{
				configMapData: map[string]string{
					"RANGE_START": "invalid-mac",
					"RANGE_END":   "02:22:22:22:22:22",
				},
				mockError:               nil,
				expectedReason:          "InvalidRange",
				expectedMessageContains: []string{"Invalid MAC range in ConfigMap"},
			}),
			Entry("when UpdateRanges fails due to pool manager validations", eventTestParams{
				configMapData: map[string]string{
					"RANGE_START": "03:33:33:33:33:33",
					"RANGE_END":   "03:44:44:44:44:44",
				},
				mockError:               fmt.Errorf("pool manager internal error"),
				expectedReason:          "UpdateFailed",
				expectedMessageContains: []string{"Failed to apply new MAC ranges", "pool manager internal error"},
			}),
		)
	})
})
