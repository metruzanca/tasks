package pluginmgr

import (
	"os"

	"github.com/hashicorp/go-hclog"
)

// pluginLogger routes plugin stderr through hclog.
type pluginLogger struct {
	hclog.Logger
}

func newPluginLogger(verbose bool) *pluginLogger {
	level := hclog.Warn
	if verbose {
		level = hclog.Debug
	}
	return &pluginLogger{Logger: hclog.New(&hclog.LoggerOptions{
		Name:   "tasks",
		Level:  level,
		Output: os.Stderr,
	})}
}
