package manager

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	manager_leaderelection "sigs.k8s.io/controller-runtime/pkg/leaderelection"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

func (k *KubeMacPoolManager) newLeaderElection(config *rest.Config, scheme *runtime.Scheme) error {
	recorderProvider, err := NewProvider(config, scheme, log.WithName("events"))
	if err != nil {
		return err
	}

	// Create the resource lock to enable leader election)
	resourceLock, err := manager_leaderelection.NewResourceLock(config, recorderProvider, manager_leaderelection.Options{
		LeaderElection:          true,
		LeaderElectionID:        names.LEADER_ID,
		LeaderElectionNamespace: k.podNamespace,
	})
	if err != nil {
		return err
	}

	l, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock: resourceLock,
		// Values taken from: https://github.com/kubernetes/apiserver/blob/master/pkg/apis/config/v1alpha1/defaults.go
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ context.Context) {
				k.leaderElectionChannel <- nil
			},
			OnStoppedLeading: func() {
				k.restartChannel <- struct{}{}
			},
		},
	})
	if err != nil {
		return err
	}

	k.leaderElection = l

	// Start the leader elector process
	go k.leaderElection.Run(context.Background())

	return nil
}

func (k *KubeMacPoolManager) waitToStartLeading() error {
	if k.leaderElection.IsLeader() {
		log.Info("This manager is already the leader")
		k.leaderElectionChannel <- nil
	}

	err := <-k.leaderElectionChannel
	if err != nil {
		return err
	}

	return k.markPodAsLeader()
}

func (k *KubeMacPoolManager) markPodAsLeader() error {
	pod, err := k.clientset.CoreV1().Pods(k.podNamespace).Get(context.TODO(), k.podName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	pod.Labels[names.LEADER_LABEL] = "true"
	_, err = k.clientset.CoreV1().Pods(k.podNamespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	log.Info("marked this manager as leader for webhook", "podName", k.podName)
	return nil
}
