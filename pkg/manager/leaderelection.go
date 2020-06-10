package manager

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	poolmanager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

func (k *KubeMacPoolManager) waitToStartLeading(poolManger *poolmanager.PoolManager) error {
	<-k.runtimeManager.Elected()
	// If we reach here then we are in the elected pod.

	err := poolManger.Start()
	if err != nil {
		return errors.Wrap(err, "failed to start pool manager routines")
	}

	err = k.AddLeaderLabelToElectedPod()
	if err != nil {
		return errors.Wrap(err, "failed marking pod as leader")
	}

	err = k.setLeadershipConditions(corev1.ConditionTrue)
	if err != nil {
		return errors.Wrap(err, "failed changing leadership condition to true")
	}
	return nil
}

func (k *KubeMacPoolManager) AddLeaderLabelToElectedPod() error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod, err := k.clientset.CoreV1().Pods(k.podNamespace).Get(context.TODO(), k.podName, metav1.GetOptions{})
		if err != nil {
			return errors.Wrap(err, "failed to get currently running kubemacpool manager pod")
		}

		pod.Labels[names.LEADER_LABEL] = "true"

		_, err = k.clientset.CoreV1().Pods(k.podNamespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		return errors.Wrap(err, "failed to update leader label to elected kubemacpool manager pod")
	}

	log.Info("marked this manager as leader for webhook", "podName", k.podName)
	return nil
}

// By setting this status to true in all pods, we declare the kubemacpool as ready and allow the webhooks to start running.
func (k *KubeMacPoolManager) setLeadershipConditions(status corev1.ConditionStatus) error {
	podList := corev1.PodList{}
	err := k.runtimeManager.GetClient().List(context.TODO(), &podList, &client.ListOptions{Namespace: k.podNamespace})
	if err != nil {
		return errors.Wrap(err, "failed to list kubemacpool manager pods")
	}
	for _, pod := range podList.Items {
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			podKey := types.NamespacedName{Namespace: k.podNamespace, Name: pod.Name}
			err := k.runtimeManager.GetClient().Get(context.TODO(), podKey, &pod)
			if err != nil {
				return errors.Wrap(err, "failed to get kubemacpool manager pods")
			}

			pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{Type: names.LEADER_READY_CONDITION_TYPE, Status: status, LastProbeTime: metav1.Time{}})

			err = k.runtimeManager.GetClient().Status().Update(context.TODO(), &pod)
			return err
		})
		if err != nil {
			return errors.Wrap(err, "failed to update Leadership readiness gate status to  kubemacpool manager pods")
		}
	}
	return nil
}
