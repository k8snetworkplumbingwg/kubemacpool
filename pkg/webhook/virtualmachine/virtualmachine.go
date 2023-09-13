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

package virtualmachine

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kubevirt "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
)

var log = logf.Log.WithName("Webhook mutatevirtualmachines")

type virtualMachineAnnotator struct {
	client      client.Client
	decoder     *admission.Decoder
	poolManager *pool_manager.PoolManager
}

// Add adds server modifiers to the server, like registering the hook to the webhook server.
func Add(s *crwebhook.Server, poolManager *pool_manager.PoolManager) error {
	virtualMachineAnnotator := &virtualMachineAnnotator{poolManager: poolManager}
	s.Register("/mutate-virtualmachines", &webhook.Admission{Handler: virtualMachineAnnotator})
	return nil
}

// podAnnotator adds an annotation to every incoming pods.
func (a *virtualMachineAnnotator) Handle(ctx context.Context, req admission.Request) admission.Response {
	virtualMachine := &kubevirt.VirtualMachine{}

	err := a.decoder.Decode(req, virtualMachine)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	originalVirtualMachine := virtualMachine.DeepCopy()

	handleRequestId := rand.Intn(100000)
	logger := log.WithName("Handle").WithValues("RequestId", handleRequestId, "virtualMachineFullName", pool_manager.VmNamespaced(virtualMachine))

	if virtualMachine.Annotations == nil {
		virtualMachine.Annotations = map[string]string{}
	}
	if virtualMachine.Namespace == "" {
		virtualMachine.Namespace = req.AdmissionRequest.Namespace
	}

	logger.V(1).Info("got a virtual machine event")

	isNotDryRun := (req.DryRun == nil || *req.DryRun == false)
	if req.AdmissionRequest.Operation == admissionv1.Create {
		err = a.mutateCreateVirtualMachinesFn(virtualMachine, isNotDryRun, logger)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError,
				fmt.Errorf("Failed to create virtual machine allocation error: %v", err))
		}
	} else if req.AdmissionRequest.Operation == admissionv1.Update {
		err = a.mutateUpdateVirtualMachinesFn(virtualMachine, isNotDryRun, logger)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError,
				fmt.Errorf("Failed to update virtual machine allocation error: %v", err))
		}
	}

	// admission.PatchResponse generates a Response containing patches.
	return patchVMChanges(originalVirtualMachine, virtualMachine, logger)
}

// create jsonpatches only to changed caused by the kubemacpool webhook changes
func patchVMChanges(originalVirtualMachine, currentVirtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) admission.Response {
	logger := parentLogger.WithName("patchVMChanges")
	kubemapcoolJsonPatches := []jsonpatch.Operation{}

	if !pool_manager.IsVirtualMachineDeletionInProgress(currentVirtualMachine) {
		originalTransactionTSString := originalVirtualMachine.GetAnnotations()[pool_manager.TransactionTimestampAnnotation]
		currentTransactionTSString := currentVirtualMachine.GetAnnotations()[pool_manager.TransactionTimestampAnnotation]
		if originalTransactionTSString != currentTransactionTSString {
			transactionTimestampAnnotationPatch := jsonpatch.NewOperation("replace", "/metadata/annotations", currentVirtualMachine.GetAnnotations())
			kubemapcoolJsonPatches = append(kubemapcoolJsonPatches, transactionTimestampAnnotationPatch)
		}

		for ifaceIdx, _ := range currentVirtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces {
			interfacePatches, err := patchChange(fmt.Sprintf("/spec/template/spec/domain/devices/interfaces/%d/macAddress", ifaceIdx), originalVirtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces[ifaceIdx].MacAddress, currentVirtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces[ifaceIdx].MacAddress)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			kubemapcoolJsonPatches = append(kubemapcoolJsonPatches, interfacePatches...)
		}
	}

	logger.Info("patchVMChanges", "kubemapcoolJsonPatches", kubemapcoolJsonPatches)
	if len(kubemapcoolJsonPatches) == 0 {
		return admission.Response{
			Patches: kubemapcoolJsonPatches,
			AdmissionResponse: admissionv1.AdmissionResponse{
				Allowed: true,
			},
		}
	}
	return admission.Response{
		Patches: kubemapcoolJsonPatches,
		AdmissionResponse: admissionv1.AdmissionResponse{
			Allowed:   true,
			PatchType: func() *admissionv1.PatchType { pt := admissionv1.PatchTypeJSONPatch; return &pt }(),
		},
	}
}

func patchChange(pathChange string, original, current interface{}) ([]jsonpatch.Operation, error) {
	marshaledOriginal, _ := json.Marshal(original)
	marshaledCurrent, _ := json.Marshal(current)
	patches, err := jsonpatch.CreatePatch(marshaledOriginal, marshaledCurrent)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to patch change")
	}
	for idx, _ := range patches {
		patches[idx].Path = pathChange
	}

	return patches, nil
}

