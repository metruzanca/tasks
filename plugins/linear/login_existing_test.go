package linear

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

func TestLoginUsesExistingCredential(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_ACCESS_TOKEN", "")
	t.Setenv("TASKS_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials.toml"))
	if err := sdk.SaveCredential("linear", map[string]string{
		"auth_type": "api_key",
		"token":     "lin_api_existing",
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "lin_api_existing" {
			t.Fatalf("authorization = %q", got)
		}
		fmt.Fprint(w, `{"data":{"viewer":{"id":"u","name":"Ada","displayName":"ada","email":"ada@example.com"}}}`)
	}))
	defer srv.Close()

	b, err := New(Config{Team: "ENG"})
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
