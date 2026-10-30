package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipAdminTokenIsExplicitAndKeepsOtherChannelChecks(t *testing.T) {
	c, _ := managedFixture(t)
	c.Environment = "cloud"
	c.HTTPAddr = "0.0.0.0:8080"
	c.VoicemailUploadMode = "public"
	c.AdminToken = ""
	if c.SkipAdminToken || c.Validate() == nil {
		t.Fatal("management authentication became optional by default")
	}
	c.SkipAdminToken = true
	if err := c.Validate(); err != nil {
		t.Fatal("explicit external authentication rejected", err)
	}
	c.AESMode = 0
	if c.Validate() == nil {
		t.Fatal("skipping management token disabled device protection")
	}
}

func TestSkipAdminTokenSettingsRoundTripAndRetainedCredential(t *testing.T) {
	c, settings := managedFixture(t)
	snap, _ := settings.Snapshot()
	snap.Values.SkipAdminToken = true
	saved, err := settings.Save(snap.Values, SecretChanges{}, snap.Revision)
	if err != nil || !saved.Pending || !saved.Values.SkipAdminToken || settings.Active().SkipAdminToken {
		t.Fatal("save changed active token policy", err)
	}
	file := filepath.Join(c.DataDir, "system.private.json")
	loaded, err := Load(file)
	if err != nil || !loaded.SkipAdminToken || loaded.AdminToken != c.AdminToken {
		t.Fatal("saved policy lost token", err)
	}
	restarted, err := OpenSettings(loaded)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = restarted.Snapshot()
	snap.Values.SkipAdminToken = false
	if _, err := restarted.Save(snap.Values, SecretChanges{}, snap.Revision); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(file)
	persisted, readErr := os.ReadFile(file)
	if err != nil || readErr != nil || loaded.SkipAdminToken || loaded.AdminToken != c.AdminToken || strings.Contains(string(persisted), `"skip_admin_token"`) {
		t.Fatal("default policy failed to restore cleanly", err, readErr)
	}
}

func TestSkippingWithoutStoredTokenCanRestoreAuthenticationOnlyWithValidToken(t *testing.T) {
	c := valid()
	c.DataDir = t.TempDir()
	c.SkipAdminToken = true
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	settings, err := OpenSettings(c)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := settings.Snapshot()
	saved, err := settings.Save(snap.Values, SecretChanges{}, snap.Revision)
	if err != nil {
		t.Fatal("external authentication could not save settings without a token", err)
	}
	saved.Values.SkipAdminToken = false
	if _, err := settings.Save(saved.Values, SecretChanges{}, saved.Revision); err != ErrSettingsInvalid {
		t.Fatal("panel authentication restored without a usable token", err)
	}
	if _, err := settings.Save(saved.Values, SecretChanges{AdminToken: strings.Repeat("S", 32)}, saved.Revision); err != nil {
		t.Fatal("could not set token while restoring panel authentication", err)
	}
}
