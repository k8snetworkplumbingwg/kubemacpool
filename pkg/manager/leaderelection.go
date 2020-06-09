package manager

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	poolmanager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

func (k *KubeMacPoolManager) waitToStartLeading(poolManager *poolmanager.PoolManager) error {
	<-k.mgr.Elected()
	// If we reach here then we are in the elected pod.

	err := poolManager.Start()
	if err != nil {
		log.Error(err, "failed to start pool manager routines")
		return err
	}

	err = k.markPodAsLeader()
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
		return err
	})

	if err != nil {
		return err
	}

	log.Info("marked this manager as leader for webhook", "podName", k.podName)
	return nil
}

// By setting this status to true in all pods, we declare the kubemacpool as ready and allow the webhooks to start running.
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

			pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{Type: "kubemacpool.io/leader-ready", Status: status, LastProbeTime: metav1.Time{}})

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
