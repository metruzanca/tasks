package main

import (
	"fmt"
	"os"

	"github.com/metruzanca/tasks/internal/cli"
)

var version = "dev"

func main() {
	cli.Version = version
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "tasks:", err)
		os.Exit(1)
	}
}
