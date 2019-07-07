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
	"fmt"
	"net/http"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	kubevirt "kubevirt.io/kubevirt/pkg/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/builder"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/types"

	pool_manager "github.com/K8sNetworkPlumbingWG/kubemacpool/pkg/pool-manager"
)

var log = logf.Log.WithName("Webhook VirtualMachine")

func Add(mgr manager.Manager, poolManager *pool_manager.PoolManager, namespaceSelector *v1.LabelSelector) (*admission.Webhook, error) {
	if !poolManager.IsKubevirtEnabled() {
		return nil, nil
	}

	virtualMachineAnnotator := &virtualMachineAnnotator{poolManager: poolManager}

	wh, err := builder.NewWebhookBuilder().
		Mutating().
		FailurePolicy(admissionregistrationv1beta1.Fail).
		Operations(admissionregistrationv1beta1.Create, admissionregistrationv1beta1.Update).
		ForType(&kubevirt.VirtualMachine{}).
		Handlers(virtualMachineAnnotator).
		NamespaceSelector(namespaceSelector).
		WithManager(mgr).
		Build()
	if err != nil {
		return nil, err
	}

	return wh, nil
}

type virtualMachineAnnotator struct {
	client      client.Client
	decoder     types.Decoder
	poolManager *pool_manager.PoolManager
}

// podAnnotator Implements admission.Handler.
var _ admission.Handler = &virtualMachineAnnotator{}

// podAnnotator adds an annotation to every incoming pods.
func (a *virtualMachineAnnotator) Handle(ctx context.Context, req types.Request) types.Response {
	virtualMachine := &kubevirt.VirtualMachine{}

	err := a.decoder.Decode(req, virtualMachine)
	if err != nil {
		return admission.ErrorResponse(http.StatusBadRequest, err)
	}
	copyObject := virtualMachine.DeepCopy()
	if copyObject.Namespace == "" {
		copyObject.Namespace = req.AdmissionRequest.Namespace
	}

	if req.AdmissionRequest.Operation == admissionv1beta1.Create {
		err = a.mutateCreateVirtualMachinesFn(ctx, copyObject)
		if err != nil {
			return admission.ErrorResponse(http.StatusInternalServerError,
				fmt.Errorf("Failed to create virtual machine allocation error: %v", err))
		}
	} else if req.AdmissionRequest.Operation == admissionv1beta1.Update {
		err = a.mutateUpdateVirtualMachinesFn(ctx, copyObject)
		if err != nil {
			return admission.ErrorResponse(http.StatusInternalServerError,
				fmt.Errorf("Failed to update virtual machine allocation error: %v", err))
		}
	}

	// admission.PatchResponse generates a Response containing patches.
	return admission.PatchResponse(virtualMachine, copyObject)
}

// mutateCreateVirtualMachinesFn calls the create allocation function
func (a *virtualMachineAnnotator) mutateCreateVirtualMachinesFn(ctx context.Context, virtualMachine *kubevirt.VirtualMachine) error {
	log.Info("got a create mutate virtual machine event",
		"virtualMachineName", virtualMachine.Name,
		"virtualMachineNamespace", virtualMachine.Namespace)
	return a.poolManager.AllocateVirtualMachineMac(virtualMachine)
}

// mutateUpdateVirtualMachinesFn calls the update allocation function
func (a *virtualMachineAnnotator) mutateUpdateVirtualMachinesFn(ctx context.Context, virtualMachine *kubevirt.VirtualMachine) error {
	log.Info("got a update mutate virtual machine event",
		"virtualMachineName", virtualMachine.Name,
		"virtualMachineNamespace", virtualMachine.Namespace)
	return a.poolManager.UpdateMacAddressesForVirtualMachine(virtualMachine)
}

// podAnnotator implements inject.Client.
var _ inject.Client = &virtualMachineAnnotator{}

// InjectClient injects the client into the podAnnotator
func (a *virtualMachineAnnotator) InjectClient(c client.Client) error {
	a.client = c
	return nil
}

// podAnnotator implements inject.Decoder.
var _ inject.Decoder = &virtualMachineAnnotator{}

// InjectDecoder injects the decoder into the podAnnotator
func (a *virtualMachineAnnotator) InjectDecoder(d types.Decoder) error {
	a.decoder = d
	return nil
}
