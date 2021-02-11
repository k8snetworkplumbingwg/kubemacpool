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
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	webhookserver "github.com/qinqon/kube-admission-webhook/pkg/webhook/server"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kubevirt "kubevirt.io/client-go/api/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager/transactions"
	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/utils"
)

var (
	log = logf.Log.WithName("Webhook mutatevirtualmachines")
	now = time.Now
)

type virtualMachineAnnotator struct {
	client      client.Client
	decoder     *admission.Decoder
	poolManager *pool_manager.PoolManager
}

// Add adds server modifiers to the server, like registering the hook to the webhook server.
func Add(s *webhookserver.Server, poolManager *pool_manager.PoolManager) error {
	virtualMachineAnnotator := &virtualMachineAnnotator{poolManager: poolManager}
	s.UpdateOpts(webhookserver.WithHook("/mutate-virtualmachines", &webhook.Admission{Handler: virtualMachineAnnotator}))
	return nil
}

// podAnnotator adds an annotation to every incoming pods.
func (a *virtualMachineAnnotator) Handle(ctx context.Context, req admission.Request) admission.Response {
	requestVm := &kubevirt.VirtualMachine{}

	err := a.decoder.Decode(req, requestVm)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	originalVmRequest := requestVm.DeepCopy()

	handleRequestId := rand.Intn(100000)
	logger := log.WithName("Handle").WithValues("RequestId", handleRequestId, "vmFullName", utils.VmNamespaced(requestVm))

	if requestVm.Annotations == nil {
		requestVm.Annotations = map[string]string{}
	}
	if requestVm.Namespace == "" {
		requestVm.Namespace = req.AdmissionRequest.Namespace
	}

	transaction := &transactions.VmTransaction{}
	if req.AdmissionRequest.Operation == admissionv1beta1.Create {
		transaction.Start(nil, requestVm, now)
		err = a.mutateCreateVirtualMachinesFn(transaction, logger)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError,
				fmt.Errorf("Failed to create virtual machine allocation error: %v", err))
		}
	} else if req.AdmissionRequest.Operation == admissionv1beta1.Update {
		currentVirtualMachine := &kubevirt.VirtualMachine{}
		err := a.decoder.DecodeRaw(req.OldObject, currentVirtualMachine)
		if err != nil && !apierrors.IsNotFound(err) {
			return admission.Errored(http.StatusInternalServerError,
				fmt.Errorf("Failed to get current virtual machine allocation error: %v", err))
		}

		transaction.Start(currentVirtualMachine, requestVm, now)
		err = a.mutateUpdateVirtualMachinesFn(transaction, logger)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError,
				fmt.Errorf("Failed to update virtual machine allocation error: %v", err))
		}
	}

	// admission.PatchResponse generates a Response containing patches.
	return patchVMChanges(originalVmRequest, transaction)
}

// create jsonpatches only to changed caused by the kubemacpool webhook changes
func patchVMChanges(originalVmRequest *kubevirt.VirtualMachine, transaction *transactions.VmTransaction) admission.Response {
	var kubemapcoolJsonPatches []jsonpatch.Operation

	if transaction.IsNeeded() {
		updatedVmRequest := transaction.GetUpdatedVm()
		if originalVmRequest.Annotations[transactions.TransactionTimestampAnnotation] != updatedVmRequest.Annotations[transactions.TransactionTimestampAnnotation] {
			transactionTimestampAnnotationPatch := jsonpatch.NewPatch("add", "/metadata/annotations", map[string]string{transactions.TransactionTimestampAnnotation: transaction.GetTransactionTimestamp()})
			kubemapcoolJsonPatches = append(kubemapcoolJsonPatches, transactionTimestampAnnotationPatch)
		}

		for ifaceIdx, _ := range transaction.GetUpdatedVm().Spec.Template.Spec.Domain.Devices.Interfaces {
			interfacePatches, err := patchChange(fmt.Sprintf("/spec/template/spec/domain/devices/interfaces/%d/macAddress", ifaceIdx), originalVmRequest.Spec.Template.Spec.Domain.Devices.Interfaces[ifaceIdx].MacAddress, updatedVmRequest.Spec.Template.Spec.Domain.Devices.Interfaces[ifaceIdx].MacAddress)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
			kubemapcoolJsonPatches = append(kubemapcoolJsonPatches, interfacePatches...)
		}

		finalizerPatches, err := patchChange("/metadata/finalizers", originalVmRequest.ObjectMeta.Finalizers, transaction.GetUpdatedVm().ObjectMeta.Finalizers)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}
		kubemapcoolJsonPatches = append(kubemapcoolJsonPatches, finalizerPatches...)
	}

	log.Info("patchVMChanges", "kubemapcoolJsonPatches", kubemapcoolJsonPatches)
	return admission.Response{
		Patches: kubemapcoolJsonPatches,
		AdmissionResponse: admissionv1beta1.AdmissionResponse{
			Allowed:   true,
			PatchType: func() *admissionv1beta1.PatchType { pt := admissionv1beta1.PatchTypeJSONPatch; return &pt }(),
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
func (a *virtualMachineAnnotator) mutateCreateVirtualMachinesFn(transaction *transactions.VmTransaction, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("createReq")
	logger.Info("got a create mutate virtual machine event")

	if transaction.IsNeeded() {
		err := transaction.ProcessCreateRequest(a.poolManager, parentLogger)
		if err != nil {
			return errors.Wrap(err, "Failed to process transaction from vm object")
		}
	} else {
		logger.V(1).Info("transaction processing not needed")
	}
	return nil
}

// mutateUpdateVirtualMachinesFn calls the update allocation function
func (a *virtualMachineAnnotator) mutateUpdateVirtualMachinesFn(transaction *transactions.VmTransaction, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("updateReq")
	logger.Info("got an update mutate virtual machine event")

	if transaction.IsNeeded() {
		err := transaction.ProcessUpdateRequest(a.poolManager, parentLogger)
		if err != nil {
			return errors.Wrap(err, "Failed to process transaction from vm object")
		}
	} else {
		logger.V(1).Info("transaction processing not needed")
	}

	return nil
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
