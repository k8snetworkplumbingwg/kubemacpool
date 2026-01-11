package vmicollision

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type MockPoolManager struct {
	isVirtualMachineManagedCalls []string
	managedNamespaces            map[string]bool
}

func (m *MockPoolManager) IsVirtualMachineManaged(namespace string) (bool, error) {
	m.isVirtualMachineManagedCalls = append(m.isVirtualMachineManagedCalls, namespace)
	if m.managedNamespaces == nil {
		return true, nil
	}
	return m.managedNamespaces[namespace], nil
}

type MockEventRecorder struct {
	Events []MockEvent
}

type MockEvent struct {
	ObjectNamespace string
	ObjectName      string
	Type            string
	Reason          string
	Message         string
}

func (m *MockEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	vmi, ok := object.(*kubevirtv1.VirtualMachineInstance)
	if !ok {
		return
	}
	m.Events = append(m.Events, MockEvent{
		ObjectNamespace: vmi.Namespace,
		ObjectName:      vmi.Name,
		Type:            eventtype,
		Reason:          reason,
		Message:         message,
	})
}

func (m *MockEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	m.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

func (m *MockEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	m.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

type vmiOption func(*kubevirtv1.VirtualMachineInstance)

func withPhase(phase kubevirtv1.VirtualMachineInstancePhase) vmiOption {
	return func(vmi *kubevirtv1.VirtualMachineInstance) {
		vmi.Status.Phase = phase
	}
}

func withMACs(macs ...string) vmiOption {
	return func(vmi *kubevirtv1.VirtualMachineInstance) {
		for i, mac := range macs {
			vmi.Status.Interfaces = append(vmi.Status.Interfaces,
				kubevirtv1.VirtualMachineInstanceNetworkInterface{
					Name: "net" + string(rune('0'+i)),
					MAC:  mac,
				},
			)
		}
	}
}

func withMigrationUIDs(sourceMigrationUID, targetMigrationUID string) vmiOption {
	return func(vmi *kubevirtv1.VirtualMachineInstance) {
		vmi.Status.MigrationState = &kubevirtv1.VirtualMachineInstanceMigrationState{}
		if sourceMigrationUID != "" {
			vmi.Status.MigrationState.SourceState = &kubevirtv1.VirtualMachineInstanceMigrationSourceState{
				VirtualMachineInstanceCommonMigrationState: kubevirtv1.VirtualMachineInstanceCommonMigrationState{
					MigrationUID: types.UID(sourceMigrationUID),
				},
			}
		}
		if targetMigrationUID != "" {
			vmi.Status.MigrationState.TargetState = &kubevirtv1.VirtualMachineInstanceMigrationTargetState{
				VirtualMachineInstanceCommonMigrationState: kubevirtv1.VirtualMachineInstanceCommonMigrationState{
					MigrationUID: types.UID(targetMigrationUID),
				},
			}
		}
	}
}

func newVMI(namespace, name string, opts ...vmiOption) *kubevirtv1.VirtualMachineInstance {
	vmi := &kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(namespace + "-" + name + "-uid"),
		},
	}

	for _, opt := range opts {
		opt(vmi)
	}

	return vmi
}

func setupReconciler(mockPoolManager *MockPoolManager, objects ...client.Object) (*VMICollisionReconciler, *MockEventRecorder, client.Client) {
	scheme := runtime.NewScheme()
	_ = kubevirtv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithIndex(&kubevirtv1.VirtualMachineInstance{}, MacAddressIndexName, IndexVMIByMAC).
		Build()

	mockRecorder := &MockEventRecorder{Events: []MockEvent{}}

	reconciler := &VMICollisionReconciler{
		Client:      fakeClient,
		poolManager: mockPoolManager,
		recorder:    mockRecorder,
	}

	return reconciler, mockRecorder, fakeClient
}

