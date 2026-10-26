package linear

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

type oauthFlow struct {
	verifier string
	state    string
	server   *http.Server
	codes    chan string
	errs     chan error
}

var oauthMu sync.Mutex

// Login implements sdk.Authenticator.
func (b *Backend) Login(ctx context.Context, input string, hasInput bool) (*pb.LoginResponse, error) {
	if hasInput {
		return b.loginWithAPIKey(ctx, input)
	}

	oauthMu.Lock()
	flow := b.oauthFlow
	oauthMu.Unlock()
	if flow != nil {
		return b.finishOAuth(ctx, flow)
	}

	if u, err := b.client.currentViewer(ctx); err == nil {
		return &pb.LoginResponse{
			Done:    true,
			Message: "Already logged in to Linear as " + u.label(),
		}, nil
	}

	if clientID := b.client.oauthClientID(); clientID != "" {
		return b.startOAuth(ctx, clientID)
	}

	return &pb.LoginResponse{
		Prompt:  "Linear API key: ",
		Secret:  true,
		Message: "No OAuth client ID is configured, so paste a personal API key. Create one at Linear Settings > Security & access > Personal API keys.",
	}, nil
}

func (b *Backend) loginWithAPIKey(ctx context.Context, key string) (*pb.LoginResponse, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("Linear API key is required")
	}
	u, err := b.client.verify(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("Linear rejected the API key: %w", err)
	}
	if err := sdk.SaveCredential("linear", map[string]string{
		"auth_type": "api_key",
		"token":     key,
	}); err != nil {
		return nil, err
	}
	b.client.resetAuth()
	return &pb.LoginResponse{
		Done:    true,
		Message: "Logged in to Linear as " + u.label(),
	}, nil
}

func (b *Backend) startOAuth(ctx context.Context, clientID string) (*pb.LoginResponse, error) {
	verifier, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	state, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	listener, err := net.Listen("tcp", "127.0.0.1:43172")
	if err != nil {
		return nil, fmt.Errorf("could not listen on 127.0.0.1:43172 for the OAuth callback: %w", err)
	}

	flow := &oauthFlow{
		verifier: verifier,
		state:    state,
		codes:    make(chan string, 1),
		errs:     make(chan error, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(flow.state)) != 1 {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			select {
			case flow.errs <- errors.New("Linear OAuth state did not match"):
			default:
			}
			return
		}
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			if desc == "" {
				desc = e
			}
			http.Error(w, "Authorization failed", http.StatusBadRequest)
			select {
			case flow.errs <- fmt.Errorf("Linear authorization failed: %s", desc):
			default:
			}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			select {
			case flow.errs <- errors.New("Linear OAuth callback had no authorization code"):
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body><h2>Logged in to Linear</h2><p>You can close this window and return to the terminal.</p></body></html>`)
		select {
		case flow.codes <- code:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	flow.server = srv
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case flow.errs <- err:
			default:
			}
		}
	}()

	authURL := oauthAuthorizeURL(clientID, state, challenge)
	if err := openBrowser(authURL); err != nil {
		oauthMu.Lock()
		b.oauthFlow = flow
		oauthMu.Unlock()
		return &pb.LoginResponse{
			Url:     authURL,
			Message: "Open the URL above to authorize Linear, then return here.",
		}, nil
	}
	oauthMu.Lock()
	b.oauthFlow = flow
	oauthMu.Unlock()
	return &pb.LoginResponse{
		Url:     authURL,
		Message: "Waiting for authorization in your browser...",
	}, nil
}

func (b *Backend) finishOAuth(ctx context.Context, flow *oauthFlow) (*pb.LoginResponse, error) {
	defer func() {
		oauthMu.Lock()
		b.oauthFlow = nil
		oauthMu.Unlock()
		_ = flow.server.Close()
	}()

	var code string
	select {
	case code = <-flow.codes:
	case err := <-flow.errs:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, errors.New("timed out waiting for the Linear OAuth callback")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	clientID := b.client.oauthClientID()
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", oauthRedirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", flow.verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.client.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Linear token exchange failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var tok tokenResponse
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("decode Linear token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("Linear token exchange returned no access token")
	}
	u, err := b.client.verify(ctx, "Bearer "+tok.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("verify Linear token: %w", err)
	}
	creds := map[string]string{
		"auth_type":     "oauth",
		"access_token":  tok.AccessToken,
		"refresh_token": tok.RefreshToken,
		"token_type":    tok.TokenType,
		"scope":         tok.Scope,
		"client_id":     clientID,
		"expires_at":    b.client.now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC().Format(time.RFC3339),
	}
	if err := sdk.SaveCredential("linear", creds); err != nil {
		return nil, err
	}
	b.client.resetAuth()
	return &pb.LoginResponse{
		Done:    true,
		Message: "Logged in to Linear as " + u.label(),
	}, nil
}

func oauthAuthorizeURL(clientID, state, challenge string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", oauthRedirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "read,write")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("actor", "user")
	q.Set("prompt", "consent")
	return "https://linear.app/oauth/authorize?" + q.Encode()
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
