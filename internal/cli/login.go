package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/metruzanca/tasks/internal/config"
	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *App) authCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage credentials"}
	cmd.AddCommand(a.loginCmd(), a.logoutCmd())
	return cmd
}

func (a *App) loginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login [backend]",
		Short: "Store credentials for a backend",
		Long: `Start the backend's interactive login flow. GitHub uses the existing gh
session or starts gh OAuth if none is present. Linear opens a browser for OAuth.
Tokens are stored in the credentials file, never in config.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.load()
			if err != nil {
				return err
			}
			name := s.Config.Backend
			if len(args) == 1 {
				name = args[0]
			}
			if a.backend != "" {
				name = a.backend
			}
			if name == "sqlite" {
				return errors.New("the sqlite backend does not use credentials")
			}
			return a.loginBackend(cmd, name)
		},
	}
}

func (a *App) loginBackend(cmd *cobra.Command, name string) error {
	s, err := a.load()
	if err != nil {
		return err
	}
	if name == "github" {
		if err := ensureGitHubSession(cmd, s); err != nil {
			return err
		}
	}
	sess, err := a.manager.Open(cmd.Context(), name)
	if err != nil {
		return err
	}
	defer sess.Close()
	auth, err := sess.Authenticator()
	if err != nil {
		return err
	}
	return runLoginLoop(cmd, auth)
}

func runLoginLoop(cmd *cobra.Command, auth sdk.Authenticator) error {
	var input string
	hasInput := false
	for {
		resp, err := auth.Login(cmd.Context(), input, hasInput)
		if err != nil {
			return err
		}
		if resp.GetUrl() != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Open: %s\n", resp.GetUrl())
		}
		if resp.GetMessage() != "" {
			fmt.Fprintln(cmd.OutOrStdout(), resp.GetMessage())
		}
		if resp.GetDone() {
			return nil
		}
		if resp.GetPrompt() == "" {
			// The backend is waiting for an external step, such as the
			// browser OAuth callback. Call it again to continue waiting.
			continue
		}
		input, err = readSecretPrompt(cmd, resp.GetPrompt(), resp.GetSecret())
		if err != nil {
			return err
		}
		hasInput = true
	}
}

func ensureGitHubSession(cmd *cobra.Command, s *config.Settings) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil
	}
	if githubToken(cmd.Context(), s) != "" {
		return nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "No GitHub session found. Starting GitHub OAuth with gh...")
	c := exec.CommandContext(cmd.Context(), "gh", "auth", "login",
		"--hostname", "github.com",
		"--web",
		"--git-protocol", "https",
		"--scopes", "repo,project",
	)
	c.Stdin = os.Stdin
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	if err := c.Run(); err != nil {
		return fmt.Errorf("gh auth login: %w", err)
	}
	return nil
}

func githubToken(ctx context.Context, s *config.Settings) string {
	if s.Config.GitHub.TokenEnv != "" {
		if v := os.Getenv(s.Config.GitHub.TokenEnv); v != "" {
			return v
		}
	}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	if creds, err := sdk.LoadCredential("github"); err == nil && creds["token"] != "" {
		return creds["token"]
	}
	if out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func readSecretPrompt(cmd *cobra.Command, prompt string, secret bool) (string, error) {
	fmt.Fprint(cmd.ErrOrStderr(), prompt)
	if secret && term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := readLine(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (a *App) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [backend]",
		Short: "Remove stored credentials for a backend",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.load()
			if err != nil {
				return err
			}
			name := s.Config.Backend
			if len(args) == 1 {
				name = args[0]
			}
			if a.backend != "" {
				name = a.backend
			}
			if err := sdk.DeleteCredential(name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed credentials for %s from %s\n", name, s.CredentialsPath)
			return nil
		},
	}
}

// readLine reads one line without buffering ahead, so a later prompt can share
// the same underlying input.
func readLine(r io.Reader) (string, error) {
	var b []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			switch one[0] {
			case '\n':
				return string(b), nil
			case '\r':
			default:
				b = append(b, one[0])
			}
		}
		if err != nil {
			if len(b) > 0 {
				return string(b), nil
			}
			return "", err
		}
	}
}
