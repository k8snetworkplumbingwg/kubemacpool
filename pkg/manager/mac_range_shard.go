package manager

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

const shardNameFormat = "RangeShard-%d"

var shardingLog = logf.Log.WithName("Sharing Mac Range")

type MacRangeShard struct {
	PodName           string
	PodIndex          int64
	start             string
	end               string
	shardingFactor    int64
	shardingFactorStr string
	ShardStart        net.HardwareAddr
	ShardEnd          net.HardwareAddr
	ShardName         string
}

type podNameIndexExtractor func(string) (string, error)

func NewMacRangeShard(podName, rangeStart, rangeEnd string, shardingFactor int) (MacRangeShard, error) {

	if podName == "" || rangeStart == "" || rangeEnd == "" || shardingFactor < 0 {
		return MacRangeShard{}, fmt.Errorf("one or more of the parameters is illigle")
	}

	podIndexInt64, err := parsePodNameIndexInt64(podName, extractStatefulSetPodNameIndex)
	if err != nil {
		shardingLog.Error(err, "failed to parse pod index name")
		return MacRangeShard{}, err
	}

	shardingFactorInt64 := int64(shardingFactor)
	rangeSharedStart, rangeSharedEnd, err := createMacRangeShard(rangeStart, rangeEnd, podIndexInt64, shardingFactorInt64)
	if err != nil {
		return MacRangeShard{}, err
	}

	macRangeShard := MacRangeShard{
		PodName:           podName,
		PodIndex:          podIndexInt64,
		ShardName:         fmt.Sprintf(shardNameFormat, podIndexInt64),
		ShardStart:        rangeSharedStart,
		ShardEnd:          rangeSharedEnd,
		start:             rangeStart,
		end:               rangeEnd,
		shardingFactorStr: string(shardingFactor),
		shardingFactor:    shardingFactorInt64,
	}

	return macRangeShard, nil
}

func createMacRangeShard(start, end string, podIndex, shardingFactor int64) (net.HardwareAddr, net.HardwareAddr, error) {
	rangeStartValue, err := convertMacAddressToInt64(start)
	if err != nil {
		shardingLog.Error(err, "failed to convert start range mac address to integer")
		return nil, nil, err
	}
	rangeEndValue, err := convertMacAddressToInt64(end)
	if err != nil {
		shardingLog.Error(err, "failed to convert end range mac address to integer")
		return nil, nil, err
	}

	rangesCount := shardingFactor
	shardRangeStartValue, shardRangeEndValue, err := calculateMacRangeShard(rangeStartValue, rangeEndValue, rangesCount, podIndex)
	if err != nil {
		shardingLog.Error(err, "failed to calculate shard range start and end addresses")
		return nil, nil, err
	}

	shardRangeStart, err := convertInt64ToHwAddr(shardRangeStartValue)
	if err != nil || shardRangeStart == nil {
		shardingLog.Error(err, "failed to convert start shard mac range address value to net.HwAddress")
		return nil, nil, err
	}

	shardRangeEnd, err := convertInt64ToHwAddr(shardRangeEndValue)
	if err != nil || shardRangeEnd == nil {
		shardingLog.Error(err, "failed to convert end shard mac range address value to net.HwAddress")
		return nil, nil, err
	}

	return shardRangeStart, shardRangeEnd, nil
}

func calculateMacRangeShard(rangeStart, rangeEnd, shardingFactor, podIndex int64) (int64, int64, error) {
	shardRangeStart := int64(0)
	shardRangeEnd := int64(0)

	if podIndex < 0 {
		return shardRangeStart, shardRangeStart, fmt.Errorf("pod index is illegal, got %d, expected > 0", podIndex)
	}
	if shardingFactor <= 0 {
		return shardRangeStart, shardRangeStart, fmt.Errorf("sharding factor is illegal, got %d, expected >= 0", shardingFactor)
	}

	rangeLength := abs(rangeStart-rangeEnd) + 1
	rangeOffset := rangeLength / shardingFactor

	if rangeLength < shardingFactor {
		return shardRangeStart, shardRangeStart, fmt.Errorf("the range length is bigger then the sharding factor, got shardingFactor %d, rangeLenght %d", shardingFactor, rangeLength)
	}
	reminder := rangeLength % shardingFactor
	shardRangeStart = rangeStart + (rangeOffset * podIndex)
	shardRangeEnd = rangeStart + (rangeOffset * podIndex) + (rangeOffset - 1)

	if reminder != 0 && podIndex == (shardingFactor-1) {
		shardRangeEnd += rangeLength - (rangeOffset * shardingFactor)
	}

	return shardRangeStart, shardRangeEnd, nil
}

func convertMacAddressToInt64(macAddress string) (int64, error) {
	macHwAddrr, err := net.ParseMAC(macAddress)
	if err != nil {
		return 0, err
	}
	macAddressValue, err := convertHwAddrToInt64(macHwAddrr)
	if err != nil {
		return 0, err
	}

	return macAddressValue, nil
}

func convertHwAddrToInt64(address net.HardwareAddr) (int64, error) {
	var addressTokenList []string
	for _, octet := range address {
		addressTokenList = append(addressTokenList, fmt.Sprintf("%02x", octet))
	}
	addressString := strings.Join(addressTokenList, "")
	addressValue, err := strconv.ParseInt(addressString, 16, 64)
	if err != nil {
		return 0, err
	}

	return addressValue, nil
}

func convertInt64ToHwAddr(addressValue int64) (net.HardwareAddr, error) {
	delimiter := ":"
	octetLength := 2
	length := 12
	rawString := fmt.Sprintf("%0*x", length, addressValue)
	formattedMacAddress := fmt.Sprintf("%s", rawString[0:octetLength])
	for idx := octetLength; idx < length; idx += octetLength {
		formattedMacAddress = fmt.Sprintf("%s%s%s", formattedMacAddress, delimiter, rawString[idx:idx+octetLength])
	}
	return net.ParseMAC(formattedMacAddress)
}

func extractStatefulSetPodNameIndex(podName string) (string, error) {
	if podName != "" {
		delimiter := "-"
		tokens := strings.Split(podName, delimiter)
		return tokens[cap(tokens)-1], nil
	}

	return podName, fmt.Errorf("failed to parse pod name index, Pod name is empty")
}

func parsePodNameIndexInt64(podName string, parseMethod podNameIndexExtractor) (int64, error) {
	podIndex := int64(-1)

	podIndexString, err := parseMethod(podName)
	if err != nil {
		shardingLog.Error(err, "Fail to parse pod name index as string")
		return podIndex, err
	}

	podIndex, err = strconv.ParseInt(podIndexString, 10, 64)
	if err != nil {
		shardingLog.Error(err, "Fail to convert pod name index to integer")
		return podIndex, err
	}

	return podIndex, nil
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
