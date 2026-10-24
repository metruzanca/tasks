package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/metruzanca/tasks/internal/config"
	"github.com/metruzanca/tasks/internal/native"
	"github.com/metruzanca/tasks/internal/pluginmgr"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Version is set at build time.
var Version = "dev"

// App holds shared CLI state.
type App struct {
	settings *config.Settings
	manager  *pluginmgr.Manager
	backend  string
	jsonOut  bool
	noColor  bool
	verbose  bool
}

// Execute runs the CLI.
func Execute() error {
	app := &App{}
	root := app.root()
	app.noColor = !term.IsTerminal(int(os.Stdout.Fd()))
	return root.Execute()
}

func (a *App) root() *cobra.Command {
	root := &cobra.Command{
		Use:           "tasks",
		Short:         "A terminal kanban board for humans and agents",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.backend, "backend", "", "backend to use (overrides config)")
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "output JSON")
	root.PersistentFlags().BoolVar(&a.noColor, "no-color", false, "disable color")
	root.PersistentFlags().BoolVar(&a.verbose, "verbose", false, "show plugin logs")

	root.AddCommand(
		a.initCmd(),
		a.addCmd(),
		a.listCmd(),
		a.showCmd(),
		a.editCmd(),
		a.moveCmd(),
		a.doneCmd(),
		a.nextCmd(),
		a.commentCmd(),
		a.commentsCmd(),
		a.archiveCmd(),
		a.rmCmd(),
		a.columnsCmd(),
		a.boardCmd(),
		a.pluginCmd(),
		a.loginCmd(),
		a.logoutCmd(),
		a.doctorCmd(),
		a.skillCmd(),
		a.configCmd(),
		a.versionCmd(),
		a.serveCmd(),
	)
	return root
}

func (a *App) load() (*config.Settings, error) {
	if a.settings != nil {
		return a.settings, nil
	}
	s, err := config.Load()
	if err != nil {
		return nil, err
	}
	a.settings = s
	a.manager = pluginmgr.New(s, a.verbose)
	return s, nil
}

func (a *App) open(ctx context.Context) (*pluginmgr.Session, error) {
	if _, err := a.load(); err != nil {
		return nil, err
	}
	return a.manager.Open(ctx, a.backend)
}

func (a *App) backendName() string {
	if a.backend != "" {
		return a.backend
	}
	if a.settings != nil && a.settings.Config.Backend != "" {
		return a.settings.Config.Backend
	}
	return "sqlite"
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), map[string]string{"version": Version})
			}
			fmt.Fprintln(cmd.OutOrStdout(), Version)
			return nil
		},
	}
}

func (a *App) serveCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "__plugin", Hidden: true}
	for _, name := range native.Names() {
		name := name
		cmd.AddCommand(&cobra.Command{
			Use:   name,
			Short: "serve the native " + name + " backend",
			RunE: func(cmd *cobra.Command, args []string) error {
				return native.Serve(name)
			},
		})
	}
	return cmd
}
