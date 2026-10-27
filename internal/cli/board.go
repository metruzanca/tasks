package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/metruzanca/tasks/internal/browser"
	"github.com/metruzanca/tasks/internal/cli/tui"
	"github.com/metruzanca/tasks/internal/render"
	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/spf13/cobra"
)

func (a *App) boardCmd() *cobra.Command {
	var web bool
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
			if web {
				url, err := openBoardWeb(ctx, s.Backend, browser.Open)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Opened %s\n", url)
				return nil
			}
			return tui.Run(ctx, s.Backend)
		},
	}
	cmd.Flags().BoolVar(&web, "web", false, "open the board in your browser")
	cmd.AddCommand(a.columnsCmd())
	return cmd
}

func openBoardWeb(ctx context.Context, backend sdk.Backend, opener func(string) error) (string, error) {
	info, err := backend.Describe(ctx)
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(info.GetBoardUrl())
	if url == "" {
		return "", errors.New("this backend does not have a web board URL")
	}
	if err := opener(url); err != nil {
		return "", fmt.Errorf("open %s: %w", url, err)
	}
	return url, nil
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
