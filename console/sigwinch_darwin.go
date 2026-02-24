package console

import "os"

// HandleSIGWINCH is a no-op on darwin. Console functionality requires Linux.
func HandleSIGWINCH(_ *os.File, _ *os.File) func() {
	return func() {}
}
