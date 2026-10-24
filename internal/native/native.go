// Package native serves the backends that are compiled into the tasks binary.
package native

import (
	"fmt"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/plugins/github"
	"github.com/metruzanca/tasks/plugins/linear"
	"github.com/metruzanca/tasks/plugins/sqlite"
)

var names = []string{"sqlite", "github", "linear"}

// Names returns the native backend names in display order.
func Names() []string {
	return append([]string(nil), names...)
}

// IsNative reports whether name is served by the tasks binary itself.
func IsNative(name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// Serve starts a native backend. It does not return until the host stops it.
func Serve(name string) error {
	switch name {
	case "sqlite":
		b, err := sqlite.NewFromEnv()
		if err != nil {
			return err
		}
		sdk.Serve(b)
	case "github":
		b, err := github.NewFromEnv()
		if err != nil {
			return err
		}
		sdk.Serve(b)
	case "linear":
		b, err := linear.NewFromEnv()
		if err != nil {
			return err
		}
		sdk.Serve(b)
	default:
		return fmt.Errorf("unknown native backend %q", name)
	}
	return nil
}
