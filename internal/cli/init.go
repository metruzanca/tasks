package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/metruzanca/tasks/internal/config"
	"github.com/metruzanca/tasks/internal/native"
	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *App) initCmd() *cobra.Command {
	var (
		db, owner, repo, statusField, priorityField, write string
		team, oauthClientID, tokenEnv, readyStatus         string
		project                                            int
		forceInteractive                                   bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure a backend and board",
		Long: `Walk through choosing a backend and writing its config.

Flags skip the form, so the command also works without a terminal. Credentials
are never written to config. Run tasks auth login <backend> after initializing
if the backend needs them.

SQLite boards are stored per repository at .tasks/tasks.db unless sqlite.path
is set explicitly. Use --db to opt into a shared board.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.load()
			if err != nil {
				return err
			}
			interactive := forceInteractive || (term.IsTerminal(int(os.Stdin.Fd())) && !hasInitFlags(cmd, a.backend))

			v := initValues{
				Backend:       a.backend,
				Write:         write,
				DB:            db,
				DBSet:         cmd.Flags().Changed("db"),
				Owner:         owner,
				Project:       project,
				Repo:          repo,
				StatusField:   statusField,
				PriorityField: priorityField,
				Team:          team,
				ReadyStatus:   readyStatus,
				OAuthClientID: oauthClientID,
				TokenEnv:      tokenEnv,
			}
			fillInitDefaults(cmd, s, &v, interactive)
			if v.Write != "global" && v.Write != "repo" {
				return fmt.Errorf("--write must be global or repo")
			}

			if interactive {
				if err := a.runInitForm(cmd, s, &v); err != nil {
					if errors.Is(err, errInitCancelled) {
						fmt.Fprintln(cmd.OutOrStdout(), "Cancelled.")
						return nil
					}
					return err
				}
			}

			if err := validateInitValues(&v); err != nil {
				return err
			}
			applyInitValues(s, &v)
			if cmd.Flags().Changed("token-env") {
				switch v.Backend {
				case "github":
					s.Config.GitHub.TokenEnv = tokenEnv
				case "linear":
					s.Config.Linear.TokenEnv = tokenEnv
				}
			}
			if cmd.Flags().Changed("oauth-client-id") && v.Backend == "linear" {
				s.Config.Linear.OAuthClientID = oauthClientID
			}

			isRepo := v.Write == "repo"
			path := s.GlobalPath
			if isRepo {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				path = filepath.Join(cwd, ".tasks.toml")
			}

			runtimeSQLitePath := s.Config.SQLite.Path
			writeSQLitePath := runtimeSQLitePath
			if !v.DBSet {
				switch {
				case isRepo && s.RepoSqlitePathSet:
					writeSQLitePath = s.RepoSqlitePath
				case !isRepo && s.GlobalSQLitePathSet:
					writeSQLitePath = s.GlobalSQLitePath
				default:
					// Do not bake the per-repo default into a config file.
					writeSQLitePath = ""
				}
			}
			s.Config.SQLite.Path = writeSQLitePath
			writeErr := s.Write(path, isRepo)
			s.Config.SQLite.Path = runtimeSQLitePath
			if writeErr != nil {
				return writeErr
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", path)
			if v.Backend == "sqlite" {
				fmt.Fprintf(cmd.OutOrStdout(), "SQLite database: %s\n", s.Config.SQLite.Path)
			}

			if interactive && (v.Backend == "github" || v.Backend == "linear") {
				fmt.Fprintln(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), "Authentication:")
				if err := a.loginBackend(cmd, v.Backend); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: login: %v\n", err)
				}
			}

			sess, err := a.manager.Open(cmd.Context(), v.Backend)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
				return nil
			}
			defer sess.Close()
			msgs, setupErr := sess.Backend.Setup(cmd.Context(), true)
			info, err := sess.Backend.Describe(cmd.Context())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
				if needsLoginHint(v.Backend, err) {
					fmt.Fprintf(cmd.OutOrStdout(), "Next: tasks auth login %s\n", v.Backend)
				}
				return nil
			}
			if setupErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: setup: %v\n", setupErr)
			} else {
				for _, msg := range msgs {
					fmt.Fprintln(cmd.OutOrStdout(), "setup: "+msg)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Backend: %s (%s)\n", info.DisplayName, info.BoardName)
			fmt.Fprintln(cmd.OutOrStdout(), "Run `tasks board` to get started.")
			return nil
		},
	}
	cmd.Flags().StringVar(&write, "write", "global", "where to write config: global or repo")
	cmd.Flags().StringVar(&db, "db", "", "SQLite database path")
	cmd.Flags().StringVar(&owner, "owner", "", "GitHub user or organization")
	cmd.Flags().IntVar(&project, "project", 0, "GitHub project number (from the project URL)")
	cmd.Flags().StringVar(&repo, "repo", "", "GitHub repository (owner/name)")
	cmd.Flags().StringVar(&statusField, "status-field", "", "GitHub status single-select field")
	cmd.Flags().StringVar(&priorityField, "priority-field", "", "GitHub priority single-select field")
	cmd.Flags().StringVar(&team, "team", "", "Linear team key or ID")
	cmd.Flags().StringVar(&readyStatus, "ready-status", "", "Linear ready workflow state")
	cmd.Flags().StringVar(&oauthClientID, "oauth-client-id", "", "Linear OAuth app client ID")
	cmd.Flags().StringVar(&tokenEnv, "token-env", "", "environment variable to read the token from")
	cmd.Flags().BoolVar(&forceInteractive, "interactive", false, "show the form even when flags are set")
	return cmd
}

func fillInitDefaults(cmd *cobra.Command, s *config.Settings, v *initValues, interactive bool) {
	if v.Backend == "" {
		v.Backend = s.Config.Backend
	}
	if v.Backend == "" {
		v.Backend = "sqlite"
	}
	if !cmd.Flags().Changed("write") {
		v.Write = "global"
		if interactive && s.RepoRoot != "" {
			v.Write = "repo"
		}
	}
	if !cmd.Flags().Changed("owner") {
		v.Owner = s.Config.GitHub.Owner
	}
	if !cmd.Flags().Changed("project") {
		v.Project = s.Config.GitHub.Project
	}
	if !cmd.Flags().Changed("repo") {
		v.Repo = s.Config.GitHub.Repo
	}
	if !cmd.Flags().Changed("status-field") {
		v.StatusField = s.Config.GitHub.StatusField
	}
	if !cmd.Flags().Changed("priority-field") {
		v.PriorityField = s.Config.GitHub.PriorityField
	}
	if !cmd.Flags().Changed("team") {
		v.Team = s.Config.Linear.Team
	}
	if !cmd.Flags().Changed("ready-status") {
		v.ReadyStatus = s.Config.Linear.ReadyStatus
	}
	if !cmd.Flags().Changed("oauth-client-id") {
		v.OAuthClientID = s.Config.Linear.OAuthClientID
	}
	if !cmd.Flags().Changed("token-env") {
		switch v.Backend {
		case "github":
			v.TokenEnv = s.Config.GitHub.TokenEnv
		case "linear":
			v.TokenEnv = s.Config.Linear.TokenEnv
		}
	}
	if !cmd.Flags().Changed("db") {
		v.DB = ""
		v.DBSet = false
		if interactive {
			switch {
			case v.Write == "repo" && s.RepoSqlitePathSet:
				v.DB = s.RepoSqlitePath
				v.DBSet = true
			case v.Write == "global" && s.RepoRoot == "" && s.GlobalSQLitePathSet:
				v.DB = s.GlobalSQLitePath
				v.DBSet = true
			}
		}
	}
}

func validateInitValues(v *initValues) error {
	if v.Write != "global" && v.Write != "repo" {
		return fmt.Errorf("--write must be global or repo")
	}
	switch v.Backend {
	case "github":
		if strings.TrimSpace(v.Owner) == "" || v.Project == 0 || strings.TrimSpace(v.Repo) == "" {
			return fmt.Errorf("github requires an owner, a project number, and a repository")
		}
		v.Repo = normalizeRepo(v.Owner, v.Repo)
		if !strings.Contains(v.Repo, "/") {
			return fmt.Errorf("github repo %q must be owner/name", v.Repo)
		}
	case "linear":
		if strings.TrimSpace(v.Team) == "" {
			return fmt.Errorf("linear requires a team")
		}
	}
	return nil
}

func applyInitValues(s *config.Settings, v *initValues) {
	s.Config.Backend = v.Backend
	if v.DBSet {
		s.Config.SQLite.Path = v.DB
	}
	switch v.Backend {
	case "github":
		s.Config.GitHub.Owner = strings.TrimSpace(v.Owner)
		s.Config.GitHub.Project = v.Project
		s.Config.GitHub.Repo = normalizeRepo(v.Owner, v.Repo)
		if v.StatusField != "" {
			s.Config.GitHub.StatusField = v.StatusField
		}
		if v.PriorityField != "" {
			s.Config.GitHub.PriorityField = v.PriorityField
		}
	case "linear":
		s.Config.Linear.Team = strings.TrimSpace(v.Team)
		s.Config.Linear.ReadyStatus = strings.TrimSpace(v.ReadyStatus)
	}
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
			if s.RepoRoot != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "repo root: %s\n", s.RepoRoot)
			}
			if s.Config.Backend == "sqlite" {
				fmt.Fprintf(cmd.OutOrStdout(), "database:  %s\n", s.Config.SQLite.Path)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "plugins: %s\n", s.Config.PluginDir)
			fmt.Fprintf(cmd.OutOrStdout(), "credentials: %s\n", s.CredentialsPath)
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
			if s.Config.Backend == "linear" {
				fmt.Fprintf(cmd.OutOrStdout(), "team:     %s\n", s.Config.Linear.Team)
				fmt.Fprintf(cmd.OutOrStdout(), "oauth:    %s\n", s.Config.Linear.OAuthClientID)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "credentials: %s\n", s.CredentialsPath)
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
			fmt.Fprintf(w, "credentials: %s\n", s.CredentialsPath)
			if creds, err := sdk.LoadCredential(s.Config.Backend); err == nil && len(creds) > 0 {
				kind := creds["auth_type"]
				if kind == "" {
					kind = "stored"
				}
				fmt.Fprintf(w, "credential: %s\n", kind)
			}
			if s.Config.Backend == "sqlite" {
				fmt.Fprintf(w, "database: %s\n", s.Config.SQLite.Path)
			}
			fmt.Fprintf(w, "native:  %s\n", strings.Join(native.Names(), ", "))
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

func needsLoginHint(backend string, err error) bool {
	if backend != "github" && backend != "linear" {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "token") || strings.Contains(msg, "credential") || strings.Contains(msg, "auth")
}
