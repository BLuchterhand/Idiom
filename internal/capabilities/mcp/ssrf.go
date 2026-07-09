package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

type blockedAddressPrefix struct {
	prefix netip.Prefix
	reason string
}

var blockedAddressPrefixes = []blockedAddressPrefix{
	{prefix: netip.MustParsePrefix("100.64.0.0/10"), reason: "shared address space (RFC6598)"},
	{prefix: netip.MustParsePrefix("192.0.0.0/24"), reason: "IETF protocol assignments"},
	{prefix: netip.MustParsePrefix("192.0.2.0/24"), reason: "documentation range"},
	{prefix: netip.MustParsePrefix("198.18.0.0/15"), reason: "benchmarking range"},
	{prefix: netip.MustParsePrefix("198.51.100.0/24"), reason: "documentation range"},
	{prefix: netip.MustParsePrefix("203.0.113.0/24"), reason: "documentation range"},
	{prefix: netip.MustParsePrefix("240.0.0.0/4"), reason: "reserved range"},
	{prefix: netip.MustParsePrefix("64:ff9b:1::/48"), reason: "local-use translation range"},
	{prefix: netip.MustParsePrefix("100::/64"), reason: "discard-only range"},
	{prefix: netip.MustParsePrefix("2001:2::/48"), reason: "benchmarking range"},
	{prefix: netip.MustParsePrefix("2001:db8::/32"), reason: "documentation range"},
}

type httpNetworkPolicy struct {
	allowPrivate bool
	lookupIPAddr func(context.Context, string) ([]net.IPAddr, error)
	dialContext  func(context.Context, string, string) (net.Conn, error)
}

func newHTTPNetworkPolicy(allowPrivate bool) httpNetworkPolicy {
	dialer := &net.Dialer{}
	return httpNetworkPolicy{
		allowPrivate: allowPrivate,
		lookupIPAddr: net.DefaultResolver.LookupIPAddr,
		dialContext:  dialer.DialContext,
	}
}

func (policy httpNetworkPolicy) validateURL(ctx context.Context, rawURL string) (*url.URL, []net.IP, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, nil, errors.New("parse url: invalid URL")
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

	resolved, err := policy.resolveAllowedIPs(ctx, host)
	if err != nil {
		return nil, nil, err
	}

	ips := make([]net.IP, 0, len(resolved))
	for _, addr := range resolved {
		ips = append(ips, append(net.IP(nil), addr.IP...))
	}

	return parsed, ips, nil
}

func (policy httpNetworkPolicy) resolveAllowedIPs(ctx context.Context, host string) ([]net.IPAddr, error) {
	if policy.lookupIPAddr == nil {
		return nil, fmt.Errorf("resolve %q: no resolver configured", host)
	}

	addresses, err := policy.lookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("no IPs resolved for %q", host)
	}

	for _, address := range addresses {
		if address.IP == nil {
			return nil, fmt.Errorf("host %q resolved to an empty IP address", host)
		}
		if !policy.allowPrivate {
			if reason, blocked := classifyBlockedIP(address.IP); blocked {
				return nil, fmt.Errorf("host %q resolves to %s (%s); set Workspace.AllowPrivateNetwork to override", host, address.IP, reason)
			}
		}
	}

	return addresses, nil
}

// dialValidated resolves and validates the destination at connection time,
// then dials the validated IP directly. Dialing the IP prevents a second DNS
// lookup from changing the destination between policy evaluation and connect.
func (policy httpNetworkPolicy) dialValidated(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split dial address %q: %w", address, err)
	}

	addresses, err := policy.resolveAllowedIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	if policy.dialContext == nil {
		return nil, fmt.Errorf("dial %q: no dialer configured", host)
	}

	var dialErrors []error
	for _, resolved := range addresses {
		target := net.JoinHostPort(resolved.String(), port)
		connection, dialErr := policy.dialContext(ctx, network, target)
		if dialErr == nil {
			return connection, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		dialErrors = append(dialErrors, fmt.Errorf("%s: %w", target, dialErr))
	}

	return nil, fmt.Errorf("dial %q: %w", host, errors.Join(dialErrors...))
}

// classifyBlockedIP returns a reason string and true when ip falls in one
// of the IP ranges we reject by default. The order matters only for the
// error message: more specific reasons (loopback, link-local) are
// preferred over the generic "private" bucket.
func classifyBlockedIP(ip net.IP) (string, bool) {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return "invalid address", true
	}
	address = address.Unmap()
	for _, blocked := range blockedAddressPrefixes {
		if blocked.prefix.Contains(address) {
			return blocked.reason, true
		}
	}

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

// redactURL strips the query string before formatting a URL for logs or
// error messages. Hosted MCP servers like Exa accept their API key as a
// query-string parameter, and we do not want a stray log line to leak
// it. The path and host are preserved.
func redactURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "<unparseable url>"
	}

	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed.String()
}
