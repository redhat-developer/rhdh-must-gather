package main

import (
	"fmt"
	"os"

	"github.com/redhat-developer/rhdh-must-gather/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
