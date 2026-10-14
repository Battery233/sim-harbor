package config

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func managedFixture(t *testing.T) (Config, *Settings) {
	t.Helper()
	c := valid()
	c.DataDir = t.TempDir()
	c.StaticDir = "web/dist"
	c.BarkBaseURL = "https://bark.example.invalid"
	c.PublicURL = "https://example.invalid"
	c.AdminToken = strings.Repeat("S", 32)
	c.AESMode = 1
	// Both synthetic keys contain sixteen bytes.
	c.AESKey = "0123456789abcdef"
	c.AESIV = "fedcba9876543210"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSettings(c)
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}
func TestUnifiedSettingsSecretsPriorityAndRedaction(t *testing.T) {
	c, s := managedFixture(t)
	t.Setenv("SIM_TEST_TOKEN", "WRONG-ENV-TOKEN")
	p := filepath.Join(c.DataDir, "system.private.json")
	loaded, err := Load(p)
	if err != nil || loaded.AdminToken != c.AdminToken || loaded.AESKey != c.AESKey {
		t.Fatal("unified values lost", err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(snap)
	for _, secret := range []string{c.AdminToken, c.AESKey, c.AESIV} {
		if strings.Contains(string(data), secret) {
			t.Fatal("secret returned")
		}
	}
	if !snap.Configured["admin_token"] || snap.Pending {
		t.Fatal("flags", snap.Pending)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal("permissions", info.Mode())
	}
	loaded.AdminTokenEnv = "SIM_TEST_TOKEN"
	if err = writeSettings(p, loaded, snap.Revision); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(p)
	if err != nil || loaded.AdminToken != c.AdminToken {
		t.Fatal("environment overrode unified credentials", err)
	}
}
func TestSettingsSavePreservesRuntimeAndClearsBarkExplicitly(t *testing.T) {
	c, s := managedFixture(t)
	a, _ := s.Snapshot()
	a.Values.LogLevel = "debug"
	b, err := s.Save(a.Values, SecretChanges{BarkKey: "SYNTHETIC-BARK"}, a.Revision)
	if err != nil || !b.Pending || !b.Configured["bark_key"] || s.Active().LogLevel != c.LogLevel || s.Active().BarkKey != "" {
		t.Fatal("save modified active runtime", err)
	}
	keep, err := s.Save(b.Values, SecretChanges{}, b.Revision)
	if err != nil || !keep.Configured["bark_key"] {
		t.Fatal("blank cleared credential", err)
	}
	cleared, err := s.Save(keep.Values, SecretChanges{ClearBark: true}, keep.Revision)
	if err != nil || cleared.Configured["bark_key"] {
		t.Fatal("explicit clear failed", err)
	}
	if err = s.CheckRevision(a.Revision); err != ErrConflict {
		t.Fatal("stale apply accepted", err)
	}
	if err = s.CheckRevision(cleared.Revision); err != nil {
		t.Fatal(err)
	}
}
func TestSettingsConcurrentRevisionAndInvalidWritesLeaveFileUntouched(t *testing.T) {
	c, s := managedFixture(t)
	snap, _ := s.Snapshot()
	bad := snap.Values
	bad.DataDir = filepath.Join(c.DataDir, "other")
	if _, err := s.Save(bad, SecretChanges{}, snap.Revision); err != ErrSettingsInvalid {
		t.Fatal(err)
	}
	bad = snap.Values
	bad.AESMode = 2
	if _, err := s.Save(bad, SecretChanges{AESKey: "short"}, snap.Revision); err != ErrSettingsInvalid {
		t.Fatal(err)
	}
	bad = snap.Values
	bad.StaticDir = c.DataDir
	if _, err := s.Save(bad, SecretChanges{}, snap.Revision); err != ErrSettingsInvalid {
		t.Fatal("private directory could be served", err)
	}
	if _, err := s.Save(snap.Values, SecretChanges{AdminToken: "short"}, snap.Revision); err != ErrSettingsInvalid {
		t.Fatal(err)
	}
	latest, _ := s.Snapshot()
	if latest.Revision != snap.Revision {
		t.Fatal("invalid request changed file")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, level := range []string{"warn", "debug"} {
		wg.Add(1)
		go func(level string) {
			defer wg.Done()
			v := snap.Values
			v.LogLevel = level
			_, err := s.Save(v, SecretChanges{}, snap.Revision)
			results <- err
		}(level)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if err == ErrConflict {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal("lost update", ok, conflict)
	}
}
func TestSettingsSymlinkAndPublicCredentialFilesRejected(t *testing.T) {
	c, s := managedFixture(t)
	p := filepath.Join(c.DataDir, "system.private.json")
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("public plaintext secret file accepted")
	}
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("public settings file accepted")
	}
	os.Chmod(p, 0600)
	other := filepath.Join(c.DataDir, "other.private.json")
	os.Rename(p, other)
	os.Symlink(other, p)
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("symlink followed")
	}
}
func TestDeviceAllowlistToggleKeepsCipherAndIdentityRequirements(t *testing.T) {
	c, _ := managedFixture(t)
	disabled := false
	c.DeviceAllowlistEnabled = &disabled
	if err := c.Validate(); err != nil || !c.Allows(&net.TCPAddr{IP: net.ParseIP("203.0.113.8"), Port: 123}) {
		t.Fatal("disabled list still rejects", err)
	}
	enabled := true
	c.DeviceAllowlistEnabled = &enabled
	c.Validate()
	if c.Allows(&net.TCPAddr{IP: net.ParseIP("203.0.113.8"), Port: 123}) {
		t.Fatal("enable lost rules")
	}
	c.DeviceAllowlistEnabled = &disabled
	c.AESMode = 0
	if c.Validate() == nil {
		t.Fatal("unrestricted plaintext accepted")
	}
}
func TestManagedBootstrapOverridePersistsWithoutEditingSeed(t *testing.T) {
	c, s := managedFixture(t)
	seed := filepath.Join(t.TempDir(), "bootstrap.json")
	initial, _ := json.Marshal(unified(c))
	os.WriteFile(seed, initial, 0600)
	snap, _ := s.Snapshot()
	snap.Values.LogLevel = "warn"
	if _, err := s.Save(snap.Values, SecretChanges{}, snap.Revision); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManaged(seed)
	if err != nil || loaded.LogLevel != "warn" {
		t.Fatal("override missing", err)
	}
	after, _ := os.ReadFile(seed)
	if string(after) != string(initial) {
		t.Fatal("read-only seed changed")
	}
	next, err := OpenSettings(loaded)
	if err != nil {
		t.Fatal(err)
	}
	applied, _ := next.Snapshot()
	if applied.Pending {
		t.Fatal("restart did not apply settings")
	}
}
