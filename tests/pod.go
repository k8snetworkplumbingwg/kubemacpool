package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	multus "gopkg.in/k8snetworkplumbingwg/multus-cni.v3/pkg/types"
)

const (
	podCollisionCleanupTimeout  = 2 * time.Minute
	podCollisionCleanupInterval = 5 * time.Second
)

type PodOption func(*corev1.Pod)

func WithMultusNetwork(nadName, mac string) PodOption {
	return func(pod *corev1.Pod) {
		var networks []*multus.NetworkSelectionElement
		if existing, ok := pod.Annotations[networkv1.NetworkAttachmentAnnot]; ok {
			_ = json.Unmarshal([]byte(existing), &networks)
		}
		networks = append(networks, &multus.NetworkSelectionElement{
			Name:       nadName,
			Namespace:  pod.Namespace,
			MacRequest: mac,
		})
		data, _ := json.Marshal(networks)
		pod.Annotations[networkv1.NetworkAttachmentAnnot] = string(data)
	}
}

func NewCollisionPod(namespace, name string, opts ...PodOption) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        randName(name),
			Namespace:   namespace,
			Annotations: map[string]string{},
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &gracePeriodSeconds,
			Containers: []corev1.Container{
				{
					Name:    "test",
					Image:   "quay.io/centos/centos:stream9",
					Command: []string{"/bin/bash", "-c", "sleep INF"},
				},
			},
		},
	}
	for _, opt := range opts {
		opt(pod)
	}
	return pod
}

func getPodEvents(namespace, podName string) (*corev1.EventList, error) {
	return testClient.K8sClient.CoreV1().Events(namespace).List(context.TODO(), metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Pod", podName),
	})
}

func cleanupTestPodsInNamespaces(namespaces []string) {
	for _, namespace := range namespaces {
		podList, err := testClient.K8sClient.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())

		for i := range podList.Items {
			err = testClient.K8sClient.CoreV1().Pods(namespace).Delete(context.TODO(), podList.Items[i].Name, metav1.DeleteOptions{})
			if err != nil {
				continue
			}
		}

		Eventually(func() int {
			list, listErr := testClient.K8sClient.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
			Expect(listErr).ToNot(HaveOccurred())
			return len(list.Items)
		}).WithTimeout(podCollisionCleanupTimeout).WithPolling(podCollisionCleanupInterval).Should(Equal(0),
			"All test pods in namespace %s should be deleted", namespace)
	}
}
