package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/metruzanca/tasks/pkg/sdk"
)

func TestGetIssueUsesRawAPIKey(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINEAR_ACCESS_TOKEN", "")
	t.Setenv("TASKS_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials.toml"))

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !strings.Contains(req.Query, "issue(id:") {
			t.Fatalf("unexpected query: %s", req.Query)
		}
		fmt.Fprint(w, `{"data":{"issue":{"id":"uuid","identifier":"ENG-1","title":"Hi","description":"","url":"","priority":1,"sortOrder":10,"createdAt":"2026-10-20T00:00:00Z","updatedAt":"2026-10-20T00:00:00Z","state":{"id":"s","name":"Todo","type":"unstarted","position":1},"labels":{"nodes":[]},"comments":{"nodes":[]}}}}`)
	}))
	defer srv.Close()

	c := newClient(Config{Team: "ENG"})
	c.graphqlURL = srv.URL
	task, err := c.getIssue(context.Background(), "ENG-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "lin_api_test" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if task.Key != "ENG-1" || task.Status != "Todo" {
		t.Fatalf("task = %+v", task)
	}
}

func TestRefreshRotatesCredentials(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_ACCESS_TOKEN", "")
	path := filepath.Join(t.TempDir(), "credentials.toml")
	t.Setenv("TASKS_CREDENTIALS_FILE", path)
	if err := sdk.SaveCredential("linear", map[string]string{
		"auth_type":     "oauth",
		"access_token":  "old",
		"refresh_token": "refresh-old",
		"expires_at":    time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"client_id":     "client",
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-old" {
			t.Fatalf("unexpected form: %v", r.Form)
		}
		fmt.Fprint(w, `{"access_token":"new","refresh_token":"refresh-new","token_type":"Bearer","scope":"read write","expires_in":86399}`)
	}))
	defer srv.Close()

	c := newClient(Config{Team: "ENG"})
	c.tokenURL = srv.URL
	header, err := c.authHeader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if header != "Bearer new" {
		t.Fatalf("header = %q", header)
	}
	creds, err := sdk.LoadCredential("linear")
	if err != nil {
		t.Fatal(err)
	}
	if creds["access_token"] != "new" || creds["refresh_token"] != "refresh-new" {
		t.Fatalf("credentials not rotated: %v", creds)
	}
}
