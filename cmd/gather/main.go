package main

import (
	"os"

	"github.com/redhat-developer/rhdh-must-gather/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
