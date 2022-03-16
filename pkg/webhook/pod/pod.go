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
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	pool_manager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
	kawwebhook "github.com/qinqon/kube-admission-webhook/pkg/webhook"
)

var log = logf.Log.WithName("Webhook mutatepods")

type podAnnotator struct {
	client      client.Client
	decoder     *admission.Decoder
	poolManager *pool_manager.PoolManager
}

// Add adds server modifiers to the server, like registering the hook to the webhook server.
func Add(s *kawwebhook.Server, poolManager *pool_manager.PoolManager) error {
	podAnnotator := &podAnnotator{poolManager: poolManager}
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

	currentNetworkAnnotation := currentPod.GetAnnotations()[pool_manager.NetworksAnnotation]
	originalPodNetworkAnnotation := originalPod.GetAnnotations()[pool_manager.NetworksAnnotation]
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

// InjectClient injects the client into the podAnnotator
func (a *podAnnotator) InjectClient(c client.Client) error {
	a.client = c
	return nil
}

// InjectDecoder injects the decoder.
func (a *podAnnotator) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}
