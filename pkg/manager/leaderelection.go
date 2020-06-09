package manager

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

func (k *KubeMacPoolManager) waitToStartLeading() error {
	<-k.mgr.Elected()

	err := k.markPodAsLeader()
	if err != nil {
		log.Error(err, "failed marking pod as leader")
		return err
	}

	err = k.setLeadershipConditions(corev1.ConditionTrue)
	if err != nil {
		log.Error(err, "failed changing leadership condition to true")
		return err
	}
	return nil
}

func (k *KubeMacPoolManager) markPodAsLeader() error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod, err := k.clientset.CoreV1().Pods(k.podNamespace).Get(context.TODO(), k.podName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		pod.Labels[names.LEADER_LABEL] = "true"

		_, err = k.clientset.CoreV1().Pods(k.podNamespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return err
	}

	log.Info("marked this manager as leader for webhook", "podName", k.podName)
	return nil
}

func (k *KubeMacPoolManager) setLeadershipConditions(status corev1.ConditionStatus) error {
	podList := corev1.PodList{}
	err := k.mgr.GetClient().List(context.TODO(), &podList, &client.ListOptions{Namespace: k.podNamespace})
	if err != nil {
		return err
	}
	for _, pod := range podList.Items {
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			podKey := types.NamespacedName{Namespace: k.podNamespace, Name: pod.Name}
			err := k.mgr.GetClient().Get(context.TODO(), podKey, &pod)
			if err != nil {
				return err
			}

			pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{Type: "kubemacpool.io/leadership", Status: status})

			err = k.mgr.GetClient().Status().Update(context.TODO(), &pod)
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
