package sdk

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// CredentialsFileName is the file that stores backend tokens separately from
// configuration so that config can be committed safely.
const CredentialsFileName = "credentials.toml"

// CredentialsPath returns the path to the shared credentials file.
func CredentialsPath() (string, error) {
	if p := os.Getenv(CredentialsFileEnv); p != "" {
		return expandHome(p), nil
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "tasks", CredentialsFileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "tasks", CredentialsFileName), nil
}

// LoadCredential returns the credential values for a backend. A missing file or
// section is not an error.
func LoadCredential(name string) (map[string]string, error) {
	path, err := CredentialsPath()
	if err != nil {
		return nil, err
	}
	all, err := readCredentials(path)
	if err != nil {
		return nil, err
	}
	values := all[name]
	if values == nil {
		values = map[string]string{}
	}
	return values, nil
}

// SaveCredential replaces the credential values for a backend.
func SaveCredential(name string, values map[string]string) error {
	path, err := CredentialsPath()
	if err != nil {
		return err
	}
	return updateCredentials(path, func(all map[string]map[string]string) error {
		all[name] = values
		return nil
	})
}

// UpdateCredential mutates a backend's credential values while holding the
// credentials lock. This avoids losing a rotated refresh token.
func UpdateCredential(name string, mutate func(map[string]string) error) error {
	path, err := CredentialsPath()
	if err != nil {
		return err
	}
	return updateCredentials(path, func(all map[string]map[string]string) error {
		values := all[name]
		if values == nil {
			values = map[string]string{}
		}
		if err := mutate(values); err != nil {
			return err
		}
		all[name] = values
		return nil
	})
}

// DeleteCredential removes a backend from the credentials file.
func DeleteCredential(name string) error {
	path, err := CredentialsPath()
	if err != nil {
		return err
	}
	return updateCredentials(path, func(all map[string]map[string]string) error {
		delete(all, name)
		return nil
	})
}

func readCredentials(path string) (map[string]map[string]string, error) {
	all := map[string]map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	if err := toml.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return all, nil
}

func updateCredentials(path string, mutate func(map[string]map[string]string) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := lockCredentials(path)
	if err != nil {
		return err
	}
	defer unlock()

	all, err := readCredentials(path)
	if err != nil {
		return err
	}
	if err := mutate(all); err != nil {
		return err
	}
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(all); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*.toml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(buf.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func lockCredentials(path string) (func(), error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if fi, statErr := os.Stat(lockPath); statErr == nil && time.Since(fi.ModTime()) > 30*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("credentials are locked by %s", lockPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}