// mutateCreateVirtualMachinesFn calls the create allocation function
func (a *virtualMachineAnnotator) mutateCreateVirtualMachinesFn(virtualMachine *kubevirt.VirtualMachine, isNotDryRun bool, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("mutateCreateVirtualMachinesFn")
	logger.Info("got a create mutate virtual machine event")
	transactionTimestamp := pool_manager.CreateTransactionTimestamp()
	pool_manager.SetTransactionTimestampAnnotationToVm(virtualMachine, transactionTimestamp)

	existingVirtualMachine := &kubevirt.VirtualMachine{}
	err := a.client.Get(context.TODO(), client.ObjectKey{Namespace: virtualMachine.Namespace, Name: virtualMachine.Name}, existingVirtualMachine)
	if err != nil {
		// If the VM does not exist yet, allocate a new MAC address
		if apierrors.IsNotFound(err) {
			if !pool_manager.IsVirtualMachineDeletionInProgress(virtualMachine) {
				// If the object is not being deleted, then lets allocate macs and add the finalizer
				err = a.poolManager.AllocateVirtualMachineMac(virtualMachine, &transactionTimestamp, isNotDryRun, logger)
				if err != nil {
					return errors.Wrap(err, "Failed to allocate mac to the vm object")
				}

				return nil
			}
		}

		// Unexpected error
		return errors.Wrap(err, "Failed to get the existing vm object")
	}

	// If the object exist this mean the user run kubectl/oc create
	// This request will failed by the api server so we can just leave it without any allocation
	return nil
}

// mutateUpdateVirtualMachinesFn calls the update allocation function
func (a *virtualMachineAnnotator) mutateUpdateVirtualMachinesFn(virtualMachine *kubevirt.VirtualMachine, isNotDryRun bool, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("mutateUpdateVirtualMachinesFn")
	logger.Info("got an update mutate virtual machine event")
	previousVirtualMachine := &kubevirt.VirtualMachine{}
	err := a.client.Get(context.TODO(), client.ObjectKey{Namespace: virtualMachine.Namespace, Name: virtualMachine.Name}, previousVirtualMachine)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if isVirtualMachineChanged(previousVirtualMachine, virtualMachine) {
		transactionTimestamp := pool_manager.CreateTransactionTimestamp()
		pool_manager.SetTransactionTimestampAnnotationToVm(virtualMachine, transactionTimestamp)

		if isVirtualMachineInterfacesChanged(previousVirtualMachine, virtualMachine) {
			return a.poolManager.UpdateMacAddressesForVirtualMachine(previousVirtualMachine, virtualMachine, &transactionTimestamp, isNotDryRun, logger)
		}
	}

	return nil
}

func isVirtualMachineChanged(previousVirtualMachine, virtualMachine *kubevirt.VirtualMachine) bool {
	if isVirtualMachineSpecChanged(previousVirtualMachine, virtualMachine) {
		return true
	}
	if isVirtualMachineMetadataChanged(previousVirtualMachine, virtualMachine) {
		return true
	}
	return false
}

// isVirtualMachineChanged checks if the vm spec changed in this webhook update request.
func isVirtualMachineSpecChanged(previousVirtualMachine, virtualMachine *kubevirt.VirtualMachine) bool {
	return !reflect.DeepEqual(previousVirtualMachine.Spec, virtualMachine.Spec)
}

// isVirtualMachineMetadataChanged checks if non-automatically generated metadata fields changed in this webhook
// update request.
func isVirtualMachineMetadataChanged(previousVirtualMachine, virtualMachine *kubevirt.VirtualMachine) bool {
	if !reflect.DeepEqual(previousVirtualMachine.GetLabels(), virtualMachine.GetLabels()) {
		return true
	}
	currentAnnotations := getAnnotationsWithoutTransactionTimestamp(virtualMachine)
	previousAnnotations := getAnnotationsWithoutTransactionTimestamp(previousVirtualMachine)
	if !reflect.DeepEqual(previousAnnotations, currentAnnotations) {
		return true
	}
	return false
}

// getAnnotationsWithoutTransactionTimestamp get the vm's annotation, but excludes the changes made
// on this webhook such as TransactionTimestampAnnotation.
func getAnnotationsWithoutTransactionTimestamp(virtualMachine *kubevirt.VirtualMachine) map[string]string {
	annotations := virtualMachine.GetAnnotations()
	delete(annotations, pool_manager.TransactionTimestampAnnotation)
	return annotations
}

// isVirtualMachineInterfacesChanged checks if the vm interfaces changed in this webhook update request.
func isVirtualMachineInterfacesChanged(previousVirtualMachine, virtualMachine *kubevirt.VirtualMachine) bool {
	return !reflect.DeepEqual(previousVirtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces, virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces)
}

// InjectClient injects the client into the podAnnotator
func (a *virtualMachineAnnotator) InjectClient(c client.Client) error {
	a.client = c
	return nil
}

// InjectDecoder injects the decoder.
func (a *virtualMachineAnnotator) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}
