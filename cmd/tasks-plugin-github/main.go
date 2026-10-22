package main

import (
	"fmt"
	"os"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/plugins/github"
)

func main() {
	b, err := github.NewFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tasks-plugin-github:", err)
		os.Exit(1)
	}
	sdk.Serve(b)
}
