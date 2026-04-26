package mcp

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateHTTPURL parses rawURL, requires the scheme to be http or https,
// resolves the hostname once, and rejects URLs that resolve to loopback,
// link-local, RFC1918 private, unique-local IPv6, multicast, or
// unspecified addresses unless allowPrivate is true. Returns the parsed
// URL and the resolved IPs on success.
//
// Resolution is one-shot: callers wire this into transport startup, not
// per request. The hosts we accept are taken from cfg.MCP entries (user
// config, not model output), so DNS rebind is not a relevant attack
// vector — the URL is not chosen by the model at request time.
func ValidateHTTPURL(rawURL string, allowPrivate bool) (*url.URL, []net.IP, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, nil, fmt.Errorf("parse url: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, nil, fmt.Errorf("unsupported scheme %q (only http and https are allowed)", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, nil, fmt.Errorf("url has no host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, nil, fmt.Errorf("no IPs resolved for %q", host)
	}
	if allowPrivate {
		return parsed, ips, nil
	}
	for _, ip := range ips {
		if reason, blocked := classifyBlockedIP(ip); blocked {
			return nil, nil, fmt.Errorf("host %q resolves to %s (%s); set Workspace.AllowPrivateNetwork to override", host, ip, reason)
		}
	}
	return parsed, ips, nil
}

// classifyBlockedIP returns a reason string and true when ip falls in one
// of the IP ranges we reject by default. The order matters only for the
// error message: more specific reasons (loopback, link-local) are
// preferred over the generic "private" bucket.
func classifyBlockedIP(ip net.IP) (string, bool) {
	if ip.IsUnspecified() {
		return "unspecified", true
	}
	if ip.IsLoopback() {
		return "loopback", true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "link-local", true
	}
	if ip.IsMulticast() {
		return "multicast", true
	}
	if ip.IsPrivate() {
		return "private (RFC1918 or unique-local)", true
	}
	return "", false
}

// RedactURL strips the query string before formatting a URL for logs or
// error messages. Hosted MCP servers like Exa accept their API key as a
// query-string parameter, and we do not want a stray log line to leak
// it. The path and host are preserved.
func RedactURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "<unparseable url>"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
