package mcp

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("HTTP network policy", func() {
	ginkgo.DescribeTable("validating endpoint addresses",
		func(rawURL string, allowPrivate bool, wantErr string) {
			parsed, ips, err := newHTTPNetworkPolicy(allowPrivate).validateURL(context.Background(), rawURL)
			if wantErr != "" {
				gomega.Expect(err).To(gomega.HaveOccurred())
				gomega.Expect(strings.ToLower(err.Error())).To(gomega.ContainSubstring(strings.ToLower(wantErr)))
				return
			}
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(parsed).NotTo(gomega.BeNil())
			gomega.Expect(ips).NotTo(gomega.BeEmpty())
		},
		ginkgo.Entry("HTTPS public IPv4", "https://1.1.1.1/mcp", false, ""),
		ginkgo.Entry("HTTP public IPv4", "http://8.8.8.8/", false, ""),
		ginkgo.Entry("HTTPS public IPv6", "https://[2606:4700:4700::1111]/mcp", false, ""),
		ginkgo.Entry("below shared-address boundary", "http://100.63.255.255/", false, ""),
		ginkgo.Entry("above shared-address boundary", "http://100.128.0.0/", false, ""),
		ginkgo.Entry("file scheme", "file:///etc/passwd", false, "scheme"),
		ginkgo.Entry("JavaScript scheme", "javascript:alert(1)", false, "scheme"),
		ginkgo.Entry("FTP scheme", "ftp://example.com/", false, "scheme"),
		ginkgo.Entry("Gopher scheme", "gopher://example.com/", false, "scheme"),
		ginkgo.Entry("empty scheme", "//example.com/", false, "scheme"),
		ginkgo.Entry("IPv4 loopback", "http://127.0.0.1/", false, "loopback"),
		ginkgo.Entry("alternate IPv4 loopback", "http://127.5.5.5/", false, "loopback"),
		ginkgo.Entry("IPv6 loopback", "http://[::1]/", false, "loopback"),
		ginkgo.Entry("IPv4-mapped IPv6 loopback", "http://[::ffff:127.0.0.1]/", false, "loopback"),
		ginkgo.Entry("localhost", "http://localhost/", false, "loopback"),
		ginkgo.Entry("IPv4 link-local", "http://169.254.169.254/", false, "link-local"),
		ginkgo.Entry("IPv6 link-local", "http://[fe80::1]/", false, "link-local"),
		ginkgo.Entry("RFC1918 10/8", "http://10.0.0.1/", false, "private"),
		ginkgo.Entry("RFC1918 172.16/12", "http://172.16.0.1/", false, "private"),
		ginkgo.Entry("RFC1918 192.168/16", "http://192.168.1.1/", false, "private"),
		ginkgo.Entry("RFC6598 shared start", "http://100.64.0.1/", false, "shared"),
		ginkgo.Entry("RFC6598 shared end", "http://100.127.255.254/", false, "shared"),
		ginkgo.Entry("benchmark IPv4", "http://198.18.0.1/", false, "benchmarking"),
		ginkgo.Entry("documentation IPv4", "http://203.0.113.1/", false, "documentation"),
		ginkgo.Entry("reserved IPv4", "http://240.0.0.1/", false, "reserved"),
		ginkgo.Entry("unique-local IPv6", "http://[fc00::1]/", false, "private"),
		ginkgo.Entry("documentation IPv6", "http://[2001:db8::1]/", false, "documentation"),
		ginkgo.Entry("link-local multicast IPv4", "http://224.0.0.1/", false, "link-local"),
		ginkgo.Entry("admin multicast IPv4", "http://239.255.0.1/", false, "multicast"),
		ginkgo.Entry("unspecified IPv4", "http://0.0.0.0/", false, "unspecified"),
		ginkgo.Entry("missing host", "http:///path", false, "no host"),
		ginkgo.Entry("authorized private RFC1918", "http://10.0.0.1/", true, ""),
		ginkgo.Entry("authorized private loopback", "http://127.0.0.1/", true, ""),
		ginkgo.Entry("authorized private link-local", "http://169.254.169.254/", true, ""),
		ginkgo.Entry("private authorization does not permit bad schemes", "ftp://10.0.0.1/", true, "scheme"),
	)

	ginkgo.It("rejects DNS rebinding before reaching the network dialer", func() {
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
		_, _, err := policy.validateURL(context.Background(), "https://rebind.example/mcp")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = policy.dialValidated(context.Background(), "tcp", "rebind.example:443")
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("loopback")))
		gomega.Expect(dialed).To(gomega.BeFalse())
	})

	ginkgo.It("dials the validated public IP directly", func() {
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
		gomega.Expect(err).To(gomega.MatchError(dialErr))
		gomega.Expect(dialAddress).To(gomega.Equal("8.8.8.8:443"))
	})

	ginkgo.It("trims endpoint whitespace", func() {
		parsed, _, err := newHTTPNetworkPolicy(false).validateURL(context.Background(), "  https://1.1.1.1/  ")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(parsed.Host).To(gomega.Equal("1.1.1.1"))
	})
})

var _ = ginkgo.Describe("URL redaction", func() {
	ginkgo.DescribeTable("removing sensitive URL components",
		func(input string, want string) {
			gomega.Expect(redactURL(input)).To(gomega.Equal(want))
		},
		ginkgo.Entry("query", "https://mcp.exa.ai/mcp?exaApiKey=secret", "https://mcp.exa.ai/mcp"),
		ginkgo.Entry("fragment", "https://example.com/p#section", "https://example.com/p"),
		ginkgo.Entry("query and fragment", "https://example.com/p?k=v#f", "https://example.com/p"),
		ginkgo.Entry("user info", "https://user:password@example.com/p", "https://example.com/p"),
		ginkgo.Entry("safe host and path", "https://mcp.exa.ai/mcp", "https://mcp.exa.ai/mcp"),
		ginkgo.Entry("unparseable URL", string([]byte{0x7f, 0x00}), "<unparseable url>"),
	)
})
