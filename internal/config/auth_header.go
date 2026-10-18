package config

import (
	"errors"
	"net/http"
	"strings"
)

// NormalizeAdminTokenHeader accepts HTTP field names that browsers can send
// without replacing transport, origin, cookie or content metadata.
func NormalizeAdminTokenHeader(name string) (string, error) {
	invalid := errors.New("invalid management authentication header")
	if strings.ContainsAny(name, "\r\n") {
		return "", invalid
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "Authorization", nil
	}
	if len(name) > 128 {
		return "", invalid
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return "", invalid
		}
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "sec-") || strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "content-") || strings.HasPrefix(lower, "access-control-request-") {
		return "", invalid
	}
	switch lower {
	case "accept", "accept-charset", "accept-encoding", "connection", "cookie", "cookie2", "date", "dnt", "expect", "host", "keep-alive", "origin", "permissions-policy", "referer", "set-cookie", "te", "trailer", "transfer-encoding", "upgrade", "user-agent", "via":
		return "", invalid
	}
	return http.CanonicalHeaderKey(name), nil
}

func (c Config) AdminTokenHeaderName() string {
	name, _ := NormalizeAdminTokenHeader(c.AdminTokenHeader)
	return name
}
