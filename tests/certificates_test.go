package tests

import (
	"bytes"
	"context"
	"fmt"
	"k8s.io/api/admissionregistration/v1beta1"
	"k8s.io/client-go/util/retry"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	//apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

var _ = Describe("kube-admission library", func() {
	Context("Check objects recovery", func() {
		var (
			oldSecret   v1.Secret
			oldCABundle []byte
		)
		BeforeEach(func() {
			err := initKubemacpoolParams()
			Expect(err).ToNot(HaveOccurred(), "Should successfully initialize kubemacpool pods")

			By("getting the secret prior to any certification change ")
			err = GetCurrentSecret(&oldSecret)
			Expect(err).ToNot(HaveOccurred(), "Should successfully get pre test secret")

			By("getting the caBundle prior to any certification change ")
			oldCABundle = GetCurrentCABundle()
		})

		It("should be able to recover from service secret deletion", func() {
			By("deleting the webhook service secret")
			deleteServiceSecret()

			checkCertLibraryRecovery(oldCABundle, &oldSecret)
		})

		It("should be able to recover from mutatingWebhookConfiguration caBundle deletion", func() {

			By("deleting the mutatingWebhookConfiguration caBundle")
			err := deleteServiceCaBundle()
			Expect(err).ToNot(HaveOccurred(), "Should successfully remove the caBundle object")

			checkCertLibraryRecovery(oldCABundle, &oldSecret)
		})
	})
})

func deleteServiceSecret() {
	secret := v1.Secret{}
	err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: names.MANAGER_NAMESPACE, Name: names.WEBHOOK_SERVICE}, &secret)
	Expect(err).ToNot(HaveOccurred(), "Should successfully get kubemacpool secret")

	err = testClient.VirtClient.Delete(context.TODO(), &secret)
	Expect(err).ToNot(HaveOccurred(), "Should successfully delete the new secret")
}

func deleteServiceCaBundle() error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mutatingWebhook := v1beta1.MutatingWebhookConfiguration{}
		err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: names.MANAGER_NAMESPACE, Name: names.MUTATE_WEBHOOK_CONFIG}, &mutatingWebhook)
		Expect(err).ToNot(HaveOccurred(), "Should successfully get MutatingWebhookConfiguration")

		for _, webhook := range mutatingWebhook.Webhooks {
			webhook.ClientConfig.CABundle = make([]byte, 0)
		}

		err = testClient.VirtClient.Update(context.TODO(), &mutatingWebhook)
		return err
	})

	Expect(err).ToNot(HaveOccurred(), "Should successfully update caBundle in MutatingWebhookConfiguration")

	return err
}

func checkCertLibraryRecovery(oldCABundle []byte, oldSecret *v1.Secret) {
	By("checking that the secret is regenerated")
	checkSecretRecovery(oldSecret)

	By("checking that the caBundle is regenerated")
	checkCaBundleRecovery(oldCABundle)
}

func GetCurrentSecret(secret *v1.Secret) error {
	return testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: names.MANAGER_NAMESPACE, Name: names.WEBHOOK_SERVICE}, secret)
}

func GetCurrentCABundle() (caBundle []byte) {
	mutatingWebhook := v1beta1.MutatingWebhookConfiguration{}
	err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: names.MANAGER_NAMESPACE, Name: names.MUTATE_WEBHOOK_CONFIG}, &mutatingWebhook)
	Expect(err).ToNot(HaveOccurred(), "Should successfully get MutatingWebhookConfiguration")

	//get the first one
	return mutatingWebhook.Webhooks[0].ClientConfig.CABundle
}

func checkSecretRecovery(oldSecret *v1.Secret) {
	Eventually(func() bool {
		secret := v1.Secret{}
		By("Getting the new secret if exists")
		err := GetCurrentSecret(&secret)
		if err != nil {
			return false
		}

		By("comparing it to the old secret to make sure it was renewed")
		if secret.String() == oldSecret.String() {
			return false
		}

		return true
	}, timeout, pollingInterval).Should(BeTrue(), "should successfully renew secret")
}

func checkCaBundleRecovery(oldCABundle []byte) {
	Eventually(func() bool {
		By("Getting the MutatingWebhookConfiguration")
		mutatingWebhook := v1beta1.MutatingWebhookConfiguration{}
		err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: names.MANAGER_NAMESPACE, Name: names.MUTATE_WEBHOOK_CONFIG}, &mutatingWebhook)
		Expect(err).ToNot(HaveOccurred(), "Should successfully get MutatingWebhookConfiguration")

		for _, webhook := range mutatingWebhook.Webhooks {
			By(fmt.Sprintf("comparing %s webhook caBundle to the old caBundle to make sure it was renewed", webhook.Name))
			if bytes.Equal(webhook.ClientConfig.CABundle, oldCABundle) {
				return false
			}
		}
		return true
	}, timeout, pollingInterval).Should(BeTrue(), "should successfully renew all webhook's caBundles")
}
