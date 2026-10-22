package cli

import (
	"github.com/metruzanca/tasks/internal/cli/tui"
	"github.com/spf13/cobra"
)

func (a *App) boardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "board",
		Short: "Open the interactive kanban board",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			return tui.Run(ctx, s.Backend)
		},
	}
}
