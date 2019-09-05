package manager

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	manager_leaderelection "sigs.k8s.io/controller-runtime/pkg/leaderelection"
)

func (k *KubeMacPoolManager) newLeaderElection(config *rest.Config, scheme *runtime.Scheme) error {
	recorderProvider, err := NewProvider(config, scheme, log.WithName("events"))
	if err != nil {
		return err
	}

	// Create the resource lock to enable leader election)
	resourceLock, err := manager_leaderelection.NewResourceLock(config, recorderProvider, manager_leaderelection.Options{
		LeaderElection:          true,
		LeaderElectionID:        "kubemacpool-election",
		LeaderElectionNamespace: k.podNamespace,
	})
	if err != nil {
		return err
	}

	k.resourceLock = resourceLock

	return nil
}

func (k *KubeMacPoolManager) waitToStartLeading() error {
	messageChan := make(chan error, 1)

	l, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock: k.resourceLock,
		// Values taken from: https://github.com/kubernetes/apiserver/blob/master/pkg/apis/config/v1alpha1/defaults.go
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ context.Context) {
				messageChan <- nil
			},
			OnStoppedLeading: func() {
				// Most implementations of leader election log.Fatal() here.
				// Since Start is wrapped in log.Fatal when called, we can just return
				// an error here which will cause the program to exit.
				messageChan <- fmt.Errorf("leader election lost")
			},
		},
	})
	if err != nil {
		return err
	}

	// Start the leader elector process
	go l.Run(context.Background())

	return <-messageChan
}
