package utils

import "fmt"

var BuildVersion string
var BuildRelease string
var BuildTime string

func GetVersion() string {
	if BuildRelease == "" {
		return fmt.Sprintf("git-%v", BuildVersion)
	}

	return fmt.Sprintf("%v (git-%v)", BuildRelease, BuildVersion)
}
