package main

import (
	"os"

	"github.com/projecteru2/cocoon/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
