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
	helper "github.com/k8snetworkplumbingwg/kubemacpool/pkg/utils"
	"github.com/pkg/errors"
	webhookserver "github.com/qinqon/kube-admission-webhook/pkg/webhook/server"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kubevirt "kubevirt.io/client-go/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

var log = logf.Log.WithName("Webhook mutatevirtualmachines")

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
	virtualMachine := &kubevirt.VirtualMachine{}

	err := a.decoder.Decode(req, virtualMachine)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	handleRequestId := rand.Int()
	logger := log.WithName("Handle").WithValues("RequestId", handleRequestId, "virtualMachineName", virtualMachine.Name, "virtualMachineNamespace", virtualMachine.Namespace)

	if virtualMachine.Annotations == nil {
		virtualMachine.Annotations = map[string]string{}
	}
	if virtualMachine.Namespace == "" {
		virtualMachine.Namespace = req.AdmissionRequest.Namespace
	}

	logger.V(1).Info("got a virtual machine event")
	if req.AdmissionRequest.Operation == admissionv1beta1.Create {
		err = a.mutateCreateVirtualMachinesFn(ctx, virtualMachine, logger)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError,
				fmt.Errorf("Failed to create virtual machine allocation error: %v", err))
		}
	} else if req.AdmissionRequest.Operation == admissionv1beta1.Update {
		err = a.mutateUpdateVirtualMachinesFn(ctx, virtualMachine, logger)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError,
				fmt.Errorf("Failed to update virtual machine allocation error: %v", err))
		}
	}

	marshaledVirtualMachine, _ := json.Marshal(virtualMachine)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	// admission.PatchResponse generates a Response containing patches.
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledVirtualMachine)
}

// mutateCreateVirtualMachinesFn calls the create allocation function
func (a *virtualMachineAnnotator) mutateCreateVirtualMachinesFn(ctx context.Context, virtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("mutateCreateVirtualMachinesFn")
	logger.Info("got a create mutate virtual machine event")

	existingVirtualMachine := &kubevirt.VirtualMachine{}
	err := a.client.Get(context.TODO(), client.ObjectKey{Namespace: virtualMachine.Namespace, Name: virtualMachine.Name}, existingVirtualMachine)
	if err != nil {
		// If the VM does not exist yet, allocate a new MAC address
		if apierrors.IsNotFound(err) {
			if virtualMachine.ObjectMeta.DeletionTimestamp.IsZero() {
				logger.V(1).Info("The VM is not being deleted.")
				// If the object is not being deleted, then lets allocate macs and add the finalizer
				err = a.poolManager.AllocateVirtualMachineMac(virtualMachine, logger)
				if err != nil {
					return errors.Wrap(err, "Failed to allocate mac to the vm object")
				}

				return addFinalizer(virtualMachine, logger)
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
func (a *virtualMachineAnnotator) mutateUpdateVirtualMachinesFn(ctx context.Context, virtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
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
	if !reflect.DeepEqual(virtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces, previousVirtualMachine.Spec.Template.Spec.Domain.Devices.Interfaces) {
		return a.poolManager.UpdateMacAddressesForVirtualMachine(previousVirtualMachine, virtualMachine, logger)
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

func addFinalizer(virtualMachine *kubevirt.VirtualMachine, parentLogger logr.Logger) error {
	logger := parentLogger.WithName("addFinalizer")

	if helper.ContainsString(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName) {
		return nil
	}

	virtualMachine.ObjectMeta.Finalizers = append(virtualMachine.ObjectMeta.Finalizers, pool_manager.RuntimeObjectFinalizerName)
	logger.Info("Finalizer was added to the VM instance")

	return nil
}
