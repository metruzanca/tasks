package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupDirs(t *testing.T) (configHome, dataHome string) {
	t.Helper()
	root := t.TempDir()
	configHome = filepath.Join(root, "config")
	dataHome = filepath.Join(root, "data")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("TASKS_BACKEND", "")
	t.Setenv("TASKS_PLUGIN_DIR", "")
	t.Setenv("TASKS_SQLITE_PATH", "")
	t.Setenv("TASKS_READY_STATUS", "")
	return configHome, dataHome
}

func writeGlobal(t *testing.T, configHome, body string) {
	t.Helper()
	dir := filepath.Join(configHome, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRepoGetsOwnBoardEvenWithGlobalPath(t *testing.T) {
	configHome, _ := setupDirs(t)
	shared := filepath.Join(t.TempDir(), "global.db")
	writeGlobal(t, configHome, "[sqlite]\npath = \""+shared+"\"\n")

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(repo, ".tasks", "tasks.db")
	if s.Config.SQLite.Path != want {
		t.Fatalf("path = %q, want %q", s.Config.SQLite.Path, want)
	}
	if s.RepoRoot != repo {
		t.Fatalf("repo root = %q, want %q", s.RepoRoot, repo)
	}
}

func TestRepoCanShareABoardExplicitly(t *testing.T) {
	setupDirs(t)
	shared := filepath.Join(t.TempDir(), "shared.db")
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "backend = \"sqlite\"\n[sqlite]\npath = \"" + shared + "\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".tasks.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Config.SQLite.Path != shared {
		t.Fatalf("path = %q, want %q", s.Config.SQLite.Path, shared)
	}
}

func TestGlobalPathOutsideRepo(t *testing.T) {
	configHome, dataHome := setupDirs(t)
	globalDB := filepath.Join(t.TempDir(), "global.db")
	writeGlobal(t, configHome, "[sqlite]\npath = \""+globalDB+"\"\n")

	dir := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Config.SQLite.Path != globalDB {
		t.Fatalf("path = %q, want %q", s.Config.SQLite.Path, globalDB)
	}
	if s.RepoRoot != "" {
		t.Fatalf("unexpected repo root %q", s.RepoRoot)
	}

	// Without a global path, the data directory is used.
	if err := os.Remove(filepath.Join(configHome, "tasks", "config.toml")); err != nil {
		t.Fatal(err)
	}
	s, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataHome, "tasks", "tasks.db")
	if s.Config.SQLite.Path != want {
		t.Fatalf("path = %q, want %q", s.Config.SQLite.Path, want)
	}
}

func TestLinearSection(t *testing.T) {
	configHome, _ := setupDirs(t)
	writeGlobal(t, configHome, `backend = "linear"
[linear]
team = "ENG"
oauth_client_id = "client"
ready_status = "Todo"
`)
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Config.Linear.Team != "ENG" || s.Config.Linear.OAuthClientID != "client" {
		t.Fatalf("linear config = %+v", s.Config.Linear)
	}
	var section map[string]string
	if err := json.Unmarshal([]byte(s.Section("linear")), &section); err != nil {
		t.Fatal(err)
	}
	if section["team"] != "ENG" || section["oauth_client_id"] != "client" {
		t.Fatalf("section = %v", section)
	}
}

func TestLinearRepoWriteHasNoToken(t *testing.T) {
	setupDirs(t)
	s := &Settings{
		Config: Config{
			Backend: "linear",
			Linear:  Linear{Team: "ENG", OAuthClientID: "client"},
		},
	}
	path := filepath.Join(t.TempDir(), ".tasks.toml")
	if err := s.Write(path, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "team = \"ENG\"") {
		t.Fatalf("missing team:\n%s", body)
	}
	if strings.Contains(body, "access_token") || strings.Contains(body, "refresh_token") || strings.Contains(body, "token =") {
		t.Fatalf("repo config contains a token:\n%s", body)
	}
}
