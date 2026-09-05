package cli

import "os"

var version = "0.0.0-unknown"

func getVersion() string {
	if v := os.Getenv("RHDH_MUST_GATHER_VERSION"); v != "" {
		return v
	}
	return version
}
