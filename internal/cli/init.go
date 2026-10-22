package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func (a *App) initCmd() *cobra.Command {
	var (
		db, owner, repo, statusField, priorityField, write string
		project                                            int
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure a backend and board",
		Long: `Write a config file. Use --backend github with --owner, --project, and
--repo for GitHub Projects, or leave the defaults for the local SQLite board.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.load()
			if err != nil {
				return err
			}
			backend := a.backend
			if backend == "" {
				backend = s.Config.Backend
			}
			if backend == "" {
				backend = "sqlite"
			}
			s.Config.Backend = backend
			if db != "" {
				s.Config.SQLite.Path = db
			}
			if owner != "" {
				s.Config.GitHub.Owner = owner
			}
			if project != 0 {
				s.Config.GitHub.Project = project
			}
			if repo != "" {
				s.Config.GitHub.Repo = repo
			}
			if statusField != "" {
				s.Config.GitHub.StatusField = statusField
			}
			if priorityField != "" {
				s.Config.GitHub.PriorityField = priorityField
			}
			if backend == "github" {
				if s.Config.GitHub.Owner == "" || s.Config.GitHub.Project == 0 || s.Config.GitHub.Repo == "" {
					return fmt.Errorf("github requires --owner, --project, and --repo")
				}
			}
			path := s.GlobalPath
			isRepo := write == "repo"
			if isRepo {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				path = filepath.Join(cwd, ".tasks.toml")
			}
			if err := s.Write(path, isRepo); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", path)
			if _, err := os.Stat(s.Config.SQLite.Path); err != nil && backend == "sqlite" {
				fmt.Fprintf(cmd.OutOrStdout(), "SQLite database will be created at %s\n", s.Config.SQLite.Path)
			}
			sess, err := a.manager.Open(cmd.Context(), backend)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
				return nil
			}
			defer sess.Close()
			if msgs, err := sess.Backend.Setup(cmd.Context(), true); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: setup: %v\n", err)
			} else {
				for _, msg := range msgs {
					fmt.Fprintln(cmd.OutOrStdout(), "setup: "+msg)
				}
			}
			info, err := sess.Backend.Describe(cmd.Context())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Backend: %s (%s)\n", info.DisplayName, info.BoardName)
			return nil
		},
	}
	cmd.Flags().StringVar(&write, "write", "global", "where to write config: global or repo")
	cmd.Flags().StringVar(&db, "db", "", "SQLite database path")
	cmd.Flags().StringVar(&owner, "owner", "", "GitHub user or organization")
	cmd.Flags().IntVar(&project, "project", 0, "GitHub project number")
	cmd.Flags().StringVar(&repo, "repo", "", "GitHub repository (owner/name)")
	cmd.Flags().StringVar(&statusField, "status-field", "", "GitHub status single-select field")
	cmd.Flags().StringVar(&priorityField, "priority-field", "", "GitHub priority single-select field")
	return cmd
}

func (a *App) configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect configuration"}
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print config paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.load()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "global: %s\n", s.GlobalPath)
			if s.RepoPath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "repo:   %s\n", s.RepoPath)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "data:   %s\n", s.DataDir)
			fmt.Fprintf(cmd.OutOrStdout(), "plugins: %s\n", s.Config.PluginDir)
			return nil
		},
	}, &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.load()
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), s.Config)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "backend:  %s\n", s.Config.Backend)
			if s.Config.Backend == "sqlite" {
				fmt.Fprintf(cmd.OutOrStdout(), "database: %s\n", s.Config.SQLite.Path)
				fmt.Fprintf(cmd.OutOrStdout(), "me:       %s\n", s.Config.SQLite.Me)
			}
			if s.Config.Backend == "github" {
				fmt.Fprintf(cmd.OutOrStdout(), "owner:    %s\n", s.Config.GitHub.Owner)
				fmt.Fprintf(cmd.OutOrStdout(), "project:  %d\n", s.Config.GitHub.Project)
				fmt.Fprintf(cmd.OutOrStdout(), "repo:     %s\n", s.Config.GitHub.Repo)
			}
			return nil
		},
	})
	return cmd
}

func (a *App) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check config, plugins, and backend connectivity",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.load()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "config:  %s\n", s.GlobalPath)
			if s.RepoPath != "" {
				fmt.Fprintf(w, "repo:    %s\n", s.RepoPath)
			}
			fmt.Fprintf(w, "backend: %s\n", s.Config.Backend)
			fmt.Fprintf(w, "plugins: %s\n", s.Config.PluginDir)
			external, _ := a.manager.List()
			for _, p := range external {
				fmt.Fprintf(w, "  found %s at %s\n", p.Name, p.Path)
			}
			sess, err := a.manager.Open(ctx, a.backend)
			if err != nil {
				return err
			}
			defer sess.Close()
			if err := sess.Backend.Health(ctx); err != nil {
				return fmt.Errorf("health check failed: %w", err)
			}
			info, err := sess.Backend.Describe(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "board:   %s\n", info.BoardName)
			if info.BoardUrl != "" {
				fmt.Fprintf(w, "url:     %s\n", info.BoardUrl)
			}
			fmt.Fprintf(w, "ready:   %s\n", info.ReadyStatus)
			fmt.Fprintf(w, "columns: ")
			for i, c := range info.Columns {
				if i > 0 {
					fmt.Fprint(w, ", ")
				}
				fmt.Fprint(w, c.Name)
			}
			fmt.Fprintln(w)
			fmt.Fprintln(w, "OK")
			return nil
		},
	}
}
