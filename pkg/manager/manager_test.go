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

package manager

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/onsi/ginkgo/extensions/table"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

var _ = Describe("leader election", func() {
	leaderPodName := "leaderPod"
	loosingPodName := "loosingPod"
	leaderLabelValue := "true"

	leaderPod := v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: leaderPodName, Namespace: names.MANAGER_NAMESPACE}}
	looserPod := v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: loosingPodName, Namespace: names.MANAGER_NAMESPACE}}

	createEnvironment := func(fakeObjectsForClient ...runtime.Object) client.Client {
		fakeClient := fake.NewFakeClient(fakeObjectsForClient...)

		return fakeClient
	}

	Describe("Internal Functions", func() {
		Context("When leader Pod is passed for leader label update", func() {
			table.DescribeTable("Should update the leader label in all pods", func(leaderPodFormerLabels, looserPodFormerLabels string) {
				By("Adding the initial label state of the pods prior to winning the election")
				initiatePodLabels(&leaderPod, leaderPodFormerLabels, leaderLabelValue)
				initiatePodLabels(&leaderPod, "app", "kubemacpool")
				initiatePodLabels(&looserPod, looserPodFormerLabels, leaderLabelValue)
				initiatePodLabels(&looserPod, "app", "kubemacpool")

				By("Initiating the Environment")
				kubeClient := createEnvironment(&leaderPod, &looserPod)

				By("running label update method")
				err := updateLeaderLabel(kubeClient, leaderPodName, names.MANAGER_NAMESPACE)
				Expect(err).ToNot(HaveOccurred(), "should successfully update kubemacpool leader labels")

				By("checking the leader pod has the leader label")
				checkLeaderPod := v1.Pod{}
				err = kubeClient.Get(context.TODO(), types.NamespacedName{Namespace: names.MANAGER_NAMESPACE, Name: leaderPodName}, &checkLeaderPod)
				Expect(err).ToNot(HaveOccurred(), "should successfully get the kubemacpool leader pod")
				Expect(checkLeaderPod.Labels).To(HaveKeyWithValue(names.LEADER_LABEL, leaderLabelValue), "leader pod should have the leader label value")

				By("checking the non-leader pod has no leader label")
				checkLooserPod := v1.Pod{}
				err = kubeClient.Get(context.TODO(), types.NamespacedName{Namespace: names.MANAGER_NAMESPACE, Name: loosingPodName}, &checkLooserPod)
				Expect(err).ToNot(HaveOccurred(), "should successfully get the kubemacpool non-leader pod")
				Expect(checkLooserPod.Labels).ToNot(HaveKeyWithValue(names.LEADER_LABEL, leaderLabelValue), "non leader pod should not have the leader label value")
			},
				table.Entry("all pods don't have a former leader label", "", ""),
				table.Entry("leader pod already has leader label from former election", names.LEADER_LABEL, ""),
				table.Entry("looser pod already has leader label from former election", "", names.LEADER_LABEL),
				table.Entry("all pods have a former leader label", names.LEADER_LABEL, names.LEADER_LABEL),
			)
		})
	})
})

func initiatePodLabels(pod *v1.Pod, label string, labelValue string) {
	if len(label) != 0 {
		if len(pod.Labels) == 0 {
			pod.Labels = make(map[string]string)
		}
		pod.Labels[label] = labelValue
	}
}
