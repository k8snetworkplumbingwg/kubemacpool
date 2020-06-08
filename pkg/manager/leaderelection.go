package manager

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

func (k *KubeMacPoolManager) waitToStartLeading() error {
	<-k.mgr.Elected()
	return k.markPodAsLeader()
}

func (k *KubeMacPoolManager) markPodAsLeader() error {
	pod, err := k.clientset.CoreV1().Pods(k.podNamespace).Get(k.podName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	pod.Labels[names.LEADER_LABEL] = "true"
	_, err = k.clientset.CoreV1().Pods(k.podNamespace).Update(pod)
	if err != nil {
		return err
	}

	log.Info("marked this manager as leader for webhook", "podName", k.podName)
	return nil
}
