//go:build !e2e

package main

// updateSeam is the update check's origin and running version; only the e2e build moves them.
func updateSeam(version string) (baseURL, running string, on bool) { return "", version, true }
