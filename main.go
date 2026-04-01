package main

import (
	"os"

	"github.com/cocoonstack/cocoon/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
