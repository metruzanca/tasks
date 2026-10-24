package linear

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/metruzanca/tasks/pkg/sdk"
)

func TestLoginAPIKeyFallback(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_ACCESS_TOKEN", "")
	t.Setenv("TASKS_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials.toml"))
	b, err := New(Config{Team: "ENG", OAuthClientID: "off"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "lin_api_key" {
			t.Fatalf("authorization = %q", got)
		}
		fmt.Fprint(w, `{"data":{"viewer":{"id":"u","name":"Ada","displayName":"ada","email":"ada@example.com"}}}`)
	}))
	defer srv.Close()
	b.client.graphqlURL = srv.URL

	first, err := b.Login(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.GetDone() || first.GetPrompt() == "" {
		t.Fatalf("first response = %+v", first)
	}
	done, err := b.Login(context.Background(), "lin_api_key", true)
	if err != nil {
		t.Fatal(err)
	}
	if !done.GetDone() {
		t.Fatalf("login did not finish: %+v", done)
	}
	creds, err := sdk.LoadCredential("linear")
	if err != nil {
		t.Fatal(err)
	}
	if creds["auth_type"] != "api_key" || creds["token"] != "lin_api_key" {
		t.Fatalf("credentials = %v", creds)
	}
}
