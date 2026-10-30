package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrConflict = errors.New("settings revision changed")
var ErrSettingsInvalid = errors.New("settings invalid")

type SecretChanges struct {
	AESKey     string `json:"aes_key"`
	AESIV      string `json:"aes_iv"`
	AdminToken string `json:"admin_token"`
	BarkKey    string `json:"bark_key"`
	ClearBark  bool   `json:"clear_bark"`
}
type SettingsSnapshot struct {
	Values             Config             `json:"values"`
	Configured         map[string]bool    `json:"credentials_configured"`
	Revision           string             `json:"revision"`
	Pending            bool               `json:"pending_restart"`
	VoicemailReception VoicemailReception `json:"voicemail_reception"`
}
type VoicemailReception struct {
	Mode             string   `json:"mode"`
	Ready            bool     `json:"ready"`
	AllowlistEnabled bool     `json:"allowlist_enabled"`
	DeviceCIDRs      []string `json:"device_cidrs"`
}
type Settings struct {
	mu             sync.Mutex
	path           string
	active         Config
	activeRevision string
}

// A private persistent override lets read-only container bootstrap mounts keep
// working. The data directory remains pinned; UI edits cannot switch databases.
func LoadManaged(seed string) (Config, error) {
	c, err := Load(seed)
	if err != nil {
		return c, err
	}
	path := filepath.Join(c.DataDir, "system.private.json")
	if _, err = os.Lstat(path); os.IsNotExist(err) {
		return c, nil
	} else if err != nil {
		return c, err
	}
	if _, err = readSettings(path); err != nil {
		return c, err
	}
	next, err := Load(path)
	if err != nil {
		return c, err
	}
	if !samePath(next.DataDir, c.DataDir) {
		return c, ErrSettingsInvalid
	}
	return next, nil
}

func OpenSettings(c Config) (*Settings, error) {
	path, err := filepath.Abs(filepath.Join(c.DataDir, "system.private.json"))
	if err != nil {
		return nil, err
	}
	s := &Settings{path: path, active: c}
	if _, err = os.Lstat(path); os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		if err = writeSettings(path, unified(c), ""); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	p, err := readSettings(path)
	if err != nil {
		return nil, err
	}
	stored, err := Load(path)
	if err != nil {
		return nil, err
	}
	if !samePath(stored.DataDir, c.DataDir) {
		return nil, ErrSettingsInvalid
	}
	s.active = stored
	s.activeRevision = revision(p)
	return s, nil
}
func (s *Settings) Active() Config { return s.active }
func (s *Settings) Snapshot() (SettingsSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}
func (s *Settings) snapshot() (SettingsSnapshot, error) {
	p, err := readSettings(s.path)
	if err != nil {
		return SettingsSnapshot{}, err
	}
	c, err := Load(s.path)
	if err != nil {
		return SettingsSnapshot{}, err
	}
	if !samePath(c.DataDir, s.active.DataDir) {
		return SettingsSnapshot{}, ErrSettingsInvalid
	}
	configured := map[string]bool{"aes_key": c.AESKey != "", "aes_iv": c.AESIV != "", "admin_token": c.AdminToken != "", "bark_key": c.BarkKey != ""}
	c.Credentials = nil
	return SettingsSnapshot{Values: c, Configured: configured, Revision: revision(p), Pending: revision(p) != s.activeRevision,
		VoicemailReception: VoicemailReception{Mode: s.active.VoicemailMode(), Ready: s.active.VoicemailReceptionReady(), AllowlistEnabled: s.active.AllowlistEnabled(), DeviceCIDRs: append([]string{}, s.active.DeviceCIDRs...)}}, nil
}
func (s *Settings) Save(values Config, secrets SecretChanges, expected string) (SettingsSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := readSettings(s.path)
	if err != nil {
		return SettingsSnapshot{}, err
	}
	if expected == "" || revision(p) != expected {
		return SettingsSnapshot{}, ErrConflict
	}
	current, err := Load(s.path)
	if err != nil {
		return SettingsSnapshot{}, err
	}
	if values.Credentials != nil || !samePath(values.DataDir, s.active.DataDir) || !samePath(values.StaticDir, s.active.StaticDir) || values.Environment != s.active.Environment {
		return SettingsSnapshot{}, ErrSettingsInvalid
	}
	if secrets.ClearBark && secrets.BarkKey != "" {
		return SettingsSnapshot{}, ErrSettingsInvalid
	}
	values.AESKey, values.AESIV, values.AdminToken, values.BarkKey = current.AESKey, current.AESIV, current.AdminToken, current.BarkKey
	for _, pair := range []struct {
		src string
		dst *string
	}{{secrets.AESKey, &values.AESKey}, {secrets.AESIV, &values.AESIV}, {secrets.AdminToken, &values.AdminToken}, {secrets.BarkKey, &values.BarkKey}} {
		if pair.src != "" {
			*pair.dst = pair.src
		}
	}
	if secrets.ClearBark {
		values.BarkKey = ""
	}
	for _, v := range []string{values.AESKey, values.AESIV, values.AdminToken, values.BarkKey} {
		if len(v) > 512 || strings.ContainsAny(v, "\x00\r\n") {
			return SettingsSnapshot{}, ErrSettingsInvalid
		}
	}
	if ((!values.SkipAdminToken || values.AdminToken != "") && len(values.AdminToken) < 24) || values.Validate() != nil {
		return SettingsSnapshot{}, ErrSettingsInvalid
	}
	if err = writeSettings(s.path, unified(values), expected); err != nil {
		return SettingsSnapshot{}, err
	}
	return s.snapshot()
}
func (s *Settings) CheckRevision(expected string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := s.snapshot()
	if err != nil {
		return err
	}
	if expected == "" || snap.Revision != expected {
		return ErrConflict
	}
	return nil
}
func unified(c Config) Config {
	c.Credentials = &Credentials{c.AESKey, c.AESIV, c.AdminToken, c.BarkKey}
	c.AESKeyEnv, c.AESIVEnv, c.AdminTokenEnv, c.BarkKeyEnv = "", "", "", ""
	return c
}
func samePath(a, b string) bool {
	aa, e := filepath.Abs(a)
	bb, f := filepath.Abs(b)
	return e == nil && f == nil && aa == bb
}
func revision(p []byte) string { h := sha256.Sum256(p); return hex.EncodeToString(h[:]) }
func readSettings(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return nil, ErrSettingsInvalid
	}
	return os.ReadFile(path)
}
func writeSettings(path string, c Config, expected string) error {
	p, err := json.MarshalIndent(c, "", "  ")
	if err != nil || len(p) > 65535 {
		return ErrSettingsInvalid
	}
	p = append(p, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".system-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if _, err = temp.Write(p); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	old, err := readSettings(path)
	if expected == "" {
		if !os.IsNotExist(err) {
			return ErrConflict
		}
	} else if err != nil || revision(old) != expected {
		return ErrConflict
	}
	if err = os.Rename(temp.Name(), path); err != nil {
		return err
	}
	// Persist the directory entry when supported. Never echo raw filesystem errors.
	if dir, e := os.Open(filepath.Dir(path)); e == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
