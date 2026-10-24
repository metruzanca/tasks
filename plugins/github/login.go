package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

// Login implements sdk.Authenticator.
func (b *Backend) Login(ctx context.Context, input string, hasInput bool) (*pb.LoginResponse, error) {
	if !hasInput {
		return &pb.LoginResponse{
			Prompt:  "GitHub token (blank uses `gh auth token`): ",
			Secret:  true,
			Message: "Create a token with repo and project scopes, or leave it blank to use the gh CLI.",
		}, nil
	}
	token := strings.TrimSpace(input)
	if token == "" {
		out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
		if err != nil {
			return nil, errors.New("no token entered and `gh auth token` failed")
		}
		token = strings.TrimSpace(string(out))
	}
	if token == "" {
		return nil, errors.New("GitHub token is required")
	}
	login, err := b.client.viewerWithToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("GitHub rejected the token: %w", err)
	}
	if err := sdk.SaveCredential("github", map[string]string{
		"auth_type": "token",
		"token":     token,
	}); err != nil {
		return nil, err
	}
	return &pb.LoginResponse{
		Done:    true,
		Message: "Logged in to GitHub as " + login,
	}, nil
}

func (c *client) viewerWithToken(ctx context.Context, token string) (string, error) {
	var out struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}
	if err := c.graphqlWithToken(ctx, token, `query { viewer { login } }`, nil, &out); err != nil {
		return "", err
	}
	if out.Viewer.Login == "" {
		return "", errors.New("GitHub did not return the authenticated user")
	}
	return out.Viewer.Login, nil
}
