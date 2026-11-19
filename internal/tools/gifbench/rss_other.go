//go:build !unix

package main

// peakRSS is the most physical memory the process has held, or 0 if the
// system does not say.
func peakRSS() int64 { return 0 }
