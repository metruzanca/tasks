package pluginmgr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/go-plugin"
	"github.com/metruzanca/tasks/internal/config"
	"github.com/metruzanca/tasks/pkg/sdk"
)

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Manager starts and installs backend plugins.
type Manager struct {
	Settings *config.Settings
	Logger   *pluginLogger
}

// Session owns a running plugin process.
type Session struct {
	Backend     sdk.Backend
	client      *plugin.Client
	rpcClient   plugin.ClientProtocol
	backendName string
}

// Close terminates the plugin process.
func (s *Session) Close() {
	if s != nil && s.client != nil {
		s.client.Kill()
	}
}

// New creates a plugin manager.
func New(settings *config.Settings, verbose bool) *Manager {
	return &Manager{Settings: settings, Logger: newPluginLogger(verbose)}
}

// Open starts the configured backend and returns a session.
func (m *Manager) Open(ctx context.Context, name string) (*Session, error) {
	if name == "" {
		name = m.Settings.Config.Backend
	}
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("invalid backend name %q", name)
	}
	cmd, err := m.command(name)
	if err != nil {
		return nil, err
	}
	cfgJSON := m.Settings.Section(name)
	cmd.Env = append(os.Environ(),
		sdk.PluginConfigEnv+"="+cfgJSON,
		sdk.PluginNameEnv+"="+name,
		sdk.CredentialsFileEnv+"="+m.Settings.CredentialsPath,
	)

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: sdk.Handshake,
		Plugins: map[string]plugin.Plugin{
			sdk.PluginName:     &sdk.Plugin{},
			sdk.AuthPluginName: &sdk.AuthPlugin{},
		},
		Cmd:              cmd,
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Managed:          true,
		Logger:           m.Logger.Logger,
		StartTimeout:     30 * time.Second,
	})
	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("start %s backend: %w", name, err)
	}
	raw, err := rpcClient.Dispense(sdk.PluginName)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("connect %s backend: %w", name, err)
	}
	backend, ok := raw.(sdk.Backend)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("backend %s has an incompatible plugin protocol", name)
	}
	return &Session{Backend: backend, client: client, rpcClient: rpcClient, backendName: name}, nil
}

// Authenticator returns the optional interactive login service.
func (s *Session) Authenticator() (sdk.Authenticator, error) {
	if s == nil || s.rpcClient == nil {
		return nil, fmt.Errorf("plugin is not running")
	}
	raw, err := s.rpcClient.Dispense(sdk.AuthPluginName)
	if err != nil {
		return nil, fmt.Errorf("backend %q does not support login", s.backendName)
	}
	auth, ok := raw.(sdk.Authenticator)
	if !ok {
		return nil, fmt.Errorf("backend %q has an incompatible login protocol", s.backendName)
	}
	return auth, nil
}

func (m *Manager) command(name string) (*exec.Cmd, error) {
	if name == "sqlite" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		return exec.Command(exe, "__plugin", "sqlite"), nil
	}
	path, err := m.Find(name)
	if err != nil {
		return nil, err
	}
	return exec.Command(path), nil
}

// Find locates an installed plugin binary.
func (m *Manager) Find(name string) (string, error) {
	if !validName.MatchString(name) {
		return "", fmt.Errorf("invalid backend name %q", name)
	}
	bin := "tasks-plugin-" + name
	exe := bin
	if filepath.Separator == '\\' {
		exe = bin + ".exe"
	}
	var candidates []string
	if m.Settings.Config.PluginDir != "" {
		candidates = append(candidates, filepath.Join(m.Settings.Config.PluginDir, exe))
	}
	if dd, err := config.DataDir(); err == nil {
		candidates = append(candidates, filepath.Join(dd, "plugins", exe))
	}
	if self, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(self), exe))
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath(bin); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("backend %q is not installed: run `tasks plugin install %s`", name, name)
}

// Plugin describes an installed plugin.
type Plugin struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// List returns installed external plugins.
func (m *Manager) List() ([]Plugin, error) {
	seen := map[string]string{}
	dirs := []string{m.Settings.Config.PluginDir}
	if dd, err := config.DataDir(); err == nil {
		dirs = append(dirs, filepath.Join(dd, "plugins"))
	}
	if self, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(self))
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasPrefix(n, "tasks-plugin-") {
				continue
			}
			n = strings.TrimPrefix(n, "tasks-plugin-")
			n = strings.TrimSuffix(n, ".exe")
			if validName.MatchString(n) {
				seen[n] = filepath.Join(dir, e.Name())
			}
		}
	}
	out := make([]Plugin, 0, len(seen))
	for n, p := range seen {
		out = append(out, Plugin{Name: n, Path: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Install builds and installs a Go plugin module into the plugin directory.
func (m *Manager) Install(ctx context.Context, module, name, version string) (string, error) {
	if name == "" {
		parts := strings.Split(strings.TrimSuffix(module, "/"), "/")
		base := parts[len(parts)-1]
		name = strings.TrimPrefix(base, "tasks-plugin-")
	}
	if !validName.MatchString(name) {
		return "", fmt.Errorf("invalid plugin name %q (use --name)", name)
	}
	if module == "" {
		return "", fmt.Errorf("plugin module path is required")
	}
	if version == "" {
		version = "latest"
	}
	spec := module
	if !strings.Contains(spec, "@") {
		spec += "@" + version
	}
	if err := os.MkdirAll(m.Settings.Config.PluginDir, 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "go", "install", spec)
	cmd.Env = append(os.Environ(), "GOBIN="+m.Settings.Config.PluginDir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go install %s: %w", spec, err)
	}
	path, err := m.Find(name)
	if err != nil {
		return "", fmt.Errorf("installed %s but could not find %s: %w", spec, name, err)
	}
	if err := m.Probe(ctx, name); err != nil {
		return path, fmt.Errorf("installed plugin failed its startup check: %w", err)
	}
	return path, nil
}

// Remove deletes an installed external plugin.
func (m *Manager) Remove(name string) error {
	if name == "sqlite" {
		return fmt.Errorf("sqlite is built in and cannot be removed")
	}
	path, err := m.Find(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// Probe starts a plugin and calls Health.
func (m *Manager) Probe(ctx context.Context, name string) error {
	s, err := m.Open(ctx, name)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return s.Backend.Health(ctx)
}

// OfficialModule returns the module path for a bundled-official external plugin.
func OfficialModule(name string) string {
	return "github.com/metruzanca/tasks/cmd/tasks-plugin-" + name
}
