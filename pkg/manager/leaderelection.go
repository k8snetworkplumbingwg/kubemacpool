package manager

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
	poolmanager "github.com/k8snetworkplumbingwg/kubemacpool/pkg/pool-manager"
)

func (k *KubeMacPoolManager) waitToStartLeading(poolManger *poolmanager.PoolManager) error {
	<-k.runtimeManager.Elected()
	// If we reach here then we are in the elected pod.
	logger := logf.Log.WithName("waitToStartLeading")

	logger.Info("pod won election")

	err := poolManger.Start()
	if err != nil {
		return errors.Wrap(err, "failed to start pool manager routines")
	}

	err = k.UpdateLeaderLabel()
	if err != nil {
		return errors.Wrap(err, "failed marking pod as leader")
	}

	err = k.setLeadershipConditions(corev1.ConditionTrue)
	if err != nil {
		return errors.Wrap(err, "failed changing leadership condition to true")
	}
	return nil
}

// Adds the leader label to elected pod and removes it from all the other pods, if exists
func (k *KubeMacPoolManager) UpdateLeaderLabel() error {
	logger := logf.Log.WithName("UpdateLeaderLabel")
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
				return errors.Wrap(err, "failed to get kubemacpool manager pod")
			}

			_, exist := pod.Labels[names.LEADER_LABEL]
			if pod.Name == k.podName && !exist {
				logger.V(1).Info("add the label to the elected leader", "Pod Name", pod.Name)
				pod.Labels[names.LEADER_LABEL] = "true"
			} else if exist {
				logger.V(1).Info("deleting leader label from old leader", "Pod Name", pod.Name)
				delete(pod.Labels, names.LEADER_LABEL)
			} else {
				return nil
			}

			return k.runtimeManager.GetClient().Status().Update(context.TODO(), &pod)
		})

		if err != nil {
			return errors.Wrap(err, fmt.Sprintf("failed to updating kubemacpool leader label in pod %s", pod.Name))
		}
	}

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
