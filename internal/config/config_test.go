package config

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func valid() Config {
	return Config{Environment: "local", HTTPAddr: "127.0.0.1:8080", TCPAddr: "127.0.0.1:9000", DataDir: "data/local", DeviceID: "SIM-TEST", DeviceCIDRs: []string{"127.0.0.1/32"}, Timezone: "Asia/Shanghai", StaleSeconds: 345}
}
func TestBoundaries(t *testing.T) {
	c := valid()
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if !c.Allows(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 123}) || c.Allows(&net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 123}) {
		t.Fatal("CIDR boundary")
	}
	cases := []func(*Config){func(c *Config) { c.HTTPAddr = "0.0.0.0:8080" }, func(c *Config) { c.TCPAddr = "127.0.0.1:65536" }, func(c *Config) { c.DeviceCIDRs = []string{"0.0.0.0/0"} }, func(c *Config) { c.Environment = "cloud"; c.AdminToken = strings.Repeat("t", 24) }, func(c *Config) { c.AESMode = 2; c.AESKey = "short" }, func(c *Config) { c.BarkBaseURL = "http://example.invalid" }}
	for i, mutate := range cases {
		c := valid()
		mutate(&c)
		if c.Validate() == nil {
			t.Fatalf("unsafe case %d accepted", i)
		}
	}
	c = valid()
	c.Environment = "cloud"
	c.AdminToken = strings.Repeat("t", 24)
	c.AESMode = 2
	c.AESKey = "0123456789abcdef"
	c.AESIV = "fedcba9876543210"
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
}

func TestLoggingDefaultsAndLimits(t *testing.T) {
	c := valid()
	if err := c.Validate(); err != nil || c.LogLevel != "info" || c.LogMaxBytes != 16777216 || c.LogBackups != 3 || c.DiagnosticsLimit != 5000 || c.LogRetentionDays != 7 {
		t.Fatal(c.LogLevel, err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.LogLevel = "trace" },
		func(c *Config) { c.LogMaxBytes = 1 },
		func(c *Config) { c.LogBackups = 6 },
		func(c *Config) { c.DiagnosticsLimit = 5001 },
		func(c *Config) { c.LogRetentionDays = -1 },
		func(c *Config) { c.LogRetentionDays = 31 },
	} {
		c := valid()
		change(&c)
		if c.Validate() == nil {
			t.Fatal("unbounded/invalid logging configuration accepted")
		}
	}
}

func TestCloudMode1AndExistingSafetyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"mode1", func(c *Config) {}, ""},
		{"mode2", func(c *Config) { c.AESMode = 2 }, ""},
		{"mode0", func(c *Config) { c.AESMode = 0 }, "cloud device channel requires AES mode 1 or 2"},
		{"missing_key", func(c *Config) { c.AESKey = "" }, "AES key and IV"},
		{"short_iv", func(c *Config) { c.AESIV = "short" }, "AES key and IV"},
		{"short_admin_token", func(c *Config) { c.AdminToken = "short" }, "access token"},
		{"missing_sources", func(c *Config) { c.DeviceCIDRs = nil }, "source CIDRs required"},
		{"unrestricted_source", func(c *Config) { c.DeviceCIDRs = []string{"0.0.0.0/0"} }, "restricted device CIDR required"},
		{"plaintext_bark", func(c *Config) { c.BarkBaseURL = "http://example.invalid" }, "URLs require HTTPS"},
		{"plaintext_public_url", func(c *Config) { c.PublicURL = "http://127.0.0.1:8080" }, "URLs require HTTPS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			c.Environment = "cloud"
			c.AdminToken = strings.Repeat("t", 24)
			c.AESMode = 1
			c.AESKey = "0123456789abcdef"
			c.AESIV = "fedcba9876543210"
			c.BarkBaseURL = "https://example.invalid"
			tc.change(&c)
			err := c.Validate()
			if tc.want == "" {
				if err != nil || c.OutboundEnabled {
					t.Fatal("cloud mode rejected or outbound enabled", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestLoadStrictAndSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("SIM_TEST_TOKEN", strings.Repeat("t", 24))
	for _, v := range []string{`{"unknown":1}`, `{} {}`, `{"http_address":"0.0.0.0:8080","admin_token_env":"SIM_TEST_TOKEN"}`} {
		if e := os.WriteFile(p, []byte(v), 0600); e != nil {
			t.Fatal(e)
		}
		c, e := Load(p)
		if strings.Contains(v, "admin_token_env") {
			if e != nil || len(c.AdminToken) != 24 {
				t.Fatalf("env loading: %v", e)
			}
		} else if e == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestIncomingTextFormattingOptIn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	for _, tc := range []struct {
		json string
		want bool
	}{
		{`{}`, false},
		{`{"incoming_text_escapes":false}`, false},
		{`{"incoming_text_escapes":true}`, true},
	} {
		if err := os.WriteFile(p, []byte(tc.json), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := Load(p)
		if err != nil || c.IncomingTextEscapes != tc.want || c.OutboundEnabled || c.BarkKey != "" {
			t.Fatal("text formatting changed action defaults", err)
		}
	}
}

func TestAllExampleConfigsValidateWithSyntheticSecretsAndOutboundOff(t *testing.T) {
	t.Setenv("SIM_ADMIN_TOKEN", strings.Repeat("t", 24))
	t.Setenv("SIM_AES_KEY", "0123456789abcdef")
	t.Setenv("SIM_AES_IV", "fedcba9876543210")
	t.Setenv("SIM_BARK_KEY", "")
	paths, err := filepath.Glob("../../configs/*.example.json")
	if err != nil || len(paths) != 4 {
		t.Fatal("expected local, LAN, cloud and container templates", paths, err)
	}
	for _, path := range paths {
		c, err := Load(path)
		if err != nil || c.OutboundEnabled || c.SendDailyLimit != 5 || c.SendMaxSegments != 1 {
			t.Fatal("unsafe/unusable template", path, err)
		}
		if c.Environment == "cloud" && c.AESMode != 1 {
			t.Fatal("cloud/container template must follow the confirmed mode1 deployment choice", path)
		}
	}
}
