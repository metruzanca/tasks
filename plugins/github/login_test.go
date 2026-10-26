package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metruzanca/tasks/pkg/sdk"
)

func TestLoginUsesExistingToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("TASKS_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials.toml"))
	if err := sdk.SaveCredential("github", map[string]string{
		"auth_type": "token",
		"token":     "existing",
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer existing" {
			t.Fatalf("authorization = %q", got)
		}
		fmt.Fprint(w, `{"data":{"viewer":{"login":"octocat"}}}`)
	}))
	defer srv.Close()

	b, err := New(Config{Owner: "octo", Project: 1, Repo: "octo/repo"})
	if err != nil {
		t.Fatal(err)
	}
	b.client.graphqlURL = srv.URL
	resp, err := b.Login(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetDone() || !strings.Contains(resp.GetMessage(), "Already logged in") {
		t.Fatalf("response = %+v", resp)
	}
}
