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

	"gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
)

var log = logf.Log.WithName("Webhook mutatepods")

type podAnnotator struct {
	client      client.Client
	decoder     admission.Decoder
	poolManager *pool_manager.PoolManager
}

// Add adds server modifiers to the server, like registering the hook to the webhook server.
func Add(s crwebhook.Server, poolManager *pool_manager.PoolManager, scheme *runtime.Scheme, client client.Client) error {
	podAnnotator := &podAnnotator{poolManager: poolManager, decoder: admission.NewDecoder(scheme), client: client}
	s.Register("/mutate-pods", &webhook.Admission{Handler: podAnnotator})
	return nil
}

// podAnnotator adds an annotation to every incoming pods.
func (a *podAnnotator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}

	err := a.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	originalPod := pod.DeepCopy()

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	isNotDryRun := (req.DryRun == nil || *req.DryRun == false)
	transactionTimestamp := pool_manager.CreateTransactionTimestamp()
	log.V(1).Info("got a create pod event", "podName", pod.Name, "podNamespace", pod.Namespace, "transactionTimestamp", transactionTimestamp)

	err = a.poolManager.AllocatePodMac(pod, isNotDryRun)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	// admission.PatchResponse generates a Response containing patches.
	return patchPodChanges(originalPod, pod)
}

// create jsonpatches only to changed caused by the kubemacpool webhook changes
func patchPodChanges(originalPod, currentPod *corev1.Pod) admission.Response {
	kubemapcoolJsonPatches := []jsonpatch.Operation{}

	currentNetworkAnnotation := currentPod.GetAnnotations()[networkv1.NetworkAttachmentAnnot]
	originalPodNetworkAnnotation := originalPod.GetAnnotations()[networkv1.NetworkAttachmentAnnot]
	if originalPodNetworkAnnotation != currentNetworkAnnotation {
		annotationPatch := jsonpatch.NewOperation("replace", "/metadata/annotations", currentPod.GetAnnotations())
		kubemapcoolJsonPatches = append(kubemapcoolJsonPatches, annotationPatch)
	}

	log.Info("patchPodChanges", "kubemapcoolJsonPatches", kubemapcoolJsonPatches)
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
