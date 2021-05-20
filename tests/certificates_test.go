package tests

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/k8snetworkplumbingwg/kubemacpool/pkg/names"
)

var _ = Describe("kube-admission library", func() {
	Context("Check objects recovery", func() {
		var (
			oldSecret   *v1.Secret
			oldCABundle []byte
			err         error
		)
		BeforeEach(func() {
			By("getting the secret prior to any certification change ")
			oldSecret, err = GetCurrentSecret()
			Expect(err).ToNot(HaveOccurred(), "Should successfully get pre test secret")

			By("getting the caBundle prior to any certification change ")
			oldCABundle = GetCurrentCABundle()
		})

		It("should be able to recover from service secret deletion", func() {
			By("deleting the webhook service secret")
			deleteServiceSecret()

			checkCertLibraryRecovery(oldCABundle, oldSecret)
		})

		It("should be able to recover from mutatingWebhookConfiguration caBundle deletion", func() {

			By("deleting the mutatingWebhookConfiguration caBundle")
			deleteServiceCaBundle()

			checkCertLibraryRecovery(oldCABundle, oldSecret)
		})
	})
})

func deleteServiceSecret() {
	err := testClient.kubevirtClient.CoreV1().Secrets(managerNamespace).Delete(context.Background(), names.WEBHOOK_SERVICE, k8smetav1.DeleteOptions{})
	Expect(err).ToNot(HaveOccurred(), "Should successfully delete the new secret")
}

func deleteServiceCaBundle() {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mutatingWebhook, err := testClient.kubevirtClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.Background(), names.MUTATE_WEBHOOK_CONFIG, k8smetav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "Should successfully get MutatingWebhookConfiguration")

		for i, _ := range mutatingWebhook.Webhooks {
			mutatingWebhook.Webhooks[i].ClientConfig.CABundle = make([]byte, 0)
		}

		_, err = testClient.kubevirtClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(context.Background(), mutatingWebhook, k8smetav1.UpdateOptions{})
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

func GetCurrentSecret() (*v1.Secret, error) {
	return testClient.kubevirtClient.CoreV1().Secrets(managerNamespace).Get(context.Background(), names.WEBHOOK_SERVICE, k8smetav1.GetOptions{})
}

func GetCurrentCABundle() (caBundle []byte) {
	mutatingWebhook, err := testClient.kubevirtClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.Background(), names.MUTATE_WEBHOOK_CONFIG, k8smetav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Should successfully get MutatingWebhookConfiguration")

	//get the first one
	return mutatingWebhook.Webhooks[0].ClientConfig.CABundle
}

func checkSecretRecovery(oldSecret *v1.Secret) {
	Eventually(func() (map[string][]byte, error) {
		By("Getting the new secret if exists")
		secret, err := GetCurrentSecret()
		if err != nil {
			return nil, err
		}
		return secret.Data, nil

	}, timeout, pollingInterval).ShouldNot(Equal(oldSecret.Data), "should successfully renew secret")
}

func checkCaBundleRecovery(oldCABundle []byte) {
	Eventually(func() ([][]byte, error) {
		By("Getting the MutatingWebhookConfiguration")
		mutatingWebhook, err := testClient.kubevirtClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.Background(), names.MUTATE_WEBHOOK_CONFIG, k8smetav1.GetOptions{})
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
