package pool_manager

import (
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	kubevirt "kubevirt.io/client-go/api/v1"
)

func CreateTransactionTimestamp() time.Time {
	return now()
}

func SetTransactionTimestampAnnotationToVm(virtualMachine *kubevirt.VirtualMachine, transactionTimestamp time.Time) {
	timeStampAnnotationValue := transactionTimestamp.Format(time.RFC3339Nano)
	virtualMachine.Annotations[TransactionTimestampAnnotation] = timeStampAnnotationValue
	log.Info("added TransactionTimestamp Annotation", "ts", timeStampAnnotationValue)
}

func GetTransactionTimestampAnnotationFromVm(virtualMachine *kubevirt.VirtualMachine) (time.Time, error) {
	timeStampAnnotationValue, exist := virtualMachine.GetAnnotations()[TransactionTimestampAnnotation]
	if !exist {
		return time.Time{}, apierrors.NewNotFound(schema.GroupResource{
			Group:    "kubemacpool.io",
			Resource: "transaction-timestamp",
		}, timeStampAnnotationValue)
	}
	return parseTransactionTimestamp(timeStampAnnotationValue)
}

func parseTransactionTimestamp(timeStampAnnotation string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, timeStampAnnotation)
}
