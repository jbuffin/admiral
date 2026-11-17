//go:build e2e

package main

import "os"

// The test build points the update check at a local releases server and gives it a running version, since make build stamps
// none; without both it requests nothing, so no run reaches GitHub. A release is built without this file.
func updateSeam(version string) (baseURL, running string, on bool) {
	baseURL, running = os.Getenv("ADMIRAL_UPDATE_TEST_URL"), os.Getenv("ADMIRAL_UPDATE_TEST_VERSION")
	return baseURL, running, baseURL != "" && running != ""
}
