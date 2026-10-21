package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Config struct {
	StatusAlertsEnabled    *bool          `json:"status_alerts_enabled,omitempty"`
	LogLevel               string         `json:"log_level"`
	LogMaxBytes            int64          `json:"log_max_bytes"`
	LogRetentionDays       int            `json:"log_retention_days"`
	LogBackups             int            `json:"log_backups"`
	DiagnosticsLimit       int            `json:"diagnostics_limit"`
	OutboundEnabled        bool           `json:"outbound_enabled"`
	IncomingTextEscapes    bool           `json:"incoming_text_escapes"`
	SendIntervalSeconds    int            `json:"send_interval_seconds"`
	SendDailyLimit         int            `json:"send_daily_limit"`
	SendMaxSegments        int            `json:"send_max_segments"`
	Environment            string         `json:"environment"`
	HTTPAddr               string         `json:"http_address"`
	TCPAddr                string         `json:"tcp_address"`
	DataDir                string         `json:"data_directory"`
	DeviceID               string         `json:"device_id"`
	DeviceCIDRs            []string       `json:"device_cidrs"`
	DeviceAllowlistEnabled *bool          `json:"device_allowlist_enabled,omitempty"`
	AESMode                int            `json:"aes_mode"`
	AESKeyEnv              string         `json:"aes_key_env"`
	AESIVEnv               string         `json:"aes_iv_env"`
	AdminTokenEnv          string         `json:"admin_token_env"`
	AdminTokenHeader       string         `json:"admin_token_header,omitempty"`
	BarkKeyEnv             string         `json:"bark_key_env"`
	BarkBaseURL            string         `json:"bark_base_url"`
	PublicURL              string         `json:"public_url"`
	Timezone               string         `json:"device_timezone"`
	StaleSeconds           int            `json:"stale_seconds"`
	StaticDir              string         `json:"static_directory"`
	AESKey                 string         `json:"-"`
	AESIV                  string         `json:"-"`
	AdminToken             string         `json:"-"`
	BarkKey                string         `json:"-"`
	Allowed                []netip.Prefix `json:"-"`
	Location               *time.Location `json:"-"`
	Credentials            *Credentials   `json:"credentials,omitempty"`
	VoicemailUploadMode    string         `json:"voicemail_upload_mode,omitempty"`
	VoicemailProxyCIDRs    []string       `json:"voicemail_proxy_cidrs,omitempty"`
	VoicemailMaxStorageMiB int64          `json:"voicemail_max_storage_mib,omitempty"`
	VoicemailEnabled       bool           `json:"voicemail_enabled"`
	VoicemailSlots         []int          `json:"voicemail_slots,omitempty"`
	VoicemailTTS           string         `json:"voicemail_tts,omitempty"`
	VoicemailAMRURL        string         `json:"voicemail_amr_url,omitempty"`
}

type Credentials struct {
	AESKey     string `json:"aes_key"`
	AESIV      string `json:"aes_iv"`
	AdminToken string `json:"admin_token"`
	BarkKey    string `json:"bark_key"`
}

func Load(path string) (Config, error) {
	c := Config{SendIntervalSeconds: 60, SendDailyLimit: 5, SendMaxSegments: 1, Environment: "local", HTTPAddr: "127.0.0.1:8080", TCPAddr: "127.0.0.1:9000", DataDir: "data/local", DeviceID: "SIM-TEST", DeviceCIDRs: []string{"127.0.0.1/32", "::1/128"}, Timezone: "Asia/Shanghai", StaleSeconds: 345, StaticDir: "web/dist", BarkBaseURL: "https://api.day.app"}
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() > 65536 {
		return c, errors.New("configuration exceeds size limit")
	}
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return c, errors.New("configuration must contain one JSON object")
	}
	if c.Credentials != nil {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return c, errors.New("credential file must be a private regular file")
		}
		c.AESKey, c.AESIV, c.AdminToken, c.BarkKey = c.Credentials.AESKey, c.Credentials.AESIV, c.Credentials.AdminToken, c.Credentials.BarkKey
	} else {
		c.AESKey = os.Getenv(c.AESKeyEnv)
		c.AESIV = os.Getenv(c.AESIVEnv)
		c.AdminToken = os.Getenv(c.AdminTokenEnv)
		c.BarkKey = os.Getenv(c.BarkKeyEnv)
	}
	return c, c.Validate()
}

