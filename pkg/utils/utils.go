package utils

import (
	"fmt"
	"os"
	"strings"
)

func FormatUptime(uptime int) string {
	// Convert seconds to format like 1d 2h 3m 4s
	days := uptime / 86400
	hours := (uptime - days*86400) / 3600
	minutes := (uptime - days*86400 - hours*3600) / 60
	seconds := uptime - days*86400 - hours*3600 - minutes*60
	uptimeString := fmt.Sprintf("%dd%dh%dm%ds", days, hours, minutes, seconds)
	return uptimeString
}

func SubstractSlices(slice1, slice2 []string) []string {
	elements := make(map[string]bool)
	for _, elem := range slice2 {
		elements[elem] = true
	}
	// Create a result slice to store the difference
	var difference []string
	// Iterate through slice1 and check if the element is present in slice2
	for _, elem := range slice1 {
		if !elements[elem] {
			difference = append(difference, elem)
		}
	}
	return difference
}

func ExistsIn(first, second []string) bool {
	mapFirst := make(map[string]bool)
	for _, value := range first {
		mapFirst[value] = true
	}
	for _, value := range second {
		if _, ok := mapFirst[value]; !ok {
			return false
		}
	}
	return true
}

func StringInSlice(str string, list []string) bool {
	for _, v := range list {
		if v == str {
			return true
		}
	}
	return false
}

func EnsurePodNamespaceEnv() string {
	if os.Getenv("POD_NAMESPACE") == "" {
		err := os.Setenv("POD_NAMESPACE", "default")
		if err != nil {
			panic(err)
		}
	}
	return os.Getenv("POD_NAMESPACE")
}

func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "timeout")
}
