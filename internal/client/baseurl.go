package client

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const (
	schemeHTTPS = "https"
	schemeHTTP  = "http"
)

var (
	errBaseURLEmpty      = errors.New("base URL is empty")
	errBaseURLNoScheme   = errors.New("base URL must include a scheme (https://...)")
	errBaseURLBadScheme  = errors.New("base URL scheme must be http or https")
	errBaseURLNoHost     = errors.New("base URL must include a host")
	errBaseURLNotAbs     = errors.New("base URL must be absolute (https://host)")
	errBaseURLBadQuery   = errors.New("base URL must not contain a query string")
	errBaseURLHasFrag    = errors.New("base URL must not contain a fragment")
	errBaseURLUserinfo   = errors.New("base URL must not embed userinfo credentials")
	errBaseURLPlaintext  = errors.New("refusing to send API token over plaintext http://; set ANKRA_ALLOW_INSECURE_HTTP=1 for loopback dev only or use https://")
)

// NormalizeBaseURL validates and normalizes an API base URL.
//
// Behavior:
//   - Empty input is rejected.
//   - Only http and https schemes are accepted; other schemes are rejected.
//   - Userinfo, query strings, and fragments are rejected to avoid surprises
//     when the client appends API paths.
//   - The trailing slash is trimmed for consistent path joining.
//   - http:// is allowed only for loopback hosts or when allowInsecureHTTP is true
//     (typically set from ANKRA_ALLOW_INSECURE_HTTP=1 for local development).
func NormalizeBaseURL(raw string, allowInsecureHTTP bool) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errBaseURLEmpty
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}

	if parsed.Scheme == "" {
		return "", errBaseURLNoScheme
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != schemeHTTPS && scheme != schemeHTTP {
		return "", errBaseURLBadScheme
	}
	if !parsed.IsAbs() {
		return "", errBaseURLNotAbs
	}
	if parsed.Host == "" {
		return "", errBaseURLNoHost
	}
	if parsed.User != nil {
		return "", errBaseURLUserinfo
	}
	if parsed.RawQuery != "" {
		return "", errBaseURLBadQuery
	}
	if parsed.Fragment != "" {
		return "", errBaseURLHasFrag
	}

	if scheme == schemeHTTP && !isLoopbackHost(parsed.Hostname()) && !allowInsecureHTTP {
		return "", errBaseURLPlaintext
	}

	parsed.Scheme = scheme
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

// isLoopbackHost returns true if the supplied hostname is a loopback address
// or the literal "localhost". This intentionally excludes link-local and
// private addresses; only the loopback exception is granted automatically.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	lower := strings.ToLower(host)
	if lower == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
