package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminTokenHeaderDefaultsAndValidation(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", "Authorization"}, {"   ", "Authorization"},
		{"authorization", "Authorization"},
		{" X-SIMHarbor-Authorization ", "X-Simharbor-Authorization"},
		{"Management-Token", "Management-Token"},
	} {
		c := valid()
		c.AdminTokenHeader = tc.input
		if err := c.Validate(); err != nil || c.AdminTokenHeaderName() != tc.want {
			t.Fatal("valid header rejected or normalized incorrectly", err)
		}
	}
	for _, name := range []string{
		"X-Token\r\nInjected: value", "Authorization\n", "X Token", "X:Token", "卡名", strings.Repeat("X", 129),
		"Host", "Cookie", "Origin", "Content-Type", "Content-Length", "Proxy-Authorization", "Sec-Fetch-Site", "Access-Control-Request-Headers", "Accept", "Referer",
	} {
		c := valid()
		c.AdminTokenHeader = name
		if c.Validate() == nil {
			t.Fatal("invalid or browser-reserved authentication header accepted")
		}
	}
}

func TestAdminTokenHeaderSavedPendingRestartAndResetToDefault(t *testing.T) {
	c, settings := managedFixture(t)
	snap, err := settings.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snap.Values.AdminTokenHeader = "X-SIMHarbor-Authorization"
	saved, err := settings.Save(snap.Values, SecretChanges{}, snap.Revision)
	if err != nil || !saved.Pending || settings.Active().AdminTokenHeaderName() != "Authorization" {
		t.Fatal("save mutated active authentication", err)
	}
	seed := filepath.Join(t.TempDir(), "seed.json")
	seedJSON, _ := json.Marshal(map[string]string{"data_directory": c.DataDir})
	if err := os.WriteFile(seed, seedJSON, 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := LoadManaged(seed)
	if err != nil || restarted.AdminTokenHeaderName() != "X-Simharbor-Authorization" || restarted.AdminToken != c.AdminToken {
		t.Fatal("managed restart lost custom header or token", err)
	}
	active, err := OpenSettings(restarted)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := active.Snapshot()
	bad := current.Values
	bad.AdminTokenHeader = "Cookie"
	if _, err := active.Save(bad, SecretChanges{}, current.Revision); err != ErrSettingsInvalid {
		t.Fatal("invalid header saved", err)
	}
	after, _ := active.Snapshot()
	if after.Revision != current.Revision {
		t.Fatal("rejected header changed settings")
	}
	current.Values.AdminTokenHeader = ""
	if _, err := active.Save(current.Values, SecretChanges{}, current.Revision); err != nil {
		t.Fatal(err)
	}
	restarted, err = LoadManaged(seed)
	if err != nil || restarted.AdminTokenHeaderName() != "Authorization" || restarted.AdminToken != c.AdminToken {
		t.Fatal("blank did not restore default header", err)
	}
	persisted, err := os.ReadFile(filepath.Join(c.DataDir, "system.private.json"))
	if err != nil || strings.Contains(string(persisted), `"admin_token_header"`) {
		t.Fatal("default header unnecessarily persisted a new configuration field", err)
	}
}