var _ = Describe("VMI Collision Controller", func() {
	const (
		testNamespace = "test-ns"
		testVMIName   = "test-vmi"
		testMAC1      = "aa:bb:cc:dd:ee:01"
		testMAC2      = "aa:bb:cc:dd:ee:02"
	)

	var (
		mockPoolManager *MockPoolManager
		mockRecorder    *MockEventRecorder
		reconciler      *VMICollisionReconciler
		ctx             context.Context
	)

	BeforeEach(func() {
		mockPoolManager = &MockPoolManager{
			isVirtualMachineManagedCalls: []string{},
			managedNamespaces:            nil,
		}
		ctx = context.Background()
	})

	Describe("Reconcile", func() {
		Context("when VMI is not found", func() {
			It("should clean up collision tracking", func() {
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "non-existent-vmi",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when VMI is in non-managed namespace", func() {
			It("should skip collision detection without cleanup", func() {
				vmi := newVMI(testNamespace, testVMIName,
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				mockPoolManager.managedNamespaces = map[string]bool{testNamespace: false}
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      testVMIName,
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockPoolManager.isVirtualMachineManagedCalls).To(ContainElement(testNamespace))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when VMI is not running", func() {
			It("should clean up collision tracking for Pending phase", func() {
				vmi := newVMI(testNamespace, testVMIName,
					withPhase(kubevirtv1.Pending),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      testVMIName,
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})

			It("should clean up collision tracking for Succeeded phase", func() {
				vmi := newVMI(testNamespace, testVMIName,
					withPhase(kubevirtv1.Succeeded),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      testVMIName,
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when VMI has no MAC addresses", func() {
			It("should not report any collisions", func() {
				vmi := newVMI(testNamespace, testVMIName,
					withPhase(kubevirtv1.Running))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      testVMIName,
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when VMI is running with no collisions", func() {
			It("should report no collisions", func() {
				vmi := newVMI(testNamespace, testVMIName,
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      testVMIName,
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when VMI has MAC collision with another running VMI", func() {
			It("should report collision", func() {
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				// Verify events were emitted on both VMIs
				Expect(mockRecorder.Events).To(HaveLen(2))

				// Expected message (sorted VMI list for deduplication)
				expectedMessage := fmt.Sprintf("MAC %s: Collision between %s/%s, %s/%s",
					testMAC1, testNamespace, "vmi1", testNamespace, "vmi2")

				// Check event on vmi1
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))

				// Check event on vmi2
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))

				// Verify messages are identical (for Kubernetes deduplication)
				Expect(mockRecorder.Events[0].Message).To(Equal(mockRecorder.Events[1].Message))
			})

			It("should report collision across namespaces", func() {
				vmi1 := newVMI("ns1", "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmi2 := newVMI("ns2", "vmi2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "ns1",
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				// Verify events were emitted on both VMIs across namespaces
				Expect(mockRecorder.Events).To(HaveLen(2))

				// Expected message (sorted by namespace/name)
				expectedMessage := fmt.Sprintf("MAC %s: Collision between %s/%s, %s/%s",
					testMAC1, "ns1", "vmi1", "ns2", "vmi2")

				// Check event on vmi1
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: "ns1",
					ObjectName:      "vmi1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))

				// Check event on vmi2
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: "ns2",
					ObjectName:      "vmi2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})

			It("should report multiple collisions for same MAC", func() {
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmi3 := newVMI(testNamespace, "vmi3",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2, vmi3)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(3))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between %s/%s, %s/%s, %s/%s",
					testMAC1, testNamespace, "vmi1", testNamespace, "vmi2", testNamespace, "vmi3")

				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi3",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})

			It("should handle VMI with multiple MACs having different collision states", func() {
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1, testMAC2))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(2))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between %s/%s, %s/%s",
					testMAC1, testNamespace, "vmi1", testNamespace, "vmi2")

				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})
		})

		Context("when VMI with collision is not running", func() {
			It("should not report collision if other VMI is Pending", func() {
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Pending),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})

			It("should not report collision if other VMI is Succeeded", func() {
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Succeeded),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})
		})

		Context("when VMIs are part of same migration", func() {
			It("should report collision when VMIs have different migration state fields", func() {
				migrationUID1 := "migration-123"
				migrationUID2 := "migration-456"
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1),
					withMigrationUIDs(migrationUID1, ""))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1),
					withMigrationUIDs("", migrationUID2))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(2))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between %s/%s, %s/%s",
					testMAC1, testNamespace, "vmi1", testNamespace, "vmi2")

				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})

			It("should not report collision when both VMIs share source migration UID", func() {
				migrationUID := "migration-123"
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1),
					withMigrationUIDs(migrationUID, ""))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1),
					withMigrationUIDs(migrationUID, ""))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})

			It("should not report collision when both VMIs share target migration UID", func() {
				migrationUID := "migration-123"
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1),
					withMigrationUIDs("", migrationUID))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1),
					withMigrationUIDs("", migrationUID))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(mockRecorder.Events).To(BeEmpty())
			})

			It("should report collision when migration UIDs don't match", func() {
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1),
					withMigrationUIDs("migration-123", "migration-456"))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1),
					withMigrationUIDs("migration-678", "migration-91011"))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(2))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between %s/%s, %s/%s",
					testMAC1, testNamespace, "vmi1", testNamespace, "vmi2")

				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})

			It("should report collision when one VMI is migrating and other is not", func() {
				migrationUID := "migration-123"
				vmi1 := newVMI(testNamespace, "vmi1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1),
					withMigrationUIDs(migrationUID, ""))
				vmi2 := newVMI(testNamespace, "vmi2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmi1, vmi2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: testNamespace,
						Name:      "vmi1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(2))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between %s/%s, %s/%s",
					testMAC1, testNamespace, "vmi1", testNamespace, "vmi2")

				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: testNamespace,
					ObjectName:      "vmi2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})
		})

		Context("when filtering unmanaged namespaces", func() {
			It("should not report collision between managed and unmanaged namespace VMIs", func() {
				managedNamespace := "managed-ns"
				unmanagedNamespace := "unmanaged-ns"

				vmiManaged := newVMI(managedNamespace, "vmi-managed",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmiUnmanaged := newVMI(unmanagedNamespace, "vmi-unmanaged",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))

				mockPoolManager.managedNamespaces = map[string]bool{
					managedNamespace:   true,
					unmanagedNamespace: false,
				}
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmiManaged, vmiUnmanaged)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: managedNamespace,
						Name:      "vmi-managed",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockPoolManager.isVirtualMachineManagedCalls).To(ContainElement(managedNamespace))
				Expect(mockPoolManager.isVirtualMachineManagedCalls).To(ContainElement(unmanagedNamespace))
				Expect(mockRecorder.Events).To(BeEmpty())
			})

			It("should filter out multiple unmanaged VMIs but detect managed collisions", func() {
				managedNS := "managed-ns"
				unmanagedNS1 := "unmanaged-ns-1"
				unmanagedNS2 := "unmanaged-ns-2"

				vmiManaged1 := newVMI(managedNS, "vmi-managed-1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmiManaged2 := newVMI(managedNS, "vmi-managed-2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmiUnmanaged1 := newVMI(unmanagedNS1, "vmi-unmanaged-1",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))
				vmiUnmanaged2 := newVMI(unmanagedNS2, "vmi-unmanaged-2",
					withPhase(kubevirtv1.Running),
					withMACs(testMAC1))

				mockPoolManager.managedNamespaces = map[string]bool{
					managedNS:    true,
					unmanagedNS1: false,
					unmanagedNS2: false,
				}
				reconciler, mockRecorder, _ = setupReconciler(mockPoolManager, vmiManaged1, vmiManaged2, vmiUnmanaged1, vmiUnmanaged2)

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: managedNS,
						Name:      "vmi-managed-1",
					},
				}

				result, err := reconciler.Reconcile(ctx, req)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				Expect(mockRecorder.Events).To(HaveLen(2))
				expectedMessage := fmt.Sprintf("MAC %s: Collision between %s/%s, %s/%s",
					testMAC1, managedNS, "vmi-managed-1", managedNS, "vmi-managed-2")

				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: managedNS,
					ObjectName:      "vmi-managed-1",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
				Expect(mockRecorder.Events).To(ContainElement(MockEvent{
					ObjectNamespace: managedNS,
					ObjectName:      "vmi-managed-2",
					Type:            "Warning",
					Reason:          "MACCollision",
					Message:         expectedMessage,
				}))
			})
		})
	})
})
