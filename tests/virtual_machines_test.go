package tests

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"net"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kubevirtv1 "kubevirt.io/kubevirt/pkg/api/v1"
)

var _ = Describe("Virtual Machines", func() {

	ovsInterface := kubevirtv1.Interface{
		Name: "ovs",
		InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
			Bridge: &kubevirtv1.InterfaceBridge{},
		},
	}

	ovsNetwork := kubevirtv1.Network{
		Name: "ovs",
		NetworkSource: kubevirtv1.NetworkSource{
			Multus: &kubevirtv1.MultusNetwork{
				NetworkName: "ovs-net-vlan100",
			},
		},
	}

	BeforeAll(func() {
		result := testClient.KubeClient.ExtensionsV1beta1().RESTClient().
			Post().
			RequestURI(fmt.Sprintf(postUrl, TestNamespace, "ovs-net-vlan100")).
			Body([]byte(fmt.Sprintf(ovsConfCRD, "ovs-net-vlan100", TestNamespace))).
			Do()
		Expect(result.Error()).NotTo(HaveOccurred())
	})

	Context("Check the client", func() {
		AfterEach(func() {
			vmList := &kubevirtv1.VirtualMachineList{}
			err := testClient.VirtClient.List(context.TODO(),&client.ListOptions{},vmList)
			Expect(err).ToNot(HaveOccurred())

			for _, vmObject := range vmList.Items {
				err = testClient.VirtClient.Delete(context.TODO(),&vmObject)
				Expect(err).ToNot(HaveOccurred())
			}
		})

		It("should create a vm object and automatically assign a static mac address", func() {
			vm := CreateVmObject(TestNamespace,false,[]kubevirtv1.Interface{ovsInterface},[]kubevirtv1.Network{ovsNetwork})
			Expect(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(BeEmpty())

			err := testClient.VirtClient.Create(context.TODO(),vm)
			Expect(err).ToNot(HaveOccurred())

			Expect(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).ToNot(BeEmpty())
			_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should reject a vm creation with an already allocated mac address", func() {
			vm := CreateVmObject(TestNamespace,false,[]kubevirtv1.Interface{ovsInterface},[]kubevirtv1.Network{ovsNetwork})
			Expect(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).To(BeEmpty())

			err := testClient.VirtClient.Create(context.TODO(),vm)
			Expect(err).ToNot(HaveOccurred())

			Expect(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress).ToNot(BeEmpty())
			_, err = net.ParseMAC(vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress)
			Expect(err).ToNot(HaveOccurred())

			vmOverlap := CreateVmObject(TestNamespace,false,[]kubevirtv1.Interface{ovsInterface},[]kubevirtv1.Network{ovsNetwork})
			// Allocated the same mac address that was registered to the first vm
			vmOverlap.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress = vm.Spec.Template.Spec.Domain.Devices.Interfaces[0].MacAddress

			err = testClient.VirtClient.Create(context.TODO(),vm)
			Expect(err).To(HaveOccurred())
		})
	})
})
