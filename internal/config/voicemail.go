package config

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var voicemailTokenPattern = regexp.MustCompile(`^[0-9a-f]{32,64}$`)
var voicemailHostname = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// An omitted mode retains the LAN-only policy of existing installations.
func (c Config) VoicemailMode() string {
	if c.VoicemailUploadMode == "" {
		return "lan"
	}
	return c.VoicemailUploadMode
}

func (c Config) VoicemailStorageLimit() int64 {
	if c.VoicemailMaxStorageMiB == 0 {
		return 512 << 20
	}
	return c.VoicemailMaxStorageMiB << 20
}

func (c Config) VoicemailProxies() []string {
	if len(c.VoicemailProxyCIDRs) == 0 {
		return []string{"127.0.0.1/32", "::1/128"}
	}
	return c.VoicemailProxyCIDRs
}

func (c Config) validateVoicemailIngress() error {
	if c.VoicemailMode() != "lan" && c.VoicemailMode() != "public" {
		return errors.New("invalid voicemail upload mode")
	}
	if c.VoicemailMode() == "public" && len(c.AdminToken) < 24 {
		return errors.New("public voicemail requires management authentication")
	}
	if c.VoicemailMaxStorageMiB != 0 && (c.VoicemailMaxStorageMiB < 16 || c.VoicemailMaxStorageMiB > 65536) {
		return errors.New("invalid voicemail storage limit")
	}
	if len(c.VoicemailProxyCIDRs) > 16 {
		return errors.New("too many voicemail proxies")
	}
	for _, raw := range c.VoicemailProxies() {
		p, err := netip.ParsePrefix(raw)
		if err != nil || p.Bits() != p.Addr().BitLen() || p.Addr().Is4In6() || !(p.Addr().IsLoopback() || p.Addr().IsPrivate()) {
			return errors.New("voicemail proxies require individual private or loopback IPs")
		}
	}
	return nil
}

// Public ingress authenticates the connector independently of the TCP board
// allowlist. Forwarded client-IP headers are never used as authentication.
func (c Config) AllowsVoicemailProxy(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	for _, raw := range c.VoicemailProxies() {
		p, err := netip.ParsePrefix(raw)
		if err == nil && p.Contains(a) {
			return true
		}
	}
	return false
}

func (c Config) VoicemailReceptionReady() bool {
	return c.VoicemailEnabled && (c.VoicemailMode() == "public" || c.AllowlistEnabled())
}

func validateVoicemailURL(raw string, mode string) error {
	rejected := errors.New("invalid voicemail upload URL")
	if raw == "" || len(raw) > 500 || strings.ContainsAny(raw, "\r\n\x00") {
		return rejected
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return rejected
	}
	if mode == "lan" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || u.Port() == "" || ip == nil || ip.To4() == nil || !(ip.IsPrivate() || ip.IsLoopback()) {
			return rejected
		}
	} else if mode == "public" {
		if u.Scheme != "https" || net.ParseIP(u.Hostname()) != nil || len(u.Hostname()) > 253 {
			return rejected
		}
		labels := strings.Split(u.Hostname(), ".")
		if len(labels) < 2 {
			return rejected
		}
		for _, label := range labels {
			if !voicemailHostname.MatchString(label) {
				return rejected
			}
		}
	} else {
		return rejected
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return rejected
		}
	}
	// Public URLs use canonical paths; legacy LAN URLs keep their old acceptance.
	if mode == "public" && u.RawPath != "" {
		return rejected
	}
	token := strings.TrimPrefix(u.Path, "/api/voicemail/amr/")
	if !strings.HasPrefix(u.Path, "/api/voicemail/amr/") || !voicemailTokenPattern.MatchString(token) {
		return rejected
	}
	return nil
}

func VoicemailUploadToken(raw string, mode ...string) (string, bool) {
	selected := "lan"
	if len(mode) > 0 {
		selected = mode[0]
	}
	if validateVoicemailURL(raw, selected) != nil {
		return "", false
	}
	u, _ := url.Parse(raw)
	return strings.TrimPrefix(u.Path, "/api/voicemail/amr/"), true
}
