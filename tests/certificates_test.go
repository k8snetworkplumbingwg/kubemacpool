package tests

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

var _ = Describe("kube-admission library", func() {
	Context("Check objects recovery", func() {
		var (
			oldSecret   v1.Secret
			oldCABundle []byte
		)
		BeforeEach(func() {
			By("getting the secret prior to any certification change ")
			err := GetCurrentSecret(&oldSecret)
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
			deleteServiceCaBundle()

			checkCertLibraryRecovery(oldCABundle, &oldSecret)
		})
	})
})

func deleteServiceSecret() {
	secret := v1.Secret{}
	err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: managerNamespace, Name: names.WEBHOOK_SERVICE}, &secret)
	Expect(err).ToNot(HaveOccurred(), "Should successfully get kubemacpool secret")

	err = testClient.VirtClient.Delete(context.TODO(), &secret)
	Expect(err).ToNot(HaveOccurred(), "Should successfully delete the new secret")
}

func deleteServiceCaBundle() {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mutatingWebhook := admissionregistrationv1.MutatingWebhookConfiguration{}
		err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: managerNamespace, Name: names.MUTATE_WEBHOOK_CONFIG}, &mutatingWebhook)
		Expect(err).ToNot(HaveOccurred(), "Should successfully get MutatingWebhookConfiguration")

		for i, _ := range mutatingWebhook.Webhooks {
			mutatingWebhook.Webhooks[i].ClientConfig.CABundle = make([]byte, 0)
		}

		err = testClient.VirtClient.Update(context.TODO(), &mutatingWebhook)
		return err
	})

	Expect(err).ToNot(HaveOccurred(), "Should successfully remove caBundle from MutatingWebhookConfiguration")
}

func checkCertLibraryRecovery(oldCABundle []byte, oldSecret *v1.Secret) {
	By("checking that the secret is regenerated")
	checkSecretRecovery(oldSecret)

	By("checking that the caBundle is regenerated")
	checkCaBundleRecovery(oldCABundle)
}

func GetCurrentSecret(secret *v1.Secret) error {
	return testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: managerNamespace, Name: names.WEBHOOK_SERVICE}, secret)
}

func GetCurrentCABundle() (caBundle []byte) {
	mutatingWebhook := admissionregistrationv1.MutatingWebhookConfiguration{}
	err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: managerNamespace, Name: names.MUTATE_WEBHOOK_CONFIG}, &mutatingWebhook)
	Expect(err).ToNot(HaveOccurred(), "Should successfully get MutatingWebhookConfiguration")

	//get the first one
	return mutatingWebhook.Webhooks[0].ClientConfig.CABundle
}

func checkSecretRecovery(oldSecret *v1.Secret) {
	Eventually(func() (map[string][]byte, error) {
		secret := v1.Secret{}
		By("Getting the new secret if exists")
		err := GetCurrentSecret(&secret)
		if err != nil {
			return nil, err
		}
		return secret.Data, nil

	}, timeout, pollingInterval).ShouldNot(Equal(oldSecret.Data), "should successfully renew secret")
}

func checkCaBundleRecovery(oldCABundle []byte) {
	Eventually(func() ([][]byte, error) {
		By("Getting the MutatingWebhookConfiguration")
		mutatingWebhook := admissionregistrationv1.MutatingWebhookConfiguration{}
		err := testClient.VirtClient.Get(context.TODO(), client.ObjectKey{Namespace: managerNamespace, Name: names.MUTATE_WEBHOOK_CONFIG}, &mutatingWebhook)
		if err != nil {
			return nil, err
		}
		caBundles := [][]byte{}
		for _, webhook := range mutatingWebhook.Webhooks {
			caBundles = append(caBundles, webhook.ClientConfig.CABundle)
		}
		return caBundles, nil
	}, timeout, pollingInterval).ShouldNot(ContainElement(oldCABundle), "should successfully renew all webhook's caBundles")
}
