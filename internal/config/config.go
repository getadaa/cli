// Package config reads and writes the CLI's small amount of local state.
//
// Everything here is plain JSON in one directory, so a person can read, back up
// or delete it without the CLI's help.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const DefaultAPIURL = "https://adaa.no/api/v1"

// Config is what a person chose. It never holds anything the API can tell us
// fresh, except the organization, which every command needs and which does not
// change for a given credential.
type Config struct {
	APIURL string `json:"api_url,omitempty"`
	// Token is only written here when no OS keychain is available.
	Token            string `json:"token,omitempty"`
	OrganizationID   string `json:"organization_id,omitempty"`
	OrganizationName string `json:"organization_name,omitempty"`
	Email            string `json:"email,omitempty"`
	Name             string `json:"name,omitempty"`
}

// State is disposable cache. Deleting it loses nothing that matters.
type State struct {
	UpdateCheckedAt time.Time `json:"update_checked_at,omitzero"`
	LatestVersion   string    `json:"latest_version,omitempty"`
}

// Dir is where config lives. ADAA_CONFIG_DIR wins, then XDG_CONFIG_HOME, then
// ~/.config/adaa on Unix, and %AppData%\adaa on Windows.
func Dir() (string, error) {
	if d := os.Getenv("ADAA_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "adaa"), nil
	}
	if runtime.GOOS == "windows" {
		d, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, "adaa"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "adaa"), nil
}

func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

func statePath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "state.json"), nil
}

// Load returns the saved config, or an empty one if nothing is saved yet.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := readJSON(p, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	return writeJSON(p, c)
}

// EffectiveAPIURL applies ADAA_API_URL over the saved value over the default.
func (c *Config) EffectiveAPIURL() string {
	if u := os.Getenv("ADAA_API_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	if c.APIURL != "" {
		return strings.TrimRight(c.APIURL, "/")
	}
	return DefaultAPIURL
}

func LoadState() *State {
	s := &State{}
	if p, err := statePath(); err == nil {
		_ = readJSON(p, s)
	}
	return s
}

func (s *State) Save() error {
	p, err := statePath()
	if err != nil {
		return err
	}
	return writeJSON(p, s)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return &CorruptError{Path: path, Err: err}
	}
	return nil
}

// writeJSON replaces the file atomically, so a crash never leaves half a
// config behind. The file may hold a token, so it is private to the user.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// CorruptError means the file exists but is not valid JSON.
type CorruptError struct {
	Path string
	Err  error
}

func (e *CorruptError) Error() string {
	return "config file " + e.Path + " is not valid JSON: " + e.Err.Error()
}

func (e *CorruptError) Unwrap() error { return e.Err }
