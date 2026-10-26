package cli

import (
	"github.com/metruzanca/tasks/internal/cli/tui"
	"github.com/metruzanca/tasks/internal/render"
	"github.com/spf13/cobra"
)

func (a *App) boardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "board",
		Short: "Open the interactive kanban board",
		Args:  cobra.NoArgs,
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
	cmd.AddCommand(a.columnsCmd())
	return cmd
}

func (a *App) columnsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "columns",
		Short: "List board columns",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			info, err := s.Backend.Describe(ctx)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), info)
			}
			render.ColumnsTable(cmd.OutOrStdout(), info.Columns, info.ReadyStatus)
			return nil
		},
	}
}
