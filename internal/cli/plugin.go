package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/metruzanca/tasks/internal/pluginmgr"
	"github.com/metruzanca/tasks/internal/skill"
	"github.com/spf13/cobra"
)

func (a *App) pluginCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plugin", Short: "Manage backend plugins"}
	cmd.AddCommand(a.pluginListCmd(), a.pluginInstallCmd(), a.pluginRemoveCmd(), a.pluginPathCmd())
	return cmd
}

func (a *App) pluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List backends",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := a.load(); err != nil {
				return err
			}
			plugins, err := a.manager.List()
			if err != nil {
				return err
			}
			if a.jsonOut {
				out := []map[string]string{{"name": "sqlite", "path": "built-in"}}
				for _, p := range plugins {
					out = append(out, map[string]string{"name": p.Name, "path": p.Path})
				}
				return printJSON(cmd.OutOrStdout(), out)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s\n", "sqlite", "built-in")
			for _, p := range plugins {
				fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s\n", p.Name, p.Path)
			}
			return nil
		},
	}
}

func (a *App) pluginInstallCmd() *cobra.Command {
	var module, version, name string
	cmd := &cobra.Command{
		Use:   "install <name|module>",
		Short: "Install a backend plugin with go install",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := a.load(); err != nil {
				return err
			}
			arg := args[0]
			if strings.Contains(arg, "/") {
				if module == "" {
					module = arg
				}
			} else if name == "" {
				name = arg
			}
			if module == "" {
				module = pluginmgr.OfficialModule(name)
			}
			if name == "" {
				parts := strings.Split(module, "/")
				name = strings.TrimPrefix(parts[len(parts)-1], "tasks-plugin-")
			}
			path, err := a.manager.Install(cmd.Context(), module, name, version)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s at %s\n", name, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&module, "module", "", "Go module path")
	cmd.Flags().StringVar(&version, "version", "", "module version (default latest)")
	cmd.Flags().StringVar(&name, "name", "", "plugin name")
	return cmd
}

func (a *App) pluginRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed backend plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := a.load(); err != nil {
				return err
			}
			if err := a.manager.Remove(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", args[0])
			return nil
		},
	}
}

func (a *App) pluginPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path <name>",
		Short: "Print the path to a backend plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := a.load(); err != nil {
				return err
			}
			if args[0] == "sqlite" {
				self, err := os.Executable()
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), self+" (built-in __plugin sqlite)")
				return nil
			}
			p, err := a.manager.Find(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		},
	}
}

func (a *App) skillCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Print or install the agent skill"}
	cmd.AddCommand(&cobra.Command{
		Use:   "print",
		Short: "Print the agent skill",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), skill.Markdown)
			return nil
		},
	})
	var dir string
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the agent skill for OpenCode",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				base, err := os.UserConfigDir()
				if err != nil {
					return err
				}
				dir = filepath.Join(base, "opencode", "skills", "tasks")
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			path := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(path, []byte(skill.Markdown), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed skill at %s\n", path)
			return nil
		},
	}
	install.Flags().StringVar(&dir, "dir", "", "destination directory")
	cmd.AddCommand(install)
	return cmd
}
