// Package main is the entrypoint for the mneme binary.
package main

import (
	"fmt"
	"os"

	"github.com/juanftp/mneme/internal/cli"
)

func main() {
	cmd := cli.NewRootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
