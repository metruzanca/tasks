package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type SQLite struct {
	Path        string `toml:"path" json:"path"`
	Me          string `toml:"me" json:"me"`
	ReadyStatus string `toml:"ready_status" json:"ready_status"`
}

type GitHub struct {
	Owner         string `toml:"owner" json:"owner"`
	Project       int    `toml:"project" json:"project"`
	Repo          string `toml:"repo" json:"repo"`
	StatusField   string `toml:"status_field" json:"status_field"`
	PriorityField string `toml:"priority_field" json:"priority_field"`
	TokenEnv      string `toml:"token_env" json:"token_env"`
}

type Config struct {
	Backend   string `toml:"backend"`
	PluginDir string `toml:"plugin_dir"`
	SQLite    SQLite `toml:"sqlite"`
	GitHub    GitHub `toml:"github"`

	Raw map[string]any `toml:"-"`
}

// Settings is a loaded configuration plus its resolved paths.
type Settings struct {
	Config     Config
	GlobalPath string
	RepoPath   string
	DataDir    string
	ConfigDir  string
}

// GlobalPath returns the user-level configuration path.
func GlobalPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tasks", "config.toml"), nil
}

// DataDir returns the user-level data directory.
func DataDir() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "tasks"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "tasks"), nil
}

// Load reads the global configuration and the nearest repo override.
func Load() (*Settings, error) {
	gp, err := GlobalPath()
	if err != nil {
		return nil, err
	}
	dd, err := DataDir()
	if err != nil {
		return nil, err
	}
	s := &Settings{
		Config:     Config{Raw: map[string]any{}},
		GlobalPath: gp,
		DataDir:    dd,
		ConfigDir:  filepath.Dir(gp),
	}
	if _, err := os.Stat(gp); err == nil {
		if err := decodeInto(gp, &s.Config); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	repo, err := FindRepoConfig()
	if err != nil {
		return nil, err
	}
	s.RepoPath = repo
	if repo != "" {
		if err := decodeInto(repo, &s.Config); err != nil {
			return nil, err
		}
	}

	if v := os.Getenv("TASKS_BACKEND"); v != "" {
		s.Config.Backend = v
	}
	if v := os.Getenv("TASKS_PLUGIN_DIR"); v != "" {
		s.Config.PluginDir = v
	}
	if v := os.Getenv("TASKS_SQLITE_PATH"); v != "" {
		s.Config.SQLite.Path = v
	}
	if v := os.Getenv("TASKS_READY_STATUS"); v != "" {
		s.Config.SQLite.ReadyStatus = v
	}

	if s.Config.Backend == "" {
		s.Config.Backend = "sqlite"
	}
	if s.Config.PluginDir == "" {
		s.Config.PluginDir = filepath.Join(dd, "plugins")
	}
	if s.Config.SQLite.Path == "" {
		s.Config.SQLite.Path = filepath.Join(dd, "tasks.db")
	}
	s.Config.SQLite.Path = ExpandHome(s.Config.SQLite.Path)
	s.Config.PluginDir = ExpandHome(s.Config.PluginDir)
	if s.Config.SQLite.Me == "" {
		s.Config.SQLite.Me = os.Getenv("USER")
	}
	return s, nil
}

// FindRepoConfig walks up from the working directory looking for .tasks.toml.
func FindRepoConfig() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, ".tasks.toml")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func decodeInto(path string, c *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := toml.Decode(string(data), c); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if c.Raw == nil {
		c.Raw = map[string]any{}
	}
	mergeRaw(c.Raw, raw)
	return nil
}

func mergeRaw(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeRaw(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

// Section returns the backend-specific config section as JSON. Plugins use this
// to stay decoupled from the host's config schema.
func (s *Settings) Section(backend string) string {
	var v any
	switch backend {
	case "sqlite":
		v = s.Config.SQLite
	case "github":
		v = s.Config.GitHub
	default:
		v = s.Config.Raw[backend]
		if v == nil {
			v = map[string]any{}
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Write writes the configuration to path. If repo is true, only backend and the
// selected backend section are written.
func (s *Settings) Write(path string, repo bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if repo {
		doc := map[string]any{"backend": s.Config.Backend}
		switch s.Config.Backend {
		case "sqlite":
			doc["sqlite"] = s.Config.SQLite
		case "github":
			doc["github"] = s.Config.GitHub
		default:
			doc[s.Config.Backend] = s.Config.Raw[s.Config.Backend]
		}
		if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
			return err
		}
	} else {
		if err := toml.NewEncoder(&buf).Encode(s.Config); err != nil {
			return err
		}
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}