func loopback(address string) bool {
	h, _, e := net.SplitHostPort(address)
	if e != nil {
		return false
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (c *Config) Validate() error {
	if _, err := NormalizeAdminTokenHeader(c.AdminTokenHeader); err != nil {
		return err
	}
	c.AdminTokenHeader = strings.TrimSpace(c.AdminTokenHeader)
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.LogMaxBytes == 0 {
		c.LogMaxBytes = 16 * 1024 * 1024
	}
	if c.LogBackups == 0 {
		c.LogBackups = 3
	}
	if c.DiagnosticsLimit == 0 {
		c.DiagnosticsLimit = 5000
	}
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		return errors.New("invalid log level")
	}
	if c.LogMaxBytes < 65536 || c.LogMaxBytes > 16*1024*1024 || c.LogBackups < 1 || c.LogBackups > 5 || c.DiagnosticsLimit < 100 || c.DiagnosticsLimit > 5000 {
		return errors.New("invalid diagnostic retention limits")
	}
	if c.LogRetentionDays == 0 {
		c.LogRetentionDays = 7
	}
	if c.LogRetentionDays < 1 || c.LogRetentionDays > 30 {
		return errors.New("invalid log retention days")
	}
	// Zero values in directly constructed configs remain safe while outbound is disabled.
	if c.SendIntervalSeconds == 0 {
		c.SendIntervalSeconds = 60
	}
	if c.SendDailyLimit == 0 {
		c.SendDailyLimit = 5
	}
	if c.SendMaxSegments == 0 {
		c.SendMaxSegments = 1
	}
	if c.SendIntervalSeconds < 10 || c.SendIntervalSeconds > 86400 || c.SendDailyLimit < 1 || c.SendDailyLimit > 100 || c.SendMaxSegments < 1 || c.SendMaxSegments > 10 {
		return errors.New("invalid outbound safety limits")
	}
	if c.Environment != "local" && c.Environment != "cloud" {
		return errors.New("environment must be local or cloud")
	}
	for _, a := range []string{c.HTTPAddr, c.TCPAddr} {
		h, p, e := net.SplitHostPort(a)
		port, pe := strconv.Atoi(p)
		if e != nil || net.ParseIP(h) == nil || pe != nil || port < 1 || port > 65535 {
			return errors.New("listen addresses must use an explicit IP and port")
		}
	}
	if c.DataDir == "" || strings.TrimSpace(c.DeviceID) == "" || len(c.DeviceID) > 128 || c.StaleSeconds < 5 || c.StaleSeconds > 86400 {
		return errors.New("data directory, device ID and stale interval are required")
	}
	if c.AESMode < 0 || c.AESMode > 2 {
		return errors.New("invalid AES mode")
	}
	if c.AESMode > 0 && (len(c.AESKey) != 16 || len(c.AESIV) != 16) {
		return errors.New("AES key and IV must each contain 16 bytes in configured environment variables")
	}
	if (!loopback(c.HTTPAddr) || c.Environment == "cloud") && len(c.AdminToken) < 24 {
		return errors.New("non-loopback/cloud management requires an access token of at least 24 characters")
	}
	if len(c.DeviceCIDRs) == 0 && c.AllowlistEnabled() {
		return errors.New("device source CIDRs required")
	}
	if !c.AllowlistEnabled() && c.AESMode == 0 {
		return errors.New("disabling device allowlist requires AES")
	}
	c.Allowed = nil
	for _, v := range c.DeviceCIDRs {
		p, e := netip.ParsePrefix(v)
		if e != nil || p.Bits() == 0 {
			return fmt.Errorf("restricted device CIDR required: %s", v)
		}
		c.Allowed = append(c.Allowed, p.Masked())
	}
	if c.Environment == "cloud" && c.AESMode == 0 {
		return errors.New("cloud device channel requires AES mode 1 or 2; mode 1 leaves commands plaintext and CBC alone is not authentication")
	}
	for _, address := range []string{c.BarkBaseURL, c.PublicURL} {
		if address == "" && c.BarkKey == "" {
			continue
		}
		u, err := url.Parse(address)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("Bark and public URLs must be absolute URLs without credentials, query or fragment")
		}
		ip := net.ParseIP(u.Hostname())
		localHTTP := c.Environment == "local" && u.Scheme == "http" && ip != nil && (ip.IsLoopback() || ip.IsPrivate())
		if u.Scheme != "https" && !localHTTP {
			return errors.New("URLs require HTTPS except explicit local IP URLs")
		}
	}
	loc, e := time.LoadLocation(c.Timezone)
	if e != nil {
		return errors.New("invalid device timezone")
	}
	c.Location = loc
	return c.normalizeVoicemail()
}

func (c *Config) normalizeVoicemail() error {
	if err := c.validateVoicemailIngress(); err != nil {
		return err
	}
	if len(c.VoicemailSlots) > 2 {
		return errors.New("invalid voicemail slots")
	}
	seen := map[int]bool{}
	for _, slot := range c.VoicemailSlots {
		if (slot != 1 && slot != 2) || seen[slot] {
			return errors.New("invalid voicemail slots")
		}
		seen[slot] = true
	}
	sort.Ints(c.VoicemailSlots)
	if c.VoicemailTTS != "" && (utf8.RuneCountInString(c.VoicemailTTS) > 274 || strings.ContainsAny(c.VoicemailTTS, "\x00\r\n")) {
		return errors.New("invalid voicemail prompt")
	}
	if c.VoicemailAMRURL != "" && validateVoicemailURL(c.VoicemailAMRURL, c.VoicemailMode()) != nil {
		return errors.New("invalid voicemail upload URL")
	}
	if c.VoicemailEnabled && (len(c.VoicemailSlots) == 0 || strings.TrimSpace(c.VoicemailTTS) == "" || c.VoicemailAMRURL == "") {
		return errors.New("voicemail requires slots, prompt and upload URL")
	}
	return nil
}

func (c Config) Allows(addr net.Addr) bool {
	h, _, e := net.SplitHostPort(addr.String())
	if e != nil {
		return false
	}
	a, e := netip.ParseAddr(h)
	if e != nil {
		return false
	}
	a = a.Unmap()
	if !c.AllowlistEnabled() {
		return true
	}
	for _, p := range c.Allowed {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (c Config) AllowlistEnabled() bool {
	return c.DeviceAllowlistEnabled == nil || *c.DeviceAllowlistEnabled
}

func (c Config) AlertsEnabled() bool { return c.StatusAlertsEnabled == nil || *c.StatusAlertsEnabled }
