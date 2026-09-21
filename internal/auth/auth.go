// Package auth decides where the API credential comes from and where it is kept.
package auth

import (
	"errors"
	"net/url"
	"os"

	"github.com/getadaa/cli/internal/config"
	"github.com/zalando/go-keyring"
)

const keyringService = "adaa"

// Source names where a token was found, for `adaa whoami` and `adaa doctor`.
type Source string

const (
	SourceNone    Source = ""
	SourceEnv     Source = "ADAA_TOKEN"
	SourceKeyring Source = "keychain"
	SourceFile    Source = "config file"
)

// keyringUser scopes the stored token to the API it belongs to, so pointing the
// CLI at a local server never sends a production token there.
func keyringUser(apiURL string) string {
	if u, err := url.Parse(apiURL); err == nil && u.Host != "" {
		return u.Host
	}
	return apiURL
}

// Token finds the credential: the environment wins, then the keychain, then
// the config file fallback.
func Token(cfg *config.Config) (string, Source) {
	if t := os.Getenv("ADAA_TOKEN"); t != "" {
		return t, SourceEnv
	}
	if t, err := keyring.Get(keyringService, keyringUser(cfg.EffectiveAPIURL())); err == nil && t != "" {
		return t, SourceKeyring
	}
	if cfg.Token != "" {
		return cfg.Token, SourceFile
	}
	return "", SourceNone
}

// Save stores a token in the keychain, falling back to the config file when
// there is no keychain (a headless Linux box, a container).
func Save(cfg *config.Config, token string) (Source, error) {
	if err := keyring.Set(keyringService, keyringUser(cfg.EffectiveAPIURL()), token); err == nil {
		if cfg.Token != "" {
			cfg.Token = ""
			if err := cfg.Save(); err != nil {
				return SourceKeyring, err
			}
		}
		return SourceKeyring, nil
	}
	cfg.Token = token
	return SourceFile, cfg.Save()
}

// Clear removes the stored token from everywhere this CLI may have put it.
func Clear(cfg *config.Config) error {
	err := keyring.Delete(keyringService, keyringUser(cfg.EffectiveAPIURL()))
	if errors.Is(err, keyring.ErrNotFound) {
		err = nil
	}
	cfg.Token = ""
	cfg.OrganizationID = ""
	cfg.OrganizationName = ""
	cfg.Email = ""
	cfg.Name = ""
	if serr := cfg.Save(); serr != nil {
		return serr
	}
	return err
}

// KeyringAvailable reports whether the OS keychain can be used at all.
func KeyringAvailable() error {
	_, err := keyring.Get(keyringService, "__adaa_probe__")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
