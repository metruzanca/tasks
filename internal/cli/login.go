package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *App) loginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login [backend]",
		Short: "Store credentials for a backend",
		Long: `Start the backend's interactive login flow. Linear opens a browser for
OAuth when an OAuth client ID is configured, otherwise it asks for a personal
API key. Tokens are stored in the credentials file, never in config.`,
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
			sess, err := a.manager.Open(cmd.Context(), name)
			if err != nil {
				return err
			}
			defer sess.Close()
			auth, err := sess.Authenticator()
			if err != nil {
				return err
			}
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
		},
	}
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
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
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
