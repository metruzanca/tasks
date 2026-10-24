package sdk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.toml")
	t.Setenv(CredentialsFileEnv, path)

	if err := SaveCredential("linear", map[string]string{"auth_type": "api_key", "token": "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredential("github", map[string]string{"auth_type": "token", "token": "gh"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCredential("linear")
	if err != nil {
		t.Fatal(err)
	}
	if got["token"] != "secret" {
		t.Fatalf("token = %q", got["token"])
	}
	if err := UpdateCredential("linear", func(v map[string]string) error {
		v["token"] = "rotated"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadCredential("linear")
	if got["token"] != "rotated" {
		t.Fatalf("token = %q", got["token"])
	}
	gh, _ := LoadCredential("github")
	if gh["token"] != "gh" {
		t.Fatalf("github token lost: %q", gh["token"])
	}
	if err := DeleteCredential("linear"); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadCredential("linear")
	if len(got) != 0 {
		t.Fatalf("expected empty credentials, got %v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "rotated") || !strings.Contains(string(data), "[github]") {
		t.Fatalf("unexpected credentials file:\n%s", data)
	}
}
