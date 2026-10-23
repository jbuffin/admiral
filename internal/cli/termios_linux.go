package cli

import "golang.org/x/sys/unix"

// Admiral ships for macOS only. This lets PR CI build and vet on Linux.
const getTermios = unix.TCGETS
