package pluginmgr

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/metruzanca/tasks/internal/config"
)

func TestNativeAndExternalPlugins(t *testing.T) {
	pluginDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, name := range []string{"tasks-plugin-github", "tasks-plugin-foo"} {
		if err := os.WriteFile(filepath.Join(pluginDir, name), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := New(&config.Settings{Config: config.Config{PluginDir: pluginDir}}, false)

	native := m.Native()
	if len(native) != 3 {
		t.Fatalf("native = %v", native)
	}
	for _, p := range native {
		if !p.Native || p.Path != "built-in" {
			t.Fatalf("unexpected native plugin: %+v", p)
		}
	}

	external, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(external) != 1 || external[0].Name != "foo" || external[0].Native {
		t.Fatalf("external = %+v", external)
	}
	if err := m.Remove("github"); err == nil {
		t.Fatal("removing a native backend should fail")
	}
	if _, err := m.Install(t.Context(), "example.com/tasks-plugin-github", "github", ""); err == nil {
		t.Fatal("installing a native backend should fail")
	}
}
