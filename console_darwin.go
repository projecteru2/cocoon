package main

import "os"

// handleSIGWINCH is a no-op on darwin. Console requires Linux.
func handleSIGWINCH(_ *os.File, _ *os.File) func() {
	return func() {}
}
