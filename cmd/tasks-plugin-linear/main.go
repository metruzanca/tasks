package main

import (
	"fmt"
	"os"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/plugins/linear"
)

func main() {
	b, err := linear.NewFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tasks-plugin-linear:", err)
		os.Exit(1)
	}
	sdk.Serve(b)
}
