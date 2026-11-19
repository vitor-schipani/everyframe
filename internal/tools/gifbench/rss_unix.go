//go:build unix

package main

import (
	"runtime"
	"syscall"
)

// peakRSS is the most physical memory the process has held, or 0 if the
// system does not say.
func peakRSS() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		return int64(ru.Maxrss) // bytes
	}
	return int64(ru.Maxrss) << 10 // kilobytes elsewhere
}
