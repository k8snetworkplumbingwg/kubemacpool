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

package pod

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/builder"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/types"

	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

var log = logf.Log.WithName("Webhook Pod")

func Add(mgr manager.Manager, poolManager *pool_manager.PoolManager, namespaceSelector *v1.LabelSelector) (*admission.Webhook, error) {
	podAnnotator := &podAnnotator{poolManager: poolManager}

	wh, err := builder.NewWebhookBuilder().
		Mutating().
		FailurePolicy(admissionregistrationv1beta1.Fail).
		Operations(admissionregistrationv1beta1.Create).
		ForType(&corev1.Pod{}).
		Handlers(podAnnotator).
		WithManager(mgr).
		NamespaceSelector(namespaceSelector).
		Build()
	if err != nil {
		return nil, err
	}

	return wh, nil
}

type podAnnotator struct {
	client      client.Client
	decoder     types.Decoder
	poolManager *pool_manager.PoolManager
}

// podAnnotator Implements admission.Handler.
var _ admission.Handler = &podAnnotator{}

// podAnnotator adds an annotation to every incoming pods.
func (a *podAnnotator) Handle(ctx context.Context, req types.Request) types.Response {
	pod := &corev1.Pod{}

	err := a.decoder.Decode(req, pod)
	if err != nil {
		return admission.ErrorResponse(http.StatusBadRequest, err)
	}

	if pod.Annotations == nil {
		admission.PatchResponse(pod, pod)
	}

	copyPod := pod.DeepCopy()
	if copyPod.Namespace == "" {
		copyPod.Namespace = req.AdmissionRequest.Namespace
	}

	log.V(1).Info("got a create pod event", "podName", copyPod.Name, "podNamespace", copyPod.Namespace)
	err = a.poolManager.AllocatePodMac(copyPod)
	if err != nil {
		return admission.ErrorResponse(http.StatusInternalServerError, err)
	}

	// admission.PatchResponse generates a Response containing patches.
	return admission.PatchResponse(pod, copyPod)
}

// podAnnotator implements inject.Client.
var _ inject.Client = &podAnnotator{}

// InjectClient injects the client into the podAnnotator
func (a *podAnnotator) InjectClient(c client.Client) error {
	a.client = c
	return nil
}

// podAnnotator implements inject.Decoder.
var _ inject.Decoder = &podAnnotator{}

// InjectDecoder injects the decoder into the podAnnotator
func (a *podAnnotator) InjectDecoder(d types.Decoder) error {
	a.decoder = d
	return nil
}
