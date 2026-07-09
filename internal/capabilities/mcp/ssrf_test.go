package mcp

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestHTTPNetworkPolicyValidateURL(t *testing.T) {
	cases := []struct {
		name         string
		url          string
		allowPrivate bool
		wantErrSub   string // substring; empty means success
	}{
		// Public IPs (literal, no DNS) — accepted.
		{"https public ip", "https://1.1.1.1/mcp", false, ""},
		{"http public ip", "http://8.8.8.8/", false, ""},
		{"https public ipv6", "https://[2606:4700:4700::1111]/mcp", false, ""},
		{"below shared address boundary", "http://100.63.255.255/", false, ""},
		{"above shared address boundary", "http://100.128.0.0/", false, ""},

		// Schemes other than http/https — rejected.
		{"file scheme", "file:///etc/passwd", false, "scheme"},
		{"javascript scheme", "javascript:alert(1)", false, "scheme"},
		{"ftp scheme", "ftp://example.com/", false, "scheme"},
		{"gopher scheme", "gopher://example.com/", false, "scheme"},
		{"empty scheme", "//example.com/", false, "scheme"},

		// Loopback.
		{"loopback ipv4", "http://127.0.0.1/", false, "loopback"},
		{"loopback ipv4 alt", "http://127.5.5.5/", false, "loopback"},
		{"loopback ipv6", "http://[::1]/", false, "loopback"},
		{"loopback ipv4 mapped ipv6", "http://[::ffff:127.0.0.1]/", false, "loopback"},
		{"localhost name", "http://localhost/", false, "loopback"},

		// Link-local.
		{"link-local ipv4", "http://169.254.169.254/", false, "link-local"},
		{"link-local ipv6", "http://[fe80::1]/", false, "link-local"},

		// RFC1918 private.
		{"rfc1918 10/8", "http://10.0.0.1/", false, "private"},
		{"rfc1918 172.16/12", "http://172.16.0.1/", false, "private"},
		{"rfc1918 192.168/16", "http://192.168.1.1/", false, "private"},
		{"rfc6598 shared start", "http://100.64.0.1/", false, "shared"},
		{"rfc6598 shared end", "http://100.127.255.254/", false, "shared"},
		{"benchmarking ipv4", "http://198.18.0.1/", false, "benchmarking"},
		{"documentation ipv4", "http://203.0.113.1/", false, "documentation"},
		{"reserved ipv4", "http://240.0.0.1/", false, "reserved"},

		// Unique-local IPv6.
		{"unique-local ipv6", "http://[fc00::1]/", false, "private"},
		{"documentation ipv6", "http://[2001:db8::1]/", false, "documentation"},

		// Multicast / unspecified. 224.0.0.0/24 is link-local multicast
		// (caught by the more specific branch); 239.255.0.1 is the
		// admin-scoped multicast space, which exercises the generic
		// multicast branch.
		{"multicast link-local ipv4", "http://224.0.0.1/", false, "link-local"},
		{"multicast admin ipv4", "http://239.255.0.1/", false, "multicast"},
		{"unspecified ipv4", "http://0.0.0.0/", false, "unspecified"},

		// Missing host.
		{"no host", "http:///path", false, "no host"},

		// allowPrivate overrides the host-class checks.
		{"allow private rfc1918", "http://10.0.0.1/", true, ""},
		{"allow private loopback", "http://127.0.0.1/", true, ""},
		{"allow private link-local", "http://169.254.169.254/", true, ""},

		// allowPrivate does not relax the scheme check.
		{"allow private bad scheme", "ftp://10.0.0.1/", true, "scheme"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, ips, err := newHTTPNetworkPolicy(tc.allowPrivate).validateURL(context.Background(), tc.url)
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}

				if parsed == nil {
					t.Fatalf("expected parsed URL, got nil")
				}

				if len(ips) == 0 {
					t.Fatalf("expected at least one resolved IP, got none")
				}

				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q, got nil (parsed=%v ips=%v)", tc.wantErrSub, parsed, ips)
			}

			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErrSub)) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErrSub, err.Error())
			}
		})
	}
}

func TestHTTPNetworkPolicyRejectsDNSRebindingAtDial(t *testing.T) {
	lookups := 0
	dialed := false
	policy := httpNetworkPolicy{
		lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
			lookups++
			if lookups == 1 {
				return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, errors.New("unexpected dial")
		},
	}

	if _, _, err := policy.validateURL(context.Background(), "https://rebind.example/mcp"); err != nil {
		t.Fatalf("initial public resolution should pass: %v", err)
	}
	if _, err := policy.dialValidated(context.Background(), "tcp", "rebind.example:443"); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("dial-time private resolution error = %v, want loopback denial", err)
	}
	if dialed {
		t.Fatal("blocked dial-time address must not reach the network dialer")
	}
}

func TestHTTPNetworkPolicyDialsValidatedIPAddressDirectly(t *testing.T) {
	dialErr := errors.New("dial sentinel")
	var dialAddress string
	policy := httpNetworkPolicy{
		lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialAddress = address
			return nil, dialErr
		},
	}

	_, err := policy.dialValidated(context.Background(), "tcp", "public.example:443")
	if !errors.Is(err, dialErr) {
		t.Fatalf("dial error = %v, want wrapped sentinel", err)
	}
	if dialAddress != "8.8.8.8:443" {
		t.Fatalf("dial address = %q, want validated IP address", dialAddress)
	}
}

func TestHTTPNetworkPolicyValidateURLTrimsWhitespace(t *testing.T) {
	parsed, _, err := newHTTPNetworkPolicy(false).validateURL(context.Background(), "  https://1.1.1.1/  ")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if parsed.Host != "1.1.1.1" {
		t.Fatalf("expected host 1.1.1.1, got %q", parsed.Host)
	}
}

func TestRedactURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"strips query", "https://mcp.exa.ai/mcp?exaApiKey=secret", "https://mcp.exa.ai/mcp"},
		{"strips fragment", "https://example.com/p#section", "https://example.com/p"},
		{"strips both", "https://example.com/p?k=v#f", "https://example.com/p"},
		{"strips user info", "https://user:password@example.com/p", "https://example.com/p"},
		{"keeps host and path", "https://mcp.exa.ai/mcp", "https://mcp.exa.ai/mcp"},
		{"unparseable", string([]byte{0x7f, 0x00}), "<unparseable url>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactURL(tc.in)
			if got != tc.want {
				t.Fatalf("RedactURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
