package pool_manager

import (
	"time"

	"github.com/go-logr/logr"
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

func (p *PoolManager) commitChangesToMacPoolMap(macsMapToCommit *macMap, vm *kubevirt.VirtualMachine, parentLogger logr.Logger) {
	vmPersistedInterfaceList := getVirtualMachineInterfaces(vm)
	parentLogger.Info("committing macs to macPoolMap according to the current vm interfaces", "macsMapToCommit", macsMapToCommit, "vmPersistedInterfaceList", vmPersistedInterfaceList)
	for macAddress, _ := range *macsMapToCommit {
		p.macPoolMap.alignMacEntryAccordingToVmInterface(macAddress, VmNamespaced(vm), vmPersistedInterfaceList)
	}
}
