package config

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publicVoicemail(c *Config) {
	c.VoicemailUploadMode = "public"
	c.AdminToken = strings.Repeat("S", 32)
	c.VoicemailEnabled = true
	c.VoicemailSlots = []int{1}
	c.VoicemailTTS = "SYNTHETIC"
	c.VoicemailAMRURL = "https://upload.example.com/api/voicemail/amr/" + strings.Repeat("a", 32) + "?slot={{slot}}&start={{telStartTs}}&phone={{phNum|$urlEncode()}}"
}

func TestVoicemailPublicConfigurationAndRejectedURLs(t *testing.T) {
	c := valid()
	publicVoicemail(&c)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if token, ok := VoicemailUploadToken(c.VoicemailAMRURL, c.VoicemailMode()); !ok || token != strings.Repeat("a", 32) {
		t.Fatal("public token unavailable")
	}
	for _, bad := range []string{
		"http://upload.example.com", "https://127.0.0.1", "https://[::1]", "https://localhost", "https://a..example.com", "https://_bad.example.com", "https://user:pass@upload.example.com", "https://upload.example.com:", "https://upload.example.com:0", "https://upload.example.com:65536",
	} {
		next := c
		next.VoicemailAMRURL = bad + "/api/voicemail/amr/" + strings.Repeat("a", 32)
		if next.Validate() == nil {
			t.Fatal("accepted invalid public URL")
		}
	}
	for _, suffix := range []string{"/extra", "#fragment"} {
		next := c
		next.VoicemailAMRURL = strings.Split(c.VoicemailAMRURL, "?")[0] + suffix
		if next.Validate() == nil {
			t.Fatal("accepted invalid path")
		}
	}
	for _, mode := range []string{"lan", "UNKNOWN"} {
		next := c
		next.VoicemailUploadMode = mode
		if next.Validate() == nil {
			t.Fatal("accepted incompatible mode")
		}
	}
	next := c
	next.AdminToken = ""
	if next.Validate() == nil {
		t.Fatal("public mode without management auth")
	}
	if !c.AllowsVoicemailProxy(net.ParseIP("127.0.0.1")) || c.AllowsVoicemailProxy(net.ParseIP("203.0.113.1")) {
		t.Fatal("unsafe default proxy trust")
	}
	for _, proxy := range []string{"0.0.0.0/0", "127.0.0.0/8", "192.168.0.0/16", "203.0.113.1/32", "::/0", "::ffff:127.0.0.1/128"} {
		next := c
		next.VoicemailProxyCIDRs = []string{proxy}
		if next.Validate() == nil {
			t.Fatal("accepted unsafe proxy range")
		}
	}
	c.VoicemailProxyCIDRs = []string{"172.18.0.1/32"}
	if c.Validate() != nil || c.AllowsVoicemailProxy(net.ParseIP("127.0.0.1")) || !c.AllowsVoicemailProxy(net.ParseIP("172.18.0.1")) {
		t.Fatal("explicit proxy not exclusive")
	}
}

func TestVoicemailIngressBoundsAndLegacyEscaping(t *testing.T) {
	c := valid()
	c.VoicemailAMRURL = "http://192.168.31.10:18880/api/voicemail/amr/%61" + strings.Repeat("a", 31)
	if err := c.Validate(); err != nil {
		t.Fatal("legacy URL acceptance changed", err)
	}
	publicVoicemail(&c)
	for _, limit := range []int64{-1, 1, 65537} {
		next := c
		next.VoicemailMaxStorageMiB = limit
		if next.Validate() == nil {
			t.Fatal("invalid capacity accepted")
		}
	}
	c.VoicemailProxyCIDRs = make([]string, 17)
	for i := range c.VoicemailProxyCIDRs {
		c.VoicemailProxyCIDRs[i] = "127.0.0.1/32"
	}
	if c.Validate() == nil {
		t.Fatal("unbounded proxy list accepted")
	}
}

func TestVoicemailLegacySettingsKeepModeAndToken(t *testing.T) {
	c, s := managedFixture(t)
	c.VoicemailAMRURL = "http://192.168.31.10:18880/api/voicemail/amr/" + strings.Repeat("b", 32)
	snap, _ := s.Snapshot()
	c.StaticDir = snap.Values.StaticDir
	saved, err := s.Save(c, SecretChanges{}, snap.Revision)
	if err != nil || saved.Values.VoicemailMode() != "lan" || saved.Values.VoicemailAMRURL != c.VoicemailAMRURL {
		t.Fatal("legacy configuration changed", err)
	}
	p, err := os.ReadFile(filepath.Join(c.DataDir, "system.private.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"voicemail_upload_mode", "voicemail_proxy_cidrs", "voicemail_max_storage_mib"} {
		if strings.Contains(string(p), field) {
			t.Fatal("default field was injected", field)
		}
	}
	publicVoicemail(&saved.Values)
	updated, err := s.Save(saved.Values, SecretChanges{}, saved.Revision)
	if err != nil || !updated.Pending || updated.VoicemailReception.Mode != "lan" {
		t.Fatal("saved policy incorrectly became active", err)
	}
	loaded, err := Load(filepath.Join(c.DataDir, "system.private.json"))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSettings(loaded)
	if err != nil {
		t.Fatal(err)
	}
	active, err := reopened.Snapshot()
	if err != nil || active.Pending || active.VoicemailReception.Mode != "public" || !active.VoicemailReception.Ready {
		t.Fatal("public policy failed to persist", err)
	}
	encoded, _ := json.Marshal(active.VoicemailReception)
	if strings.Contains(string(encoded), strings.Repeat("a", 32)) {
		t.Fatal("active status leaked token")
	}
}
